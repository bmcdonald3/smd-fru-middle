# smd-fru-middle

Standalone FRU-to-SMD middleware service (V1).

## What Is Implemented

- Polls FRU-tracker `GET /devices` on an interval
- Tracks progress with a persistent watermark checkpoint (`updatedAt` + `uid`)
- Extracts candidate endpoints from FRU `spec.properties`
- Resolves credentials from a Magellan encrypted secret store (firmware-updater style)
- Discovers Redfish `Systems` and `Managers` collections
- Upserts SMD RedfishEndpoints using `PUT /hsm/v2/Inventory/RedfishEndpoints/{xname}`
- Supports dry-run mode for safe validation

## Required FRU Property Keys (V1)

- `xname` (or override via `FRU_MIDDLE_XNAME_PROPERTY_KEY`)
- `secret_id` (or override via `FRU_MIDDLE_SECRET_ID_PROPERTY_KEY`)
- `redfish_address` (or override via `FRU_MIDDLE_REDFISH_ADDR_PROPERTY_KEY`)

## Environment Variables

- `MASTER_KEY` (required): 64-character hex string (AES-256 key)
- `FRU_MIDDLE_FRU_BASE_URL` (default: `http://localhost:8080`)
- `FRU_MIDDLE_SMD_BASE_URL` (default: `http://localhost:27779`)
- `FRU_MIDDLE_POLL_INTERVAL` (default: `30s`)
- `FRU_MIDDLE_HTTP_TIMEOUT` (default: `20s`)
- `FRU_MIDDLE_CHECKPOINT_PATH` (default: `data/checkpoint.json`)
- `FRU_MIDDLE_SECRETS_FILE` (default: `secrets.json`)
- `FRU_MIDDLE_XNAME_PROPERTY_KEY` (default: `xname`)
- `FRU_MIDDLE_SECRET_ID_PROPERTY_KEY` (default: `secret_id`)
- `FRU_MIDDLE_REDFISH_ADDR_PROPERTY_KEY` (default: `redfish_address`)
- `FRU_MIDDLE_DRY_RUN` (default: `false`)

## Secret Management (firmware-updater-compatible pattern)

Create a secret:

```bash
MASTER_KEY=<64-char-hex> go run ./cmd/secret-cli \
  --secret-id bmc-x0c0s1b0 \
  --username root \
  --password changeme \
  --store-path secrets.json
```

This stores encrypted credentials under `secret-id` in `secrets.json`.

## Run Middleware

```bash
MASTER_KEY=<64-char-hex> \
FRU_MIDDLE_DRY_RUN=true \
go run ./cmd/server
```

Set `FRU_MIDDLE_DRY_RUN=false` to enable SMD writes.

## End-to-End Test (Exact Commands Used)

This section records the exact commands used in a successful FRU -> middleware -> SMD run on 2026-07-23.

### 1. Start Dependencies

PostgreSQL container (SMD backend):

```bash
docker run -d --name cray-smd-postgres \
  -e POSTGRES_DB=hmsds \
  -e POSTGRES_USER=hmsdsuser \
  -e POSTGRES_PASSWORD=hmsdsuser \
  -p 5432:5432 postgres:10.8
```

Build SMD binaries locally:

```bash
pushd /Users/benmcdonald/smd >/dev/null && go build -o /tmp/smd-local ./cmd/smd && go build -o /tmp/smd-init-local ./cmd/smd-init && popd >/dev/null
```

Initialize SMD schema:

```bash
SMD_DBPASS=hmsdsuser /tmp/smd-init-local \
  -dbhost 127.0.0.1 \
  -dbport 5432 \
  -dbname hmsds \
  -dbuser hmsdsuser \
  -dbopts 'sslmode=disable' \
  -migrationsdir /Users/benmcdonald/smd/migrations
```

Start SMD server:

```bash
SMD_DBPASS=hmsdsuser SMD_DBTYPE=postgres /tmp/smd-local \
  -dbtype postgres \
  -dbhost 127.0.0.1 \
  -dbport 5432 \
  -dbname hmsds \
  -dbuser hmsdsuser \
  -dbopts 'sslmode=disable' \
  -http-listen :27779 \
  -openchami \
  -log 2
```

Start FRU-tracker server:

```bash
cd /Users/benmcdonald/fru-tracker && env \
  'FRU-TRACKER_PORT=8080' \
  'FRU-TRACKER_DATABASE_URL=file:/tmp/fru-tracker-e2e.db?cache=shared&_fk=1' \
  go run ./cmd/server serve --port 8080 --database-url 'file:/tmp/fru-tracker-e2e.db?cache=shared&_fk=1'
```

Start local Redfish mock used by middleware discovery:

```bash
cat > /tmp/redfish-mock.go <<'EOF'
package main

import (
  "encoding/json"
  "log"
  "net/http"
)

func writeJSON(w http.ResponseWriter, v any) {
  w.Header().Set("Content-Type", "application/json")
  _ = json.NewEncoder(w).Encode(v)
}

func main() {
  mux := http.NewServeMux()
  mux.HandleFunc("/redfish/v1", func(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, map[string]any{
      "@odata.id": "/redfish/v1",
      "Systems": map[string]any{"@odata.id": "/redfish/v1/Systems"},
      "Managers": map[string]any{"@odata.id": "/redfish/v1/Managers"},
    })
  })
  mux.HandleFunc("/redfish/v1/Systems", func(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, map[string]any{
      "Members": []any{map[string]any{"@odata.id": "/redfish/v1/Systems/System-1"}},
      "Members@odata.count": 1,
    })
  })
  mux.HandleFunc("/redfish/v1/Managers", func(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, map[string]any{
      "Members": []any{map[string]any{"@odata.id": "/redfish/v1/Managers/BMC-1"}},
      "Members@odata.count": 1,
    })
  })

  log.Printf("redfish mock listening on :18081")
  log.Fatal(http.ListenAndServe(":18081", mux))
}
EOF

go run /tmp/redfish-mock.go
```

### 2. Seed Middleware Secret Store

```bash
cd /Users/benmcdonald/smd-fru-middle && rm -f data/checkpoint.json && \
MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
go run ./cmd/secret-cli \
  --secret-id bmc-x3000c0s17b0 \
  --username root \
  --password changeme \
  --store-path secrets.json
```

### 3. Post to FRU-tracker

Payload file:

```bash
cat > /tmp/fru-upload-e2e.json <<'EOF'
{
  "apiVersion": "example.fabrica.dev/v1",
  "kind": "DiscoverySnapshot",
  "metadata": {
    "name": "middleware-e2e-01"
  },
  "spec": {
    "rawData": [
      {
        "deviceType": "Node",
        "manufacturer": "HPE",
        "partNumber": "NODE-PART",
        "serialNumber": "NODE12345",
        "properties": {
          "xname": "x3000c0s17b0",
          "secret_id": "bmc-x3000c0s17b0",
          "redfish_address": "http://localhost:18081",
          "redfish_uri": "/redfish/v1/Systems/System-1"
        }
      }
    ]
  }
}
EOF
```

Post command:

```bash
curl -sS -i -X POST http://localhost:8080/discoverysnapshots -H 'Content-Type: application/json' -d @/tmp/fru-upload-e2e.json
```

### 4. Start Middleware (Write Mode)

```bash
MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
FRU_MIDDLE_FRU_BASE_URL=http://localhost:8080 \
FRU_MIDDLE_SMD_BASE_URL=http://localhost:27779 \
FRU_MIDDLE_SECRETS_FILE=/Users/benmcdonald/smd-fru-middle/secrets.json \
FRU_MIDDLE_POLL_INTERVAL=2s \
FRU_MIDDLE_DRY_RUN=false \
go run ./cmd/server
```

Observed middleware log line on first cycle:

```text
2026/07/23 15:01:00 cycle complete: total=1 processed=1 skipped=0 failed=0
```

### 5. Query SMD Results

RedfishEndpoints query command:

```bash
curl -sS http://localhost:27779/hsm/v2/Inventory/RedfishEndpoints
```

Actual response:

```json
{"RedfishEndpoints":[{"ID":"x3000c0s17b0","Type":"NodeBMC","Hostname":"localhost","Domain":"","FQDN":"localhost","Enabled":true,"User":"root","Password":"changeme","RediscoverOnUpdate":false,"DiscoveryInfo":{"LastDiscoveryStatus":"NotYetQueried"}}]}
```

ComponentEndpoints query command:

```bash
curl -sS http://localhost:27779/hsm/v2/Inventory/ComponentEndpoints
```

Actual response:

```json
{"ComponentEndpoints":[{"ID":"x3000c0s17b0","Type":"NodeBMC","RedfishType":"Manager","RedfishSubtype":"","OdataID":"/redfish/v1/Managers/BMC-1","RedfishEndpointID":"x3000c0s17b0","Enabled":true,"RedfishEndpointFQDN":"localhost","RedfishURL":"localhost/redfish/v1/Managers/BMC-1","ComponentEndpointType":"ComponentEndpointManager","RedfishManagerInfo":{"Actions":{"#Manager.Reset":{"ResetType@Redfish.AllowableValues":null,"@Redfish.ActionInfo":"/redfish/v1/Managers/BMC-1/ResetActionInfo","target":"/redfish/v1/Managers/BMC-1/Actions/Manager.Reset"}},"CommandShell":{"ServiceEnabled":true,"MaxConcurrentSessions":65536,"ConnectTypesSupported":[]}}},{"ID":"x3000c0s17b0n0","Type":"Node","RedfishType":"ComputerSystem","RedfishSubtype":"","OdataID":"/redfish/v1/Systems/System-1","RedfishEndpointID":"x3000c0s17b0","Enabled":true,"RedfishEndpointFQDN":"localhost","RedfishURL":"localhost/redfish/v1/Systems/System-1","ComponentEndpointType":"ComponentEndpointComputerSystem","RedfishSystemInfo":{"Actions":{"#ComputerSystem.Reset":{"ResetType@Redfish.AllowableValues":null,"@Redfish.ActionInfo":"/redfish/v1/Systems/System-1/ResetActionInfo","target":"/redfish/v1/Systems/System-1/Actions/ComputerSystem.Reset"}}}}]}
```

## Notes

- V1 intentionally skips FRU records missing `xname`, `secret_id`, or reachable Redfish data.
- HPE xname derivation is intentionally deferred to a future phase.
- FRU event subscription is not used in V1 because current FRU-tracker eventing is in-process.
