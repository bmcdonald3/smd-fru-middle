package magellan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInventoryNormalizesURIs(t *testing.T) {
	var got struct {
		BMC      string `json:"bmc"`
		SecretID string `json:"secretID"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer token, got %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"bmc": "http://bmc.example",
			"systems": [{
				"uri": "http://bmc.example/redfish/v1/Systems/System-1",
				"ethernet_interfaces": [{"uri": "http://bmc.example/redfish/v1/Systems/System-1/EthernetInterfaces/1", "ip": "10.0.0.5"}]
			}],
			"managers": [{"uri": "http://bmc.example/redfish/v1/Managers/BMC-1"}]
		}`))
	}))
	defer srv.Close()

	systems, managers, err := NewClient(srv.URL, "tok", 5*time.Second, false).
		Inventory(context.Background(), "http://bmc.example", "bmc-x0c0s1b0")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}

	if got.BMC != "http://bmc.example" || got.SecretID != "bmc-x0c0s1b0" {
		t.Errorf("unexpected request body: %+v", got)
	}
	if len(systems) != 1 || systems[0].URI != "/redfish/v1/Systems/System-1" {
		t.Errorf("system URI not normalized: %+v", systems)
	}
	if systems[0].EthernetInterfaces[0].URI != "/redfish/v1/Systems/System-1/EthernetInterfaces/1" {
		t.Errorf("interface URI not normalized: %+v", systems[0].EthernetInterfaces)
	}
	if len(managers) != 1 || managers[0].URI != "/redfish/v1/Managers/BMC-1" {
		t.Errorf("manager URI not normalized: %+v", managers)
	}
}

func TestInventoryPropagatesErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no ServiceRoot found", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, _, err := NewClient(srv.URL, "", time.Second, false).
		Inventory(context.Background(), "http://bmc.example", "bmc-1")
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
}

// SMD never contacts a BMC, so this payload is its only source of MACs. NICs
// with a MAC must survive even when the BMC reports no IP for them, which is
// normal for host NICs.
func TestInventoryKeepsMACOnlyInterfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"systems": [{
				"uri": "http://bmc.example/redfish/v1/Systems/1",
				"ethernet_interfaces": [
					{"uri": "http://bmc.example/redfish/v1/Systems/1/EthernetInterfaces/2", "mac": "aa:bb", "ip": "10.0.0.5"},
					{"uri": "http://bmc.example/redfish/v1/Systems/1/EthernetInterfaces/3", "mac": "ff:ff"},
					{"uri": "http://bmc.example/redfish/v1/Systems/1/EthernetInterfaces/1", "mac": "cc:dd"},
					{"uri": "http://bmc.example/redfish/v1/Systems/1/EthernetInterfaces/4"}
				]
			}],
			"managers": [{
				"uri": "http://bmc.example/redfish/v1/Managers/BMC",
				"ethernet_interfaces": [{"uri": "http://bmc.example/redfish/v1/Managers/BMC/EthernetInterfaces/1", "mac": "ee:ff", "ip": "172.24.0.3"}]
			}]
		}`))
	}))
	defer srv.Close()

	systems, managers, err := NewClient(srv.URL, "", 5*time.Second, false).
		Inventory(context.Background(), "http://bmc.example", "s")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}

	nics := systems[0].EthernetInterfaces
	if len(nics) != 3 {
		t.Fatalf("expected 3 NICs with a MAC, got %d: %+v", len(nics), nics)
	}
	// Sorted by URI: /1 (MAC only), /2 (MAC+IP), /3 (MAC only). The identity-less
	// NIC /4 is dropped.
	if nics[0].MAC != "cc:dd" || nics[0].IP != "" {
		t.Errorf("MAC-only NIC not preserved: %+v", nics[0])
	}
	if nics[0].URI != "/redfish/v1/Systems/1/EthernetInterfaces/1" {
		t.Errorf("NIC URI not normalized: %q", nics[0].URI)
	}
	for _, n := range nics {
		if n.MAC == "" {
			t.Errorf("NIC with no identity survived filtering: %+v", n)
		}
	}
	if len(managers[0].EthernetInterfaces) != 1 {
		t.Errorf("manager NIC dropped: %+v", managers[0].EthernetInterfaces)
	}
}
