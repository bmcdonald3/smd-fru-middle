# Magellan BMC Layer Migration — 2026-09-03

Status: **component-validated; end-to-end SMD validation still outstanding.**
A `real-bmc-test.sh` run on 2026-09-03 reported PASS but was later found invalid
(see "Invalid Run" below).

## What Was Changed

The middleware no longer speaks Redfish. All BMC interaction now happens inside
magellan (RFD #133), which the middleware calls over REST.

Before:

```
FRU-tracker -> middleware --(Redfish/Basic Auth)--> BMC
                          --(HTTP)--> SMD
```

After:

```
FRU-tracker -> middleware --(POST /v1/inventory)--> magellan --(Redfish)--> BMC
                          --(HTTP)--> SMD
```

`internal/redfish/client.go` (367 lines) was deleted. The middleware links no
Redfish library and constructs no Redfish URLs.

### Environment

| Component       | Details                                     |
|-----------------|---------------------------------------------|
| BMC address     | `172.24.0.3`                                |
| BMC credential  | `root` / `initial0`                         |
| xname assigned  | `x3000c0s1b0`                               |
| System model    | Intel S2600BPB                              |
| System UUID     | `317091ec-8be6-11e8-ab21-a4bf013f6b40`      |
| System serial   | `QSBP82909087`                              |
| BMC firmware    | `2.86.2da97d3f`                             |
| Host machine    | `tamarindo`                                 |
| Go toolchain    | `go1.26.5`                                  |
| magellan commit | `afb595f` (branch `fru-test`)               |
| middleware commit | `a7d6cfc` (branch `main`)                 |

---

## Changes to Magellan

Branch `fru-test`, on top of the existing RFD #133 daemon work (`30bf99d`).

### 1. Per-request credential selection — `5dea412`

`POST /v1/inventory` resolved BMC credentials by BMC URI only. This middleware
keys credentials by FRU `secret_id` (e.g. `bmc-x3000c0s1b0`), so the daemon had
no way to select them. `cmd/serve.go` noted this as "a later enhancement".

| File | Change |
|------|--------|
| `pkg/bmc/conn.go` | Added `ConnConfig.SecretID` and `credentialID()`; `GetUserPass` resolves by secret ID when set, else BMC URI, else store default |
| `pkg/bmc/manager.go` | Session cache keyed by URI **and** credential identity, so two callers using different credentials for one BMC never share a session |
| `pkg/service/service.go` | Added `connConfigFor()` and `InventoryWithSecret()`; `Inventory()` retained as a wrapper |
| `internal/server/handlers.go` | `POST /v1/inventory` accepts optional `secretID` |
| `pkg/bmc/conn_test.go` | Covers secret-ID resolution |

Credentials are never sent over the wire — only the secret ID is. Magellan
resolves it from the shared encrypted secret store.

### 2. NIC address handling — `afb595f` (supersedes `e9b4319`)

Two defects found while validating against the S2600BPB:

- `walkSystems` read `IPv4Addresses[0].Address` directly, so an interface whose
  first entry is a placeholder reported that placeholder, and an IPv6-only
  interface reported nothing.
- `walkManagers` filtered on `len(IPv4Addresses) <= 0`, dropping IPv6-only
  interfaces entirely.

Added `firstIPAddress`, which returns the first address that parses and is not
unspecified (`0.0.0.0`, `::`), preferring IPv4. Used by both walkers.

Observed effect on this BMC: manager NICs 2 and 3 report `0.0.0.0` and are now
excluded rather than published as real addresses.

```
before:  manager NICs carrying an IP = 3 of 3   (172.24.0.3, 0.0.0.0, 0.0.0.0)
after:   manager NICs carrying an IP = 1 of 1   (172.24.0.3)
```

---

## Changes to smd-fru-middle

Six commits, `55f8ff2..a7d6cfc`.

| File | Change |
|------|--------|
| `internal/redfish/client.go` | **Deleted** (367 lines) |
| `internal/magellan/client.go` | New. Calls `POST /v1/inventory` with BMC address + secret ID; optional bearer auth |
| `internal/magellan/client_test.go` | New. Request shape, URI normalization, error propagation, NIC filtering |
| `internal/pipeline/service.go` | `redfish.Discover(...)` → `magellan.Inventory(...)` |
| `internal/config/config.go` | `FRU_MIDDLE_MAGELLAN_BASE_URL`, `FRU_MIDDLE_MAGELLAN_AUTH_TOKEN`, `FRU_MIDDLE_MAGELLAN_TIMEOUT` |
| `internal/models/models.go` | Removed `ID`/`Type` fields only the deleted client populated |
| `scripts/validate-magellan.sh` | New. 17-check component validation, no SMD/Postgres required |
| `scripts/e2e-test.sh`, `scripts/real-bmc-test.sh` | Start a magellan daemon; e2e Redfish mock made gofish-compatible |

### Behavioural notes

**URI normalization.** Magellan returns absolute URIs
(`https://172.24.0.3/redfish/v1/Systems/...`). SMD keys ComponentEndpoints off
the Redfish-relative `@odata.id`, so the client reduces them back to a path.

**NIC filtering.** Magellan reports every NIC a BMC advertises. The middleware
drops address-less NICs before building the SMD payload, matching the deleted
client's behaviour.

**Timeout semantics changed.** `FRU_MIDDLE_HTTP_TIMEOUT` (20s) previously bounded
each individual Redfish request. One magellan call now covers an entire crawl, so
`FRU_MIDDLE_MAGELLAN_TIMEOUT` (default 180s) governs it — above magellan's own
120s request timeout, so magellan's error surfaces rather than a client timeout.

**Manager payload is richer.** The deleted client sent managers as `{ID, Type,
URI}`, and `ID`/`Type` were `json:"-"`, so SMD received only `{"uri": "..."}`.
Magellan returns UUID, name, model, firmware version, serial console types, and
manager NICs. This is new data reaching SMD.

**New operational dependency.** The middleware now requires a reachable magellan
daemon sharing the same secret store and `MASTER_KEY`. Credentials are resolved
twice — by the middleware for the SMD payload, by magellan for the BMC. If the
two stores drift, SMD registration succeeds while magellan cannot read the BMC.

---

## Notable Conditions

- The BMC uses a self-signed certificate; the magellan daemon needs `--insecure`.
- **System NICs on this BMC carry no IP address.** The Redfish resource has no
  `IPv4Addresses` or `IPv6Addresses` field at all — a BMC knows the host NIC's
  MAC but not the IP the host OS assigned. Addresses live on the manager NIC.
  All five system NICs are therefore filtered out before SMD, as they were by
  the deleted client.
- SMD performs its own Redfish discovery after endpoint registration. The
  `EthernetNICInfo` in the 2026-08-12 record came from SMD's crawl, not the
  middleware payload. **SMD state is therefore not evidence that any particular
  run performed work** — see "Invalid Run" below.

---

## How to Reproduce

Component validation (no Postgres/SMD/FRU-tracker required):

```bash
BMC_ADDRESS=172.24.0.3 \
BMC_USER=root \
XNAME=x3000c0s1b0 \
MAGELLAN_DIR=/root/mcdonald/magellan \
./scripts/validate-magellan.sh
```

Full pipeline:

```bash
BMC_ADDRESS=172.24.0.3 BMC_USER=root BMC_PASS=<pass> XNAME=x3000c0s1b0 \
SMD_DIR=/root/mcdonald/smd \
FRU_DIR=/root/mcdonald/fru-tracker \
MAGELLAN_DIR=/root/mcdonald/magellan \
./scripts/real-bmc-test.sh
```

---

## Actual Output — `validate-magellan.sh`, 17/17 PASS

```
[1] Static checks — this repo must contain no BMC client
  PASS  internal/redfish is gone
  PASS  internal/magellan client is present
  PASS  middleware does not link any Redfish library
  PASS  no non-test Go code constructs Redfish URLs

[3] Crawling the real BMC through magellan
  PASS  POST /v1/inventory returned 200
        systems=1 managers=1
  PASS  system.uuid = '317091ec-8be6-11e8-ab21-a4bf013f6b40'
  PASS  system.serial = 'QSBP82909087'
  PASS  system.model = 'S2600BPB'
  PASS  system.manufacturer = 'Intel Corporation'
  PASS  system.ethernet_interfaces = 5 NIC(s)
  PASS  every system NIC reports a MAC
  PASS  manager NICs carrying an IP = 1 of 1
        a4-bf-01-3f-6b-42  172.24.0.3  BMC Ethernet Interface

[4] Who actually talked to the BMC?
  PASS  no middleware process connected to the BMC

[5] Negative check — unknown secret ID must not silently succeed
  PASS  unknown secret ID rejected with HTTP 502
        {"error":"https://172.24.0.3: credentials blank for BMC"}

[6] Middleware end-to-end (stub FRU-tracker, dry-run, no SMD needed)
  PASS  middleware built an SMD payload from magellan-supplied inventory
        dry-run: would upsert redfish endpoint id=x3000c0s1b0 systems=1 managers=1

[7] Negative check — middleware must fail loudly when magellan is down
  PASS  middleware surfaced a magellan error, proving it has no BMC fallback
  PASS  no inventory produced without magellan
```

Stage [7] is the load-bearing check: with magellan stopped the middleware
produces nothing, confirming no direct-to-BMC fallback path remains.

---

## Invalid Run — 2026-09-03 16:23, `real-bmc-test.sh`

A run reported `PASS: Real-BMC collection succeeded` with SMD output matching the
2026-08-12 baseline byte for byte. **That result was spurious.** The logs show:

```
16:23:28  magellan daemon listening
16:23:28  GET /healthz 200                              <- readiness probe
16:23:55  shutting down magellan daemon                 <- killed mid-run
16:23:59  middleware: POST /v1/inventory -> EOF         <- daemon already gone
16:24:04+ FRU-tracker unreachable
```

Magellan served exactly one healthz request and **zero** `/v1/inventory` calls.
The middleware processed no candidates. The `x3000c0s1b0` records the script
asserted on were pre-existing, not produced by that run.

Cause: twelve orphaned `go run ./cmd/server` processes had accumulated over the
day, the oldest running 3.4 hours. `go run` compiles and executes a *child*
process, so the harness's `stop_pid` killed the wrapper and left the server
alive. Those orphans kept polling `localhost:8080` and `127.0.0.1:18443`, and a
leftover cleanup handler tore down this run's magellan and FRU-tracker mid-flight.

The SMD assertions could not detect this because SMD rediscovers endpoints on its
own; its state is not evidence that a given run did any work.

### Harness changes made in response

| Change | Purpose |
|--------|---------|
| All scripts build `./cmd/server` and run the binary | `go run`'s child no longer outlives the harness |
| `real-bmc-test.sh` preflight aborts on live `cmd/server` / `magellan` processes | A contaminated box fails loudly instead of passing |
| `real-bmc-test.sh` requires `/v1/inventory` → 200 in *this run's* magellan log | Ties the result to this run rather than to SMD state |
| `real-bmc-test.sh` requires `processed=1` in this run's middleware log | Confirms the middleware, not a leftover, did the upsert |

---

## Still To Validate

1. `RedfishEndpoints` and `ComponentEndpoints` created in SMD **by a verified run**.
2. Whether the richer manager payload (UUID, model, firmware, manager NICs) is
   accepted by SMD or trips `CompEthInterface` validation. This remains the
   largest untested behavioural difference.
3. Whether `ComponentEndpoints.OdataID` stays relative end to end.

---

## Defects Found During Migration

Recorded because each passed a green test suite before real hardware caught it.

| Defect | Symptom | Root cause |
|--------|---------|------------|
| Client timeout | `context deadline exceeded` after 20s | Per-request timeout reused for a whole-crawl call |
| Stale daemon checkout | HTTP 400, `unknown field "secretID"` | Daemon built from a branch without the change; `decodeJSON` uses `DisallowUnknownFields` |
| NIC placeholders | `0.0.0.0` published as a real address | `IPv4Addresses[0]` read without validation |
| Weak assertion | 5 NICs with no IP passed as healthy | Test asserted NIC count, not address presence |
| Orphaned processes | Harness reported PASS having done no work | `go run` child outlived `stop_pid`; SMD state mistaken for proof of work |

The e2e mock BMC answered instantly with clean IPv4 and could not surface any of
these; each required the physical S2600BPB.

---

## What To Contribute Upstream

1. **`bmcdonald3/ipv6-nic-addresses` → `OpenCHAMI/magellan:main`** — the NIC
   address fix (`ecff2fa`), branched from `main`, one file plus a table test.
   Independent of RFD #133 and ready to open. Note for reviewers: `pkg/crawler`
   also backs `magellan collect`, so CLI output changes too.

2. **Credential-selection work (`5dea412`) → the RFD #133 branch, not `main`.**
   It touches `internal/server` and `pkg/service`, which exist only on
   `feature/RFD-133-Explore`. It has to land on top of that work.

3. **smd-fru-middle** — already on `main` of `bmcdonald3/smd-fru-middle`.

`fru-test` also carries `e9b4319`, superseded by `afb595f`. Once the NIC PR
merges to `main`, rebase `fru-test` and both should drop out as already applied.
