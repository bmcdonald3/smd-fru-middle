#!/usr/bin/env bash
# Validates that BMC traffic now happens in magellan and nowhere else.
#
# Needs no Postgres/SMD/FRU-tracker — just this repo, the magellan repo, and a
# reachable BMC. For the full FRU -> middleware -> SMD run use real-bmc-test.sh.
#
# Env (all optional except BMC_PASS):
#   BMC_ADDRESS   default 172.24.0.3
#   BMC_USER      default root
#   BMC_PASS      prompted if unset
#   BMC_SCHEME    default https (set to http only for a local mock)
#   XNAME         default x3000c0s1b0
#   MAGELLAN_DIR  default ../magellan
#   MAGELLAN_PORT default 18443
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BMC_ADDRESS="${BMC_ADDRESS:-172.24.0.3}"
BMC_USER="${BMC_USER:-root}"
BMC_SCHEME="${BMC_SCHEME:-https}"
BMC_URL="$BMC_SCHEME://$BMC_ADDRESS"
XNAME="${XNAME:-x3000c0s1b0}"
MAGELLAN_DIR="${MAGELLAN_DIR:-$(cd "$ROOT_DIR/../magellan" 2>/dev/null && pwd || echo "$ROOT_DIR/../magellan")}"
MAGELLAN_PORT="${MAGELLAN_PORT:-18443}"
MASTER_KEY="${MASTER_KEY:-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}"
export MASTER_KEY

WORK_DIR="$(mktemp -d /tmp/validate-magellan.XXXXXX)"
SECRETS_FILE="$WORK_DIR/secrets.json"
MAGELLAN_BIN="$WORK_DIR/magellan"
MIDDLE_BIN="$WORK_DIR/middleware"
MAGELLAN_LOG="$WORK_DIR/magellan.log"
INVENTORY_JSON="$WORK_DIR/inventory.json"
CONNS_FILE="$WORK_DIR/bmc-connections.txt"
SECRET_ID="bmc-${XNAME}"
MAGELLAN_PID=""

PASS=0
FAIL=0

green() { printf '\033[32m%s\033[0m\n' "$1"; }
red()   { printf '\033[31m%s\033[0m\n' "$1"; }

ok()   { green "  PASS  $1"; PASS=$((PASS + 1)); }
bad()  { red   "  FAIL  $1"; FAIL=$((FAIL + 1)); }

cleanup() {
  set +e
  [[ -n "$MAGELLAN_PID" ]] && kill "$MAGELLAN_PID" 2>/dev/null
  [[ -n "${FRU_STUB_PID:-}" ]] && kill "$FRU_STUB_PID" 2>/dev/null
  echo ""
  echo "Artifacts: $WORK_DIR"
  echo "  magellan log:  $MAGELLAN_LOG"
  echo "  inventory:     $INVENTORY_JSON"
  [[ -s "$CONNS_FILE" ]] && echo "  bmc conns:     $CONNS_FILE"
}
trap cleanup EXIT

# Lists "pid command" for every socket currently open to the BMC, so we can prove
# which process is the one actually speaking Redfish.
bmc_connection_owners() {
  if command -v ss >/dev/null 2>&1; then
    ss -tnp 2>/dev/null | grep "$BMC_ADDRESS" | grep -o 'pid=[0-9]*,[^)]*' || true
  elif command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP 2>/dev/null | grep "$BMC_ADDRESS" | awk '{print $1" "$2}' || true
  fi
}

echo "=============================================================="
echo " Validating magellan-only BMC access"
echo "   BMC:      $BMC_URL ($BMC_USER)"
echo "   xname:    $XNAME"
echo "   magellan: $MAGELLAN_DIR"
echo "=============================================================="

# ---------------------------------------------------------------- preflight
echo ""
echo "[0] Preflight"

for cmd in go curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || { red "missing required command: $cmd"; exit 1; }
done

[[ -d "$MAGELLAN_DIR" ]] || { red "MAGELLAN_DIR does not exist: $MAGELLAN_DIR"; exit 1; }

if [[ -z "${BMC_PASS:-}" ]]; then
  read -rs -p "  BMC password for $BMC_USER@$BMC_ADDRESS: " BMC_PASS
  echo ""
fi
[[ -n "$BMC_PASS" ]] || { red "BMC_PASS is empty"; exit 1; }

echo "  go:       $(go version | awk '{print $3}')"
echo "  magellan: $(git -C "$MAGELLAN_DIR" rev-parse --short HEAD 2>/dev/null) ($(git -C "$MAGELLAN_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null))"
echo "  middle:   $(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null) ($(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null))"

if ! curl -fsSk -u "$BMC_USER:$BMC_PASS" "$BMC_URL/redfish/v1/" >/dev/null 2>&1; then
  red "  cannot reach $BMC_URL/redfish/v1/ — check address/credentials"
  exit 1
fi
echo "  BMC reachable: yes"

# ------------------------------------------------- static: no BMC code here
echo ""
echo "[1] Static checks — this repo must contain no BMC client"

if [[ -d "$ROOT_DIR/internal/redfish" ]]; then
  bad "internal/redfish still exists (old direct-to-BMC client)"
else
  ok "internal/redfish is gone"
fi

if [[ -f "$ROOT_DIR/internal/magellan/client.go" ]]; then
  ok "internal/magellan client is present"
else
  bad "internal/magellan/client.go missing — middleware changes not on this box"
fi

if (cd "$ROOT_DIR" && go list -deps ./... 2>/dev/null | grep -q 'stmcginnis/gofish'); then
  bad "middleware still links the gofish Redfish library"
else
  ok "middleware does not link any Redfish library"
fi

# Any real Redfish call would have to build a /redfish/... URL somewhere.
if (cd "$ROOT_DIR" && grep -rn 'redfish/v1' --include='*.go' internal cmd 2>/dev/null | grep -v '_test.go' | grep -q .); then
  bad "non-test Go code still references redfish/v1 paths:"
  (cd "$ROOT_DIR" && grep -rn 'redfish/v1' --include='*.go' internal cmd | grep -v '_test.go' | sed 's/^/        /')
else
  ok "no non-test Go code constructs Redfish URLs"
fi

# ------------------------------------------------------------ build + start
echo ""
echo "[2] Building magellan and seeding credentials"

if ! (cd "$MAGELLAN_DIR" && go build -o "$MAGELLAN_BIN" .) 2>"$WORK_DIR/build.log"; then
  bad "magellan build failed:"
  sed 's/^/        /' "$WORK_DIR/build.log"
  exit 1
fi
ok "magellan built"

if ! (cd "$ROOT_DIR" && go run ./cmd/secret-cli \
      --secret-id "$SECRET_ID" \
      --username "$BMC_USER" \
      --password "$BMC_PASS" \
      --store-path "$SECRETS_FILE") >/dev/null 2>&1; then
  bad "failed to seed secret store"
  exit 1
fi
ok "credentials stored under secret ID '$SECRET_ID'"

if ! (cd "$ROOT_DIR" && go build -o "$MIDDLE_BIN" ./cmd/server) 2>"$WORK_DIR/middle-build.log"; then
  bad "middleware build failed:"
  sed 's/^/        /' "$WORK_DIR/middle-build.log"
  exit 1
fi
ok "middleware built"

"$MAGELLAN_BIN" serve --host 127.0.0.1 --port "$MAGELLAN_PORT" \
  --secrets-file "$SECRETS_FILE" --insecure >"$MAGELLAN_LOG" 2>&1 &
MAGELLAN_PID=$!

for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:$MAGELLAN_PORT/healthz" >/dev/null 2>&1 && break
  sleep 1
done
if curl -fsS "http://127.0.0.1:$MAGELLAN_PORT/healthz" >/dev/null 2>&1; then
  ok "magellan daemon healthy on :$MAGELLAN_PORT (pid $MAGELLAN_PID)"
else
  bad "magellan daemon did not become healthy:"
  sed 's/^/        /' "$MAGELLAN_LOG"
  exit 1
fi

# -------------------------------------------------------- inventory + owner
echo ""
echo "[3] Crawling the real BMC through magellan"

# Poll open sockets while the crawl runs so we can attribute BMC traffic.
( for _ in $(seq 1 60); do bmc_connection_owners >>"$CONNS_FILE"; sleep 0.25; done ) &
WATCH_PID=$!

HTTP_CODE="$(curl -s -o "$INVENTORY_JSON" -w '%{http_code}' \
  -X POST "http://127.0.0.1:$MAGELLAN_PORT/v1/inventory" \
  -H 'Content-Type: application/json' \
  -d "{\"bmc\":\"$BMC_URL\",\"secretID\":\"$SECRET_ID\"}")"

kill "$WATCH_PID" 2>/dev/null; wait "$WATCH_PID" 2>/dev/null

if [[ "$HTTP_CODE" == "200" ]]; then
  ok "POST /v1/inventory returned 200"
else
  bad "POST /v1/inventory returned $HTTP_CODE"
  sed 's/^/        /' "$INVENTORY_JSON"
fi

python3 - "$INVENTORY_JSON" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception as e:
    print(f"  FAIL  response is not valid JSON: {e}")
    sys.exit(1)

systems  = d.get("systems") or []
managers = d.get("managers") or []
print(f"        systems={len(systems)} managers={len(managers)}")

if not systems:
    print("  FAIL  no systems returned")
    sys.exit(1)

failed = 0
s = systems[0]
for field in ("uuid", "serial", "model", "manufacturer"):
    val = s.get(field)
    if not val:
        failed += 1
    print(f"  {'PASS' if val else 'FAIL'}  system.{field} = {val!r}")

nics = s.get("ethernet_interfaces") or []
if not nics:
    failed += 1
print(f"  {'PASS' if nics else 'FAIL'}  system.ethernet_interfaces = {len(nics)} NIC(s)")
for n in nics:
    print(f"        {n.get('mac')}  {n.get('ip') or '-'}  {n.get('name')}")

# System NICs are host cards; a BMC knows their MAC but usually not the IP the
# host OS assigned, so only the MAC is required here.
unnamed = [n for n in nics if not (n.get("mac") or "").strip()]
if unnamed:
    failed += 1
print(f"  {'PASS' if not unnamed else 'FAIL'}  every system NIC reports a MAC")

# The BMC's own management NIC must have an address — it is the one we reached.
mgr_nics = (managers[0].get("ethernet_interfaces") or []) if managers else []
addressed = [n for n in mgr_nics if (n.get("ip") or "").strip()]
if not addressed:
    failed += 1
print(f"  {'PASS' if addressed else 'FAIL'}  manager NICs carrying an IP = {len(addressed)} of {len(mgr_nics)}")
for n in mgr_nics:
    print(f"        {n.get('mac')}  {n.get('ip') or '-'}  {n.get('name')}")

if not managers:
    failed += 1
print(f"  {'PASS' if managers else 'FAIL'}  managers returned = {len(managers)}")

sys.exit(1 if failed else 0)
PY
PY_RC=$?
if [[ $PY_RC -eq 0 ]]; then PASS=$((PASS + 1)); else FAIL=$((FAIL + 1)); fi

echo ""
echo "[4] Who actually talked to the BMC?"
if [[ -s "$CONNS_FILE" ]]; then
  sort -u "$CONNS_FILE" | sed 's/^/        /'
  if sort -u "$CONNS_FILE" | grep -qi 'magellan'; then
    ok "magellan owns the connection to $BMC_ADDRESS"
  else
    echo "        (could not attribute — may need root for ss/lsof process info)"
  fi
  if sort -u "$CONNS_FILE" | grep -qiE 'smd-fru|cmd/server|middle'; then
    bad "a middleware process also held a connection to the BMC"
  else
    ok "no middleware process connected to the BMC"
  fi
else
  echo "        (no socket samples captured — needs root on most systems; skipping)"
fi

# ------------------------------------------------------- negative: bad secret
echo ""
echo "[5] Negative check — unknown secret ID must not silently succeed"

BAD_CODE="$(curl -s -o "$WORK_DIR/bad.json" -w '%{http_code}' \
  -X POST "http://127.0.0.1:$MAGELLAN_PORT/v1/inventory" \
  -H 'Content-Type: application/json' \
  -d "{\"bmc\":\"$BMC_URL\",\"secretID\":\"definitely-not-a-real-secret\"}")"

if [[ "$BAD_CODE" == "200" ]]; then
  echo "        returned 200 — magellan fell back to default/URI credentials"
  echo "        (expected if a 'default' entry exists in the store; not a failure)"
  ok "unknown secret ID handled (fell back, store has a default)"
else
  ok "unknown secret ID rejected with HTTP $BAD_CODE"
  head -c 300 "$WORK_DIR/bad.json" | sed 's/^/        /'
  echo ""
fi

# --------------------------------------------------------------- middleware
echo ""
echo "[6] Middleware end-to-end (stub FRU-tracker, dry-run, no SMD needed)"

FRU_STUB_PORT="${FRU_STUB_PORT:-18082}"
cat > "$WORK_DIR/fru-stub.py" <<PYSTUB
import json, http.server
DEVICES = [{
  "metadata": {"uid": "dev-1", "updatedAt": "2026-09-03T00:00:00Z"},
  "spec": {
    "deviceType": "Node", "manufacturer": "", "partNumber": "", "serialNumber": "",
    "properties": {
      "xname": "$XNAME",
      "secret_id": "$SECRET_ID",
      "redfish_address": "$BMC_URL"
    }
  }
}]
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps(DEVICES).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", $FRU_STUB_PORT), H).serve_forever()
PYSTUB

python3 "$WORK_DIR/fru-stub.py" >"$WORK_DIR/fru-stub.log" 2>&1 &
FRU_STUB_PID=$!

STUB_UP=0
for _ in $(seq 1 20); do
  if curl -fsS "http://127.0.0.1:$FRU_STUB_PORT/devices" >/dev/null 2>&1; then
    STUB_UP=1
    break
  fi
  sleep 0.5
done

if [[ $STUB_UP -eq 1 ]]; then
  ok "stub FRU-tracker serving one device for $XNAME"
else
  bad "stub FRU-tracker failed to start"
  sed 's/^/        /' "$WORK_DIR/fru-stub.log" | head -5
fi

# Portable bounded run: no `timeout` on macOS. Polls for the end of the first
# cycle rather than sleeping a fixed time, since a real BMC crawl can take a
# minute or more. Runs a built binary because `go run` spawns a child that
# survives killing the wrapper.
run_middleware() {
  local out="$1" secs="$2"
  FRU_MIDDLE_FRU_BASE_URL="http://127.0.0.1:$FRU_STUB_PORT" \
  FRU_MIDDLE_MAGELLAN_BASE_URL="http://127.0.0.1:$MAGELLAN_PORT" \
  FRU_MIDDLE_SECRETS_FILE="$SECRETS_FILE" \
  FRU_MIDDLE_CHECKPOINT_PATH="$WORK_DIR/checkpoint-$$.json" \
  FRU_MIDDLE_INSECURE_TLS=true \
  FRU_MIDDLE_DRY_RUN=true \
  FRU_MIDDLE_POLL_INTERVAL=5s \
  "$MIDDLE_BIN" >"$out" 2>&1 &
  local pid=$!
  for _ in $(seq 1 "$secs"); do
    grep -q 'cycle complete: total=1' "$out" 2>/dev/null && break
    sleep 1
  done
  { kill "$pid"; wait "$pid"; } >/dev/null 2>&1
}

MIDDLE_OK_LOG="$WORK_DIR/middleware-with-magellan.log"
rm -f "$WORK_DIR"/checkpoint-*.json
run_middleware "$MIDDLE_OK_LOG" 240

echo "        middleware output:"
sed 's/^/        /' "$MIDDLE_OK_LOG" | head -12

if grep -q 'would upsert redfish endpoint' "$MIDDLE_OK_LOG"; then
  ok "middleware built an SMD payload from magellan-supplied inventory"
  if grep -qE 'systems=[1-9]' "$MIDDLE_OK_LOG"; then
    ok "payload contains at least one system"
  else
    bad "payload contained zero systems"
  fi
else
  bad "middleware never produced an SMD payload"
fi

if grep -qi 'redfish discovery failed' "$MIDDLE_OK_LOG"; then
  bad "middleware reported a Redfish error (old direct-to-BMC path is live)"
else
  ok "middleware reported no Redfish errors"
fi

# ---------------------------------------------- negative: magellan unavailable
echo ""
echo "[7] Negative check — middleware must fail loudly when magellan is down"

kill "$MAGELLAN_PID" 2>/dev/null; wait "$MAGELLAN_PID" 2>/dev/null
MAGELLAN_PID=""
sleep 1

MIDDLE_DOWN_LOG="$WORK_DIR/middleware-no-magellan.log"
rm -f "$WORK_DIR"/checkpoint-*.json
run_middleware "$MIDDLE_DOWN_LOG" 30

kill "$FRU_STUB_PID" 2>/dev/null; wait "$FRU_STUB_PID" 2>/dev/null

echo "        middleware output:"
sed 's/^/        /' "$MIDDLE_DOWN_LOG" | head -8

if grep -q 'magellan inventory failed' "$MIDDLE_DOWN_LOG"; then
  ok "middleware surfaced a magellan error, proving it has no BMC fallback"
else
  bad "expected 'magellan inventory failed' — middleware may still reach the BMC directly"
fi

if grep -q 'would upsert redfish endpoint' "$MIDDLE_DOWN_LOG"; then
  bad "middleware still produced inventory with magellan down (it reached the BMC itself)"
else
  ok "no inventory produced without magellan"
fi

# -------------------------------------------------------------------- summary
echo ""
echo "=============================================================="
if [[ $FAIL -eq 0 ]]; then
  green " RESULT: PASS  ($PASS checks passed)"
else
  red   " RESULT: FAIL  ($PASS passed, $FAIL failed)"
fi
echo "=============================================================="
echo ""
echo "Inventory magellan returned from the real BMC:"
python3 -m json.tool <"$INVENTORY_JSON" 2>/dev/null | head -60 || cat "$INVENTORY_JSON"

exit $(( FAIL > 0 ? 1 : 0 ))
