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

## Notes

- V1 intentionally skips FRU records missing `xname`, `secret_id`, or reachable Redfish data.
- HPE xname derivation is intentionally deferred to a future phase.
- FRU event subscription is not used in V1 because current FRU-tracker eventing is in-process.
