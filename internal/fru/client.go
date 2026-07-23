package fru

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) ListDevices(ctx context.Context) ([]models.Device, error) {
	url := c.baseURL + "/devices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected FRU response status: %s", resp.Status)
	}

	var envelope struct {
		Items   []models.Device `json:"items"`
		Devices []models.Device `json:"devices"`
		Data    []models.Device `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode FRU response: %w", err)
	}

	devices := envelope.Items
	if len(devices) == 0 {
		devices = envelope.Devices
	}
	if len(devices) == 0 {
		devices = envelope.Data
	}

	if len(devices) == 0 {
		// Some endpoints return a bare array; retry decode if envelope is empty.
		if err := c.decodeBareArray(ctx, url, &devices); err != nil {
			return nil, err
		}
	}

	return devices, nil
}

func (c *Client) decodeBareArray(ctx context.Context, url string, out *[]models.Device) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build bare-array request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("execute bare-array request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected FRU bare-array response status: %s", resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode FRU bare-array response: %w", err)
	}

	return nil
}

func FilterChanged(devices []models.Device, mark models.Watermark) []models.Device {
	if mark.UpdatedAt.IsZero() {
		sorted := make([]models.Device, len(devices))
		copy(sorted, devices)
		sortDevices(sorted)
		return sorted
	}

	var changed []models.Device
	for _, device := range devices {
		t := device.Metadata.UpdatedAt
		uid := device.Metadata.UID

		if t.After(mark.UpdatedAt) || (t.Equal(mark.UpdatedAt) && uid > mark.UID) {
			changed = append(changed, device)
		}
	}

	sortDevices(changed)
	return changed
}

func sortDevices(devices []models.Device) {
	sort.Slice(devices, func(i, j int) bool {
		a := devices[i].Metadata.UpdatedAt
		b := devices[j].Metadata.UpdatedAt
		if a.Equal(b) {
			return devices[i].Metadata.UID < devices[j].Metadata.UID
		}
		return a.Before(b)
	})
}
