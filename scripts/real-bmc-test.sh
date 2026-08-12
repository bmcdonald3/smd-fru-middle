#!/usr/bin/env bash
# Real-BMC integration test.
# Starts SMD + FRU-tracker locally, posts a DiscoverySnapshot for a real BMC,
# runs the middleware, then validates RedfishEndpoints and ComponentEndpoints.
#
# Required env vars (no defaults):
#   BMC_ADDRESS   — IP or hostname of the BMC (e.g. 172.24.0.3)
#   BMC_USER      — Redfish username
#   BMC_PASS      — Redfish password
#   XNAME         — xname to assign this BMC in SMD (e.g. x3000c0s1b0)
#
# Optional overrides:
#   SMD_DIR, FRU_DIR, SMD_PORT, FRU_PORT, PG_PORT, MASTER_KEY
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMD_DIR="${SMD_DIR:-/Users/benmcdonald/smd}"
FRU_DIR="${FRU_DIR:-/Users/benmcdonald/fru-tracker}"

PG_PORT="${PG_PORT:-5432}"
SMD_PORT="${SMD_PORT:-27779}"
FRU_PORT="${FRU_PORT:-8080}"

PG_DB="${PG_DB:-hmsds}"
PG_USER="${PG_USER:-hmsdsuser}"
PG_PASS="${PG_PASS:-hmsdsuser}"

MASTER_KEY="${MASTER_KEY:-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}"

: "${BMC_ADDRESS:?BMC_ADDRESS is required (e.g. 172.24.0.3)}"
: "${BMC_USER:?BMC_USER is required}"
: "${BMC_PASS:?BMC_PASS is required}"
: "${XNAME:?XNAME is required (e.g. x3000c0s1b0)}"

SECRET_ID="bmc-${XNAME}"

WORK_DIR="$(mktemp -d /tmp/smd-fru-middle-real.XXXXXX)"
PG_CONTAINER="smd-fru-middle-real-pg-$$"
SECRETS_FILE="$WORK_DIR/secrets.json"
FRU_DB_URL="file:$WORK_DIR/fru-tracker-real.db?cache=shared&_fk=1"

SMD_BIN="$WORK_DIR/smd-local"
SMD_INIT_BIN="$WORK_DIR/smd-init-local"
FRU_BIN="$WORK_DIR/fru-tracker-local"
PAYLOAD_JSON="$WORK_DIR/fru-upload-real.json"

SMD_PID=""
FRU_PID=""
MIDDLE_PID=""

SMD_LOG="$WORK_DIR/smd.log"
FRU_LOG="$WORK_DIR/fru.log"
MIDDLE_LOG="$WORK_DIR/middle.log"
SMD_INIT_LOG="$WORK_DIR/smd-init.log"

REDFISH_RESP="$WORK_DIR/redfish_endpoints.json"
COMP_RESP="$WORK_DIR/component_endpoints.json"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $cmd" >&2
    exit 1
  fi
}

is_port_in_use() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

port_listener_pids() {
  lsof -t -nP -iTCP:"$1" -sTCP:LISTEN 2>/dev/null | sort -u
}

stop_pid() {
  local pid="$1"
  [[ -z "$pid" ]] && return 0
  kill -0 "$pid" 2>/dev/null || return 0
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 20); do
    kill -0 "$pid" 2>/dev/null || { wait "$pid" 2>/dev/null || true; return 0; }
    sleep 0.2
  done
  kill -9 "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

clear_port_listeners() {
  local pids
  pids="$(port_listener_pids "$1" || true)"
  [[ -z "$pids" ]] && return 0
  echo "INFO: clearing stale listener(s) on port $1: $pids"
  while IFS= read -r pid; do
    [[ -n "$pid" ]] && stop_pid "$pid"
  done <<<"$pids"
}

pick_free_port() {
  for candidate in "$@"; do
    is_port_in_use "$candidate" || { echo "$candidate"; return 0; }
  done
  return 1
}

cleanup() {
  set +e
  [[ -n "$MIDDLE_PID" ]] && stop_pid "$MIDDLE_PID"
  [[ -n "$FRU_PID" ]]    && stop_pid "$FRU_PID"
  [[ -n "$SMD_PID" ]]    && stop_pid "$SMD_PID"
  clear_port_listeners "$SMD_PORT"
  clear_port_listeners "$FRU_PORT"
  docker rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
  echo ""
  echo "Logs and artifacts kept in: $WORK_DIR"
}
trap cleanup EXIT

wait_for_http() {
  local url="$1" max_tries="${2:-60}" sleep_secs="${3:-1}"
  for _ in $(seq 1 "$max_tries"); do
    curl -fsSk "$url" >/dev/null 2>&1 && return 0
    sleep "$sleep_secs"
  done
  echo "ERROR: timed out waiting for $url" >&2
  return 1
}

wait_for_contains() {
  local url="$1" token="$2" out_file="$3" max_tries="${4:-120}" sleep_secs="${5:-1}"
  for _ in $(seq 1 "$max_tries"); do
    local out
    out="$(curl -fsSk "$url" || true)"
    if [[ -n "$out" ]] && grep -Fq "$token" <<<"$out"; then
      printf '%s\n' "$out" > "$out_file"
      return 0
    fi
    sleep "$sleep_secs"
  done
  echo "ERROR: timed out waiting for '$token' from $url" >&2
  return 1
}

assert_file_contains() {
  local file="$1" token="$2" description="$3"
  if ! grep -Fq "$token" "$file"; then
    echo "ERROR: assertion failed: $description" >&2
    echo "ERROR: missing token: $token" >&2
    echo "--- file contents ---" >&2
    cat "$file" >&2
    exit 1
  fi
}

echo "==> Validating prerequisites"
require_cmd docker
require_cmd go
require_cmd curl
require_cmd lsof

docker info >/dev/null 2>&1 || { echo "ERROR: Docker daemon not reachable." >&2; exit 1; }
[[ -d "$SMD_DIR" ]] || { echo "ERROR: SMD_DIR does not exist: $SMD_DIR" >&2; exit 1; }
[[ -d "$FRU_DIR" ]] || { echo "ERROR: FRU_DIR does not exist: $FRU_DIR" >&2; exit 1; }

echo "==> Verifying Redfish connectivity to $BMC_ADDRESS"
if ! curl -fsSk -u "$BMC_USER:$BMC_PASS" "https://$BMC_ADDRESS/redfish/v1/" >/dev/null 2>&1; then
  echo "ERROR: cannot reach https://$BMC_ADDRESS/redfish/v1/ — check BMC_ADDRESS, BMC_USER, BMC_PASS" >&2
  exit 1
fi
echo "    BMC reachable: OK"

echo "==> Clearing stale listeners"
clear_port_listeners "$SMD_PORT"
clear_port_listeners "$FRU_PORT"

if is_port_in_use "$PG_PORT"; then
  alt="$(pick_free_port 15432 25432 35432 45432 55432 || true)"
  [[ -z "$alt" ]] && { echo "ERROR: no free port for Postgres" >&2; exit 1; }
  echo "INFO: PG_PORT $PG_PORT in use, switching to $alt"
  PG_PORT="$alt"
fi

echo "==> Starting Postgres: $PG_CONTAINER"
docker run -d --name "$PG_CONTAINER" \
  -e POSTGRES_DB="$PG_DB" \
  -e POSTGRES_USER="$PG_USER" \
  -e POSTGRES_PASSWORD="$PG_PASS" \
  -p "$PG_PORT:5432" \
  postgres:10.8 >/dev/null

echo "==> Waiting for Postgres"
for _ in $(seq 1 60); do
  docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1 || {
  echo "ERROR: Postgres did not become ready" >&2; exit 1
}

echo "==> Building SMD and FRU-tracker binaries"
(cd "$SMD_DIR" && GOTOOLCHAIN=local go build -o "$SMD_BIN" ./cmd/smd && GOTOOLCHAIN=local go build -o "$SMD_INIT_BIN" ./cmd/smd-init)
(cd "$FRU_DIR" && GOTOOLCHAIN=local go build -o "$FRU_BIN" ./cmd/server)

echo "==> Initializing SMD schema"
SMD_DBPASS="$PG_PASS" "$SMD_INIT_BIN" \
  -dbhost 127.0.0.1 -dbport "$PG_PORT" -dbname "$PG_DB" -dbuser "$PG_USER" \
  -dbopts 'sslmode=disable' \
  -migrationsdir "$SMD_DIR/migrations/postgres" >"$SMD_INIT_LOG" 2>&1 || {
  echo "ERROR: smd-init failed:" >&2; cat "$SMD_INIT_LOG" >&2; exit 1
}

echo "==> Starting SMD"
SMD_DBPASS="$PG_PASS" SMD_DBTYPE=postgres "$SMD_BIN" \
  -dbtype postgres -dbhost 127.0.0.1 -dbport "$PG_PORT" \
  -dbname "$PG_DB" -dbuser "$PG_USER" -dbopts 'sslmode=disable' \
  -http-listen ":$SMD_PORT" -openchami -log 2 >"$SMD_LOG" 2>&1 &
SMD_PID=$!
wait_for_http "https://localhost:$SMD_PORT/hsm/v2/service/ready" 90 1

echo "==> Starting FRU-tracker"
(cd "$FRU_DIR" && env "FRU-TRACKER_PORT=$FRU_PORT" "FRU-TRACKER_DATABASE_URL=$FRU_DB_URL" \
  "$FRU_BIN" serve --port "$FRU_PORT" --database-url "$FRU_DB_URL" >"$FRU_LOG" 2>&1) &
FRU_PID=$!
wait_for_http "http://localhost:$FRU_PORT/devices" 90 1

echo "==> Storing BMC credentials"
(cd "$ROOT_DIR" && MASTER_KEY="$MASTER_KEY" go run ./cmd/secret-cli \
  --secret-id "$SECRET_ID" \
  --username "$BMC_USER" \
  --password "$BMC_PASS" \
  --store-path "$SECRETS_FILE" >/dev/null)

echo "==> Posting DiscoverySnapshot for $XNAME -> https://$BMC_ADDRESS"
cat > "$PAYLOAD_JSON" <<EOF
{
  "apiVersion": "example.fabrica.dev/v1",
  "kind": "DiscoverySnapshot",
  "metadata": { "name": "real-bmc-01" },
  "spec": {
    "rawData": [{
      "deviceType": "Node",
      "properties": {
        "xname":           "$XNAME",
        "secret_id":       "$SECRET_ID",
        "redfish_address": "https://$BMC_ADDRESS"
      }
    }]
  }
}
EOF

curl -fsS -X POST "http://localhost:$FRU_PORT/discoverysnapshots" \
  -H 'Content-Type: application/json' \
  -d @"$PAYLOAD_JSON" >/dev/null

wait_for_contains "http://localhost:$FRU_PORT/devices" "$XNAME" "$WORK_DIR/fru-devices.json" 60 1
echo "    Device visible in FRU-tracker: OK"

echo "==> Starting middleware (DRY_RUN=false, InsecureTLS=true)"
cd "$ROOT_DIR"
MASTER_KEY="$MASTER_KEY" \
FRU_MIDDLE_FRU_BASE_URL="http://localhost:$FRU_PORT" \
FRU_MIDDLE_SMD_BASE_URL="https://localhost:$SMD_PORT" \
FRU_MIDDLE_SECRETS_FILE="$SECRETS_FILE" \
FRU_MIDDLE_POLL_INTERVAL=5s \
FRU_MIDDLE_DRY_RUN=false \
FRU_MIDDLE_INSECURE_TLS=true \
go run ./cmd/server >"$MIDDLE_LOG" 2>&1 &
MIDDLE_PID=$!

echo "==> Waiting for RedfishEndpoint to appear in SMD (up to 2 min)..."
wait_for_contains "https://localhost:$SMD_PORT/hsm/v2/Inventory/RedfishEndpoints" "$XNAME" "$REDFISH_RESP" 120 2

echo "==> Waiting for ComponentEndpoints to appear in SMD (up to 2 min)..."
wait_for_contains "https://localhost:$SMD_PORT/hsm/v2/Inventory/ComponentEndpoints" "$XNAME" "$COMP_RESP" 120 2

echo ""
echo "==> Validating results"
assert_file_contains "$REDFISH_RESP" "\"ID\":\"$XNAME\"" "RedfishEndpoint registered for $XNAME"
assert_file_contains "$COMP_RESP" "$XNAME" "ComponentEndpoints created for $XNAME"

echo ""
echo "PASS: Real-BMC collection succeeded for $XNAME"
echo ""
echo "--- RedfishEndpoints ---"
cat "$REDFISH_RESP" | python3 -m json.tool 2>/dev/null || cat "$REDFISH_RESP"
echo ""
echo "--- ComponentEndpoints ---"
cat "$COMP_RESP" | python3 -m json.tool 2>/dev/null || cat "$COMP_RESP"
echo ""
echo "--- Middleware log tail ---"
tail -30 "$MIDDLE_LOG"
echo ""
echo "Log files:"
echo "  SMD:        $SMD_LOG"
echo "  FRU:        $FRU_LOG"
echo "  Middleware: $MIDDLE_LOG"
