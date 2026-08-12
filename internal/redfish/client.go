package redfish

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

type Client struct {
	http *http.Client
}

func NewClient(timeout time.Duration, insecureTLS bool) *Client {
	transport := http.DefaultTransport
	if insecureTLS {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return &Client{http: &http.Client{Timeout: timeout, Transport: transport}}
}

func (c *Client) Discover(ctx context.Context, baseAddress string, creds models.Credentials) ([]models.System, []models.Manager, error) {
	baseAddress = strings.TrimSpace(baseAddress)
	if baseAddress == "" {
		return nil, nil, fmt.Errorf("redfish base address is empty")
	}

	rootURL := normalizeRedfishRoot(baseAddress)
	systems, err := c.discoverCollection(ctx, rootURL+"/Systems", creds, "ComputerSystem")
	if err != nil {
		return nil, nil, err
	}

	managerSystems, err := c.discoverCollection(ctx, rootURL+"/Managers", creds, "Manager")
	if err != nil {
		return nil, nil, err
	}

	for i := range systems {
		_ = c.enrichSystem(ctx, rootURL, &systems[i], creds)
	}

	managers := make([]models.Manager, 0, len(managerSystems))
	for _, m := range managerSystems {
		managers = append(managers, models.Manager{
			ID:   m.ID,
			Type: m.Type,
			URI:  m.URI,
		})
	}

	return systems, managers, nil
}

func normalizeRedfishRoot(address string) string {
	trimmed := strings.TrimRight(address, "/")
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if strings.HasSuffix(trimmed, "/redfish/v1") {
			return trimmed
		}
		return trimmed + "/redfish/v1"
	}
	return "https://" + trimmed + "/redfish/v1"
}

func (c *Client) discoverCollection(ctx context.Context, collectionURL string, creds models.Credentials, entryType string) ([]models.System, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, collectionURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build redfish request: %w", err)
	}
	req.SetBasicAuth(creds.Username, creds.Password)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query redfish collection %s: %w", collectionURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("redfish collection %s returned status %s", collectionURL, resp.Status)
	}

	var payload struct {
		Members []struct {
			ODataID string `json:"@odata.id"`
		} `json:"Members"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode redfish collection %s: %w", collectionURL, err)
	}

	entries := make([]models.System, 0, len(payload.Members))
	for _, member := range payload.Members {
		uri := strings.TrimSpace(member.ODataID)
		if uri == "" {
			continue
		}
		id := idFromURI(uri)
		entries = append(entries, models.System{ID: id, Type: entryType, URI: uri})
	}

	return entries, nil
}

func (c *Client) enrichSystem(ctx context.Context, rootURL string, system *models.System, creds models.Credentials) error {
	resourceURL, err := absoluteResourceURL(rootURL, system.URI)
	if err != nil {
		return err
	}

	var payload struct {
		UUID         string                     `json:"UUID"`
		Manufacturer string                     `json:"Manufacturer"`
		Model        string                     `json:"Model"`
		SerialNumber string                     `json:"SerialNumber"`
		BiosVersion  string                     `json:"BiosVersion"`
		SystemType   string                     `json:"SystemType"`
		PowerState   string                     `json:"PowerState"`
		Actions      map[string]json.RawMessage `json:"Actions"`
		Links        struct {
			Managers []struct {
				ODataID string `json:"@odata.id"`
			} `json:"Managers"`
		} `json:"Links"`
		ProcessorSummary struct {
			Count int    `json:"Count"`
			Model string `json:"Model"`
		} `json:"ProcessorSummary"`
		MemorySummary struct {
			TotalSystemMemoryGiB float64 `json:"TotalSystemMemoryGiB"`
		} `json:"MemorySummary"`
		EthernetInterfaces struct {
			ODataID string `json:"@odata.id"`
		} `json:"EthernetInterfaces"`
	}

	if err := c.fetchJSON(ctx, resourceURL, creds, &payload); err != nil {
		return err
	}

	system.UUID = strings.TrimSpace(payload.UUID)
	system.Manufacturer = strings.TrimSpace(payload.Manufacturer)
	system.Model = strings.TrimSpace(payload.Model)
	system.Name = strings.TrimSpace(payload.Model)
	system.Serial = strings.TrimSpace(payload.SerialNumber)
	system.BiosVersion = strings.TrimSpace(payload.BiosVersion)
	system.SystemType = strings.TrimSpace(payload.SystemType)
	system.Power = &models.Power{State: strings.TrimSpace(payload.PowerState)}
	system.ProcessorCount = payload.ProcessorSummary.Count
	system.ProcessorType = strings.TrimSpace(payload.ProcessorSummary.Model)
	if payload.MemorySummary.TotalSystemMemoryGiB > 0 {
		system.MemoryTotal = float32(math.Round(payload.MemorySummary.TotalSystemMemoryGiB*100) / 100)
	}

	if len(payload.Actions) > 0 {
		actions := make([]string, 0, len(payload.Actions))
		for key := range payload.Actions {
			normalized := strings.TrimSpace(strings.TrimPrefix(key, "#"))
			if normalized != "" {
				actions = append(actions, normalized)
			}
		}
		sort.Strings(actions)
		system.Actions = actions
	}

	if len(payload.Links.Managers) > 0 {
		managers := make([]string, 0, len(payload.Links.Managers))
		for _, manager := range payload.Links.Managers {
			uri := strings.TrimSpace(manager.ODataID)
			if uri != "" {
				managers = append(managers, uri)
			}
		}
		if len(managers) > 0 {
			if system.Links == nil {
				system.Links = &models.SystemLinks{}
			}
			system.Links.Managers = managers
		}
	}

	ethernetURI := strings.TrimSpace(payload.EthernetInterfaces.ODataID)
	if ethernetURI == "" {
		ethernetURI = strings.TrimRight(system.URI, "/") + "/EthernetInterfaces"
	}

	ifaces, err := c.discoverEthernetInterfaces(ctx, rootURL, ethernetURI, creds)
	if err == nil {
		system.EthernetInterfaces = ifaces
	}

	return nil
}

func (c *Client) discoverEthernetInterfaces(ctx context.Context, rootURL, collectionURI string, creds models.Credentials) ([]models.EthernetInterface, error) {
	collectionURL, err := absoluteResourceURL(rootURL, collectionURI)
	if err != nil {
		return nil, err
	}

	var collection struct {
		Members []struct {
			ODataID string `json:"@odata.id"`
		} `json:"Members"`
	}
	if err := c.fetchJSON(ctx, collectionURL, creds, &collection); err != nil {
		return nil, err
	}

	ifaces := make([]models.EthernetInterface, 0, len(collection.Members))
	for _, member := range collection.Members {
		memberURI := strings.TrimSpace(member.ODataID)
		if memberURI == "" {
			continue
		}
		iface, err := c.fetchEthernetInterface(ctx, rootURL, memberURI, creds)
		if err != nil {
			continue
		}
		ifaces = append(ifaces, iface)
	}

	sort.Slice(ifaces, func(i, j int) bool {
		return ifaces[i].URI < ifaces[j].URI
	})

	return ifaces, nil
}

func (c *Client) fetchEthernetInterface(ctx context.Context, rootURL, ifaceURI string, creds models.Credentials) (models.EthernetInterface, error) {
	resourceURL, err := absoluteResourceURL(rootURL, ifaceURI)
	if err != nil {
		return models.EthernetInterface{}, err
	}

	var payload struct {
		ODataID       string `json:"@odata.id"`
		MACAddress    string `json:"MACAddress"`
		Name          string `json:"Name"`
		Description   string `json:"Description"`
		LinkStatus    string `json:"LinkStatus"`
		IPv4Addresses []struct {
			Address string `json:"Address"`
		} `json:"IPv4Addresses"`
		IPv6Addresses []struct {
			Address string `json:"Address"`
		} `json:"IPv6Addresses"`
	}

	if err := c.fetchJSON(ctx, resourceURL, creds, &payload); err != nil {
		return models.EthernetInterface{}, err
	}

	iface := models.EthernetInterface{
		URI:         strings.TrimSpace(payload.ODataID),
		MAC:         strings.TrimSpace(payload.MACAddress),
		Name:        strings.TrimSpace(payload.Name),
		Description: strings.TrimSpace(payload.Description),
		Enabled:     strings.EqualFold(strings.TrimSpace(payload.LinkStatus), "LinkUp"),
	}

	if iface.URI == "" {
		iface.URI = strings.TrimSpace(ifaceURI)
	}

	for _, addr := range payload.IPv4Addresses {
		v := strings.TrimSpace(addr.Address)
		if v != "" {
			iface.IP = v
			break
		}
	}
	if iface.IP == "" {
		for _, addr := range payload.IPv6Addresses {
			v := strings.TrimSpace(addr.Address)
			if v != "" {
				iface.IP = v
				break
			}
		}
	}

	return iface, nil
}

func (c *Client) fetchJSON(ctx context.Context, requestURL string, creds models.Credentials, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("build redfish request: %w", err)
	}
	req.SetBasicAuth(creds.Username, creds.Password)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("query redfish resource %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("redfish resource %s returned status %s", requestURL, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode redfish resource %s: %w", requestURL, err)
	}

	return nil
}

func absoluteResourceURL(rootURL, uri string) (string, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return "", fmt.Errorf("redfish resource URI is empty")
	}

	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		return uri, nil
	}

	root, err := url.Parse(rootURL)
	if err != nil {
		return "", fmt.Errorf("parse redfish root URL %q: %w", rootURL, err)
	}

	root.Path = ""
	root.RawPath = ""
	root.RawQuery = ""
	root.Fragment = ""

	resource, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse redfish resource URI %q: %w", uri, err)
	}

	return root.ResolveReference(resource).String(), nil
}

func idFromURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}

	u, err := url.Parse(uri)
	if err == nil {
		uri = u.Path
	}
	parts := strings.Split(strings.Trim(uri, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
