// Package magellan is this service's client for the magellan BMC daemon.
//
// All BMC/Redfish interaction is delegated to magellan (RFD #133); this service
// never opens a connection to a BMC itself. Credentials are selected by secret
// ID so they are never sent over the wire — magellan resolves them from the
// shared secret store.
package magellan

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string, timeout time.Duration, insecureTLS bool) *Client {
	transport := http.DefaultTransport
	if insecureTLS {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: timeout, Transport: transport},
	}
}

type inventoryRequest struct {
	BMC      string `json:"bmc"`
	SecretID string `json:"secretID,omitempty"`
}

type inventoryResponse struct {
	BMC      string           `json:"bmc"`
	Systems  []models.System  `json:"systems"`
	Managers []models.Manager `json:"managers"`
}

// Inventory asks magellan to crawl the BMC at bmcAddress, using the credentials
// stored under secretID.
func (c *Client) Inventory(ctx context.Context, bmcAddress, secretID string) ([]models.System, []models.Manager, error) {
	body, err := json.Marshal(inventoryRequest{BMC: bmcAddress, SecretID: secretID})
	if err != nil {
		return nil, nil, fmt.Errorf("encode magellan inventory request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/inventory", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build magellan request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute magellan request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, nil, fmt.Errorf("magellan inventory returned status %s: %s", resp.Status, strings.TrimSpace(string(rbody)))
	}

	var out inventoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, fmt.Errorf("decode magellan inventory response: %w", err)
	}

	// Magellan reports absolute resource URIs; SMD keys ComponentEndpoints off
	// the Redfish-relative @odata.id, so reduce them back to their path.
	for i := range out.Systems {
		out.Systems[i].URI = redfishPath(out.Systems[i].URI)
		out.Systems[i].EthernetInterfaces = usableInterfaces(out.Systems[i].EthernetInterfaces)
	}
	for i := range out.Managers {
		out.Managers[i].URI = redfishPath(out.Managers[i].URI)
		out.Managers[i].EthernetInterfaces = usableInterfaces(out.Managers[i].EthernetInterfaces)
	}

	return out.Systems, out.Managers, nil
}

// usableInterfaces normalizes NIC URIs and drops interfaces that identify
// nothing. A MAC with no IP is kept: SMD never contacts a BMC itself, so this
// payload is its only source of MAC addresses for DHCP and boot.
func usableInterfaces(ifaces []models.EthernetInterface) []models.EthernetInterface {
	kept := make([]models.EthernetInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		if strings.TrimSpace(iface.MAC) == "" && strings.TrimSpace(iface.IP) == "" {
			continue
		}
		iface.URI = redfishPath(iface.URI)
		kept = append(kept, iface)
	}
	if len(kept) == 0 {
		return nil
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].URI < kept[j].URI })
	return kept
}

// redfishPath reduces an absolute resource URI to its Redfish path, leaving
// values that are already relative untouched.
func redfishPath(uri string) string {
	if idx := strings.Index(uri, "/redfish/"); idx >= 0 {
		return uri[idx:]
	}
	return uri
}
