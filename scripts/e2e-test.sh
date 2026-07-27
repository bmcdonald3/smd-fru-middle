#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMD_DIR="${SMD_DIR:-/Users/benmcdonald/smd}"
FRU_DIR="${FRU_DIR:-/Users/benmcdonald/fru-tracker}"

PG_PORT="${PG_PORT:-5432}"
SMD_PORT="${SMD_PORT:-27779}"
FRU_PORT="${FRU_PORT:-8080}"
REDFISH_PORT="${REDFISH_PORT:-18081}"

PG_DB="${PG_DB:-hmsds}"
PG_USER="${PG_USER:-hmsdsuser}"
PG_PASS="${PG_PASS:-hmsdsuser}"

MASTER_KEY="${MASTER_KEY:-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}"
XNAME="${XNAME:-x3000c0s17b0}"
SECRET_ID="${SECRET_ID:-bmc-x3000c0s17b0}"

WORK_DIR="$(mktemp -d /tmp/smd-fru-middle-e2e.XXXXXX)"
PG_CONTAINER="smd-fru-middle-pg-$$"
CHECKPOINT_PATH="$ROOT_DIR/data/checkpoint.json"
SECRETS_FILE="$ROOT_DIR/secrets.json"
FRU_DB_URL="file:$WORK_DIR/fru-tracker-e2e.db?cache=shared&_fk=1"

SMD_BIN="$WORK_DIR/smd-local"
SMD_INIT_BIN="$WORK_DIR/smd-init-local"
FRU_BIN="$WORK_DIR/fru-tracker-local"
REDFISH_MOCK="$WORK_DIR/redfish-mock.go"
PAYLOAD_JSON="$WORK_DIR/fru-upload-e2e.json"

SMD_PID=""
FRU_PID=""
REDFISH_PID=""
MIDDLE_PID=""

SMD_LOG="$WORK_DIR/smd.log"
FRU_LOG="$WORK_DIR/fru.log"
REDFISH_LOG="$WORK_DIR/redfish.log"
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

check_port_free() {
  local port="$1"
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "ERROR: port $port is already in use; please stop the existing process or choose another port." >&2
    exit 1
  fi
}

is_port_in_use() {
  local port="$1"
  lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
}

port_listener_pids() {
  local port="$1"
  lsof -t -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | sort -u
}

stop_pid() {
  local pid="$1"
  if [[ -z "$pid" ]]; then
    return 0
  fi
  if ! kill -0 "$pid" >/dev/null 2>&1; then
    return 0
  fi

  kill "$pid" >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do
    if ! kill -0 "$pid" >/dev/null 2>&1; then
      wait "$pid" 2>/dev/null || true
      return 0
    fi
    sleep 0.2
  done

  kill -9 "$pid" >/dev/null 2>&1 || true
  wait "$pid" 2>/dev/null || true
}

clear_port_listeners() {
  local port="$1"
  local pids

  pids="$(port_listener_pids "$port" || true)"
  if [[ -z "$pids" ]]; then
    return 0
  fi

  echo "INFO: clearing stale listener(s) on port $port: $pids"
  local pid
  while IFS= read -r pid; do
    [[ -n "$pid" ]] || continue
    stop_pid "$pid"
  done <<<"$pids"
}

pick_free_port() {
  local candidates=("$@")
  local candidate
  for candidate in "${candidates[@]}"; do
    if ! is_port_in_use "$candidate"; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

cleanup() {
  set +e
  if [[ -n "$MIDDLE_PID" ]]; then stop_pid "$MIDDLE_PID"; fi
  if [[ -n "$FRU_PID" ]]; then stop_pid "$FRU_PID"; fi
  if [[ -n "$SMD_PID" ]]; then stop_pid "$SMD_PID"; fi
  if [[ -n "$REDFISH_PID" ]]; then stop_pid "$REDFISH_PID"; fi
  clear_port_listeners "$SMD_PORT"
  clear_port_listeners "$FRU_PORT"
  clear_port_listeners "$REDFISH_PORT"
  clear_port_listeners "$PG_PORT"
  docker rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
  echo ""
  echo "Logs and artifacts kept in: $WORK_DIR"
}
trap cleanup EXIT

wait_for_http() {
  local url="$1"
  local max_tries="${2:-60}"
  local sleep_secs="${3:-1}"

  for _ in $(seq 1 "$max_tries"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$sleep_secs"
  done

  echo "ERROR: timed out waiting for $url" >&2
  return 1
}

wait_for_contains() {
  local url="$1"
  local token="$2"
  local out_file="$3"
  local max_tries="${4:-120}"
  local sleep_secs="${5:-1}"

  for _ in $(seq 1 "$max_tries"); do
    local out
    out="$(curl -fsS "$url" || true)"
    if [[ -n "$out" ]] && grep -q "$token" <<<"$out"; then
      printf '%s\n' "$out" > "$out_file"
      return 0
    fi
    sleep "$sleep_secs"
  done

  echo "ERROR: timed out waiting for token '$token' from $url" >&2
  return 1
}

echo "==> Validating prerequisites"
require_cmd docker
require_cmd go
require_cmd curl
require_cmd lsof

if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker daemon is not reachable. Start Docker and retry." >&2
  exit 1
fi

[[ -d "$SMD_DIR" ]] || { echo "ERROR: SMD_DIR does not exist: $SMD_DIR" >&2; exit 1; }
[[ -d "$FRU_DIR" ]] || { echo "ERROR: FRU_DIR does not exist: $FRU_DIR" >&2; exit 1; }

echo "==> Clearing stale listeners from prior runs"
clear_port_listeners "$SMD_PORT"
clear_port_listeners "$FRU_PORT"
clear_port_listeners "$REDFISH_PORT"

if is_port_in_use "$PG_PORT"; then
  clear_port_listeners "$PG_PORT"
  if is_port_in_use "$PG_PORT"; then
    alt_pg_port="$(pick_free_port 15432 25432 35432 45432 55432 65432 || true)"
    if [[ -z "$alt_pg_port" ]]; then
      echo "ERROR: PostgreSQL host port $PG_PORT is in use and no fallback port was available." >&2
      exit 1
    fi
    echo "INFO: port $PG_PORT is still in use; switching PostgreSQL host port to $alt_pg_port"
    PG_PORT="$alt_pg_port"
  fi
fi

check_port_free "$SMD_PORT"
check_port_free "$FRU_PORT"
check_port_free "$REDFISH_PORT"

if [[ "${#MASTER_KEY}" -ne 64 ]]; then
  echo "ERROR: MASTER_KEY must be a 64-character hex string" >&2
  exit 1
fi

echo "==> Starting Postgres container: $PG_CONTAINER"
docker run -d --name "$PG_CONTAINER" \
  -e POSTGRES_DB="$PG_DB" \
  -e POSTGRES_USER="$PG_USER" \
  -e POSTGRES_PASSWORD="$PG_PASS" \
  -p "$PG_PORT:5432" \
  postgres:10.8 >/dev/null

echo "==> Waiting for Postgres readiness"
for _ in $(seq 1 60); do
  if docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1 || {
  echo "ERROR: Postgres did not become ready" >&2
  exit 1
}

echo "==> Building SMD and FRU binaries"
(
  cd "$SMD_DIR"
  go build -o "$SMD_BIN" ./cmd/smd
  go build -o "$SMD_INIT_BIN" ./cmd/smd-init
)
(
  cd "$FRU_DIR"
  go build -o "$FRU_BIN" ./cmd/server
)

echo "==> Initializing SMD schema"
if ! SMD_DBPASS="$PG_PASS" "$SMD_INIT_BIN" \
  -dbhost 127.0.0.1 \
  -dbport "$PG_PORT" \
  -dbname "$PG_DB" \
  -dbuser "$PG_USER" \
  -dbopts 'sslmode=disable' \
  -migrationsdir "$SMD_DIR/migrations/postgres" >"$SMD_INIT_LOG" 2>&1; then
  echo "ERROR: smd-init failed. Output:" >&2
  cat "$SMD_INIT_LOG" >&2
  exit 1
fi

echo "==> Starting SMD"
SMD_DBPASS="$PG_PASS" SMD_DBTYPE=postgres "$SMD_BIN" \
  -dbtype postgres \
  -dbhost 127.0.0.1 \
  -dbport "$PG_PORT" \
  -dbname "$PG_DB" \
  -dbuser "$PG_USER" \
  -dbopts 'sslmode=disable' \
  -http-listen ":$SMD_PORT" \
  -openchami \
  -log 2 >"$SMD_LOG" 2>&1 &
SMD_PID=$!
wait_for_http "http://localhost:$SMD_PORT/hsm/v2/service/ready" 90 1

echo "==> Starting FRU-tracker"
cd "$FRU_DIR"
env "FRU-TRACKER_PORT=$FRU_PORT" "FRU-TRACKER_DATABASE_URL=$FRU_DB_URL" \
  "$FRU_BIN" serve --port "$FRU_PORT" --database-url "$FRU_DB_URL" >"$FRU_LOG" 2>&1 &
FRU_PID=$!
cd "$ROOT_DIR"
wait_for_http "http://localhost:$FRU_PORT/devices" 90 1

echo "==> Starting local Redfish mock on port $REDFISH_PORT"
cat > "$REDFISH_MOCK" <<EOF
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

  log.Printf("redfish mock listening on :$REDFISH_PORT")
  log.Fatal(http.ListenAndServe(":$REDFISH_PORT", mux))
}
EOF

go run "$REDFISH_MOCK" >"$REDFISH_LOG" 2>&1 &
REDFISH_PID=$!
wait_for_http "http://localhost:$REDFISH_PORT/redfish/v1/Systems" 60 1

echo "==> Preparing middleware state"
rm -f "$CHECKPOINT_PATH"
(
  cd "$ROOT_DIR"
  MASTER_KEY="$MASTER_KEY" go run ./cmd/secret-cli \
    --secret-id "$SECRET_ID" \
    --username root \
    --password changeme \
    --store-path "$SECRETS_FILE" >/dev/null
)

echo "==> Posting DiscoverySnapshot to FRU-tracker"
cat > "$PAYLOAD_JSON" <<EOF
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
          "xname": "$XNAME",
          "secret_id": "$SECRET_ID",
          "redfish_address": "http://localhost:$REDFISH_PORT",
          "redfish_uri": "/redfish/v1/Systems/System-1"
        }
      }
    ]
  }
}
EOF

curl -fsS -X POST "http://localhost:$FRU_PORT/discoverysnapshots" \
  -H 'Content-Type: application/json' \
  -d @"$PAYLOAD_JSON" >/dev/null

wait_for_contains "http://localhost:$FRU_PORT/devices" "NODE12345" "$WORK_DIR/fru-devices.json" 120 1

echo "==> Starting middleware"
cd "$ROOT_DIR"
MASTER_KEY="$MASTER_KEY" \
FRU_MIDDLE_FRU_BASE_URL="http://localhost:$FRU_PORT" \
FRU_MIDDLE_SMD_BASE_URL="http://localhost:$SMD_PORT" \
FRU_MIDDLE_SECRETS_FILE="$SECRETS_FILE" \
FRU_MIDDLE_POLL_INTERVAL=2s \
FRU_MIDDLE_DRY_RUN=false \
go run ./cmd/server >"$MIDDLE_LOG" 2>&1 &
MIDDLE_PID=$!

wait_for_contains "http://localhost:$SMD_PORT/hsm/v2/Inventory/RedfishEndpoints" "$XNAME" "$REDFISH_RESP" 120 1
wait_for_contains "http://localhost:$SMD_PORT/hsm/v2/Inventory/ComponentEndpoints" "$XNAME" "$COMP_RESP" 120 1

echo ""
echo "PASS: End-to-end FRU -> middleware -> SMD test succeeded"
echo ""
echo "--- RedfishEndpoints ---"
cat "$REDFISH_RESP"
echo ""
echo "--- ComponentEndpoints ---"
cat "$COMP_RESP"
echo ""
echo "Log files:"
echo "  SMD:      $SMD_LOG"
echo "  FRU:      $FRU_LOG"
echo "  Redfish:  $REDFISH_LOG"
echo "  Middleware: $MIDDLE_LOG"
