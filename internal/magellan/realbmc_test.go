package magellan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

// Verbatim excerpt of what magellan returned for the Intel S2600BPB at
// 172.24.0.3: system NICs carry a MAC and no IP, the manager NIC carries both.
const realBMCInventory = `{
  "bmc": "https://172.24.0.3",
  "managers": [
    {
      "uri": "https://172.24.0.3/redfish/v1/Managers/BMC",
      "uuid": "0c88958e-a568-01eb-9f83-5442ee0f5cee",
      "name": "Manager",
      "model": "S2600BPB",
      "type": "BMC",
      "firmware_version": "2.86.2da97d3f",
      "ethernet_interfaces": [
        {
          "uri": "https://172.24.0.3/redfish/v1/Managers/BMC/EthernetInterfaces/1",
          "mac": "a4-bf-01-3f-6b-42",
          "ip": "172.24.0.3",
          "name": "BMC Ethernet Interface",
          "enabled": true
        }
      ]
    }
  ],
  "systems": [
    {
      "uri": "https://172.24.0.3/redfish/v1/Systems/QSBP82909087",
      "uuid": "317091ec-8be6-11e8-ab21-a4bf013f6b40",
      "manufacturer": "Intel Corporation",
      "system_type": "Physical",
      "name": "S2600BPB",
      "model": "S2600BPB",
      "serial": "QSBP82909087",
      "ethernet_interfaces": [
        {"uri": "https://172.24.0.3/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/1", "mac": "a4-bf-01-3f-6b-40", "name": "Computer System Ethernet Interface", "description": "System NIC 1"},
        {"uri": "https://172.24.0.3/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/2", "mac": "a4-bf-01-3f-6b-41", "name": "Computer System Ethernet Interface", "description": "System NIC 2"},
        {"uri": "https://172.24.0.3/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/3", "mac": "ff-ff-ff-ff-ff-ff", "name": "Computer System Ethernet Interface", "description": "System NIC 3"},
        {"uri": "https://172.24.0.3/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/4", "mac": "02-09-01-08-38-c5", "name": "Computer System Ethernet Interface", "description": "System NIC 4"},
        {"uri": "https://172.24.0.3/redfish/v1/Systems/QSBP82909087/EthernetInterfaces/5", "mac": "02-09-01-08-46-9a", "name": "Computer System Ethernet Interface", "description": "System NIC 5"}
      ]
    }
  ]
}`

// SMD is the only consumer of this payload and never contacts a BMC, so the
// MACs it stores for DHCP and boot come from here or nowhere.
func TestRealBMCPayloadCarriesSystemMACs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realBMCInventory))
	}))
	defer srv.Close()

	systems, managers, err := NewClient(srv.URL, "", 5*time.Second, false).
		Inventory(context.Background(), "https://172.24.0.3", "bmc-x3000c0s1b0")
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}

	if len(systems[0].EthernetInterfaces) != 5 {
		t.Fatalf("expected 5 system NICs, got %d: %+v",
			len(systems[0].EthernetInterfaces), systems[0].EthernetInterfaces)
	}

	body, err := json.Marshal(models.SMDRedfishEndpointPayload{
		SchemaVersion: 1,
		ID:            "x3000c0s1b0",
		Systems:       systems,
		Managers:      managers,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	for _, mac := range []string{
		"a4-bf-01-3f-6b-40", "a4-bf-01-3f-6b-41", "ff-ff-ff-ff-ff-ff",
		"02-09-01-08-38-c5", "02-09-01-08-46-9a",
	} {
		if !strings.Contains(string(body), mac) {
			t.Errorf("SMD payload is missing system MAC %s", mac)
		}
	}
	if !strings.Contains(string(body), `"a4-bf-01-3f-6b-42"`) {
		t.Error("SMD payload is missing the manager MAC")
	}

	// SMD keys ComponentEndpoints off the Redfish-relative path.
	if strings.Contains(string(body), "https://172.24.0.3/redfish") {
		t.Error("payload still carries absolute resource URIs")
	}
	t.Logf("payload: %s", body)
}
