package redfish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

type Client struct {
	http *http.Client
}

func NewClient(timeout time.Duration) *Client {
	return &Client{http: &http.Client{Timeout: timeout}}
}

func (c *Client) Discover(ctx context.Context, baseAddress string, creds models.Credentials) ([]models.System, []models.System, error) {
	baseAddress = strings.TrimSpace(baseAddress)
	if baseAddress == "" {
		return nil, nil, fmt.Errorf("redfish base address is empty")
	}

	rootURL := normalizeRedfishRoot(baseAddress)
	systems, err := c.discoverCollection(ctx, rootURL+"/Systems", creds, "ComputerSystem")
	if err != nil {
		return nil, nil, err
	}

	managers, err := c.discoverCollection(ctx, rootURL+"/Managers", creds, "Manager")
	if err != nil {
		return nil, nil, err
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
