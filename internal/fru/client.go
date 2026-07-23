package fru

import (
	"context"
	"bytes"
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read FRU response: %w", err)
	}

	devices, err := decodeDevices(body)
	if err != nil {
		return nil, err
	}

	return devices, nil
}

func decodeDevices(body []byte) ([]models.Device, error) {
	var envelope struct {
		Items   []models.Device `json:"items"`
		Devices []models.Device `json:"devices"`
		Data    []models.Device `json:"data"`
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&envelope); err == nil {
		devices := envelope.Items
		if len(devices) == 0 {
			devices = envelope.Devices
		}
		if len(devices) == 0 {
			devices = envelope.Data
		}
		if len(devices) > 0 {
			return devices, nil
		}
	}

	var bare []models.Device
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&bare); err == nil {
		return bare, nil
	}

	return nil, fmt.Errorf("decode FRU response: unsupported JSON shape")
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
