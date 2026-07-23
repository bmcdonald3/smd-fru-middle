package smd
package smd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) UpsertRedfishEndpoint(ctx context.Context, payload models.SMDRedfishEndpointPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode SMD payload: %w", err)
	}

	url := fmt.Sprintf("%s/hsm/v2/Inventory/RedfishEndpoints/%s", c.baseURL, payload.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build SMD request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("execute SMD request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("SMD upsert returned status %s", resp.Status)
	}

	return nil
}
