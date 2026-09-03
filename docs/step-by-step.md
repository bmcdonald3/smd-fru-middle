**Step 1 — Store credentials for your real machine**

```bash
MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
go run ./cmd/secret-cli \
  --secret-id bmc-<your-xname> \
  --username <bmc-username> \
  --password <bmc-password> \
  --store-path secrets.json
```

Replace `<your-xname>`, `<bmc-username>`, `<bmc-password>` with your actual values. This encrypts the credentials into secrets.json in the repo root.

**Step 2 — Start SMD and FRU-tracker locally**

The e2e script can do this for you — but you need to stop it at the point just before starting the middleware. The cleanest way is to run the e2e script's setup steps manually:

```bash
# Terminal 1 — start Postgres + SMD + FRU-tracker (reuse the script's logic)
./scripts/e2e-test.sh
# Let it run until it prints "==> Starting middleware" then Ctrl-C
```

Or just run e2e-test.sh in full with the mock first to confirm infra is healthy, then re-run infrastructure manually and point at your real BMC. Up to you.

**Step 3 — Post a DiscoverySnapshot with your real BMC's details**

```bash
curl -X POST http://localhost:8080/discoverysnapshots \
  -H 'Content-Type: application/json' \
  -d '{
    "apiVersion": "example.fabrica.dev/v1",
    "kind": "DiscoverySnapshot",
    "metadata": { "name": "real-bmc-01" },
    "spec": {
      "rawData": [{
        "deviceType": "Node",
        "manufacturer": "HPE",
        "partNumber": "",
        "serialNumber": "",
        "properties": {
          "xname":            "<your-xname>",
          "secret_id":        "bmc-<your-xname>",
          "redfish_address":  "https://<bmc-ip-or-hostname>"
        }
      }]
    }
  }'
```

**Step 4 — Start the magellan BMC daemon**

The middleware no longer talks to BMCs; magellan does. Point it at the same
`secrets.json` so it can resolve the `secret_id` the middleware sends.

```bash
MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
magellan serve \
  --host 127.0.0.1 \
  --port 8443 \
  --secrets-file secrets.json \
  --insecure
```

Drop `--insecure` once your BMC presents a trusted certificate.

**Step 5 — Run the middleware pointing at magellan**

```bash
MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
FRU_MIDDLE_FRU_BASE_URL=http://localhost:8080 \
FRU_MIDDLE_SMD_BASE_URL=http://localhost:27779 \
FRU_MIDDLE_MAGELLAN_BASE_URL=http://127.0.0.1:8443 \
FRU_MIDDLE_SECRETS_FILE=secrets.json \
FRU_MIDDLE_POLL_INTERVAL=5s \
FRU_MIDDLE_DRY_RUN=true \
go run ./cmd/server
```

Start with `DRY_RUN=true` — it will log exactly what it *would* write to SMD without touching anything. Once you confirm the inventory data looks right in the logs, flip to `DRY_RUN=false`.

**Step 6 — Check what landed in SMD**

```bash
curl -s http://localhost:27779/hsm/v2/Inventory/RedfishEndpoints | jq .
curl -s http://localhost:27779/hsm/v2/Inventory/ComponentEndpoints | jq .
```

---

The two things most likely to need tweaking with a real BMC:
1. **TLS** — if the BMC uses a self-signed cert, run the magellan daemon with `--insecure`.
2. **Redfish shape differences** — real BMCs sometimes use slightly different field names or structures than the mock. The magellan daemon's logs show exactly what came back from the BMC if something goes wrong.

Created 4 todos