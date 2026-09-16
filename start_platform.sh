#!/bin/bash
set -e

BASE_DIR=$(cd "$(dirname "$0")" && pwd)
LOG_DIR="$BASE_DIR/logs"
BUILD_DIR="$BASE_DIR/build"

mkdir -p "$LOG_DIR"

MODE=${1:-normal}

FORCE_RESTART=0
BUILD_CPP=1
CLEAR_LOGS=1

case "$MODE" in
  normal)
    FORCE_RESTART=1
    BUILD_CPP=1
    CLEAR_LOGS=1
    ;;
  fast)
    FORCE_RESTART=0
    BUILD_CPP=0
    CLEAR_LOGS=0
    ;;
  fast-restart)
    FORCE_RESTART=1
    BUILD_CPP=0
    CLEAR_LOGS=1
    ;;
  *)
    echo "Unknown mode: $MODE"
    echo "Usage: ./start_platform.sh [normal|fast|fast-restart]"
    exit 1
    ;;
esac

echo "Mode: $MODE"
echo "BASE_DIR: $BASE_DIR"
echo "LOG_DIR: $LOG_DIR"

is_port_open() {
  ss -ltn "( sport = :$1 )" | grep -q ":$1" || return 1
  return 0
}

is_proc_running() {
  pgrep -f "$1" > /dev/null 2>&1
}

kill_by_port() {
  local PORT="$1"

  if is_port_open "$PORT"; then
    fuser -k "${PORT}/tcp" > /dev/null 2>&1 || true
    sleep 1
  fi
}

kill_by_match() {
  local MATCH="$1"

  if is_proc_running "$MATCH"; then
    pkill -f "$MATCH" || true
    sleep 1
  fi
}

start_if_needed() {
  local NAME="$1"
  local PORT="$2"
  local MATCH="$3"
  local CMD="$4"
  local LOG_FILE="$5"

  if [ "$FORCE_RESTART" = "1" ]; then
    echo "Force restarting $NAME"
    kill_by_port "$PORT"
    kill_by_match "$MATCH"
  fi

  if is_port_open "$PORT"; then
    echo "$NAME already healthy on port $PORT"
    return
  fi

  if is_proc_running "$MATCH"; then
    echo "$NAME process exists but port $PORT unhealthy, killing"
    kill_by_match "$MATCH"
  fi

  echo "Starting $NAME"
  (
    eval "$CMD"
  ) > "$LOG_FILE" 2>&1 &
}

safe_curl() {
  local URL="$1"
  curl -s "$URL" || true
}

if [ "$MODE" = "normal" ]; then
  echo "Cleaning old processes"

  for port in 8003 8005 8010 8021 8022 8023 5556 5557 3000; do
    kill_by_port "$port"
  done

  pkill -9 -f contract-master || true
  pkill -9 -f feed-decoder || true
  pkill -9 -f snapshot-service || true
  pkill -9 -f execution-gateway || true
  pkill -9 -f trade-worker || true
  pkill -9 -f vite || true
  pkill -9 -f "services/reconciler" || true
  pkill -9 -f "services/greeksoft-feed-bridge" || true
  pkill -9 -f "services/latency-dashboard" || true

  sleep 1
fi

if [ "$CLEAR_LOGS" = "1" ]; then
  rm -f "$LOG_DIR"/*.log || true
fi

echo "Loading environment variables"

ENV_FILE="$BASE_DIR/.env"

if [ -f "$ENV_FILE" ]; then
  sed -i 's/\r$//' "$ENV_FILE"
  set -a
  source "$ENV_FILE"
  set +a
  echo ".env loaded from $ENV_FILE"
else
  echo ".env file missing at $ENV_FILE"
  exit 1
fi

if [ -z "$POSTGRES_DSN" ]; then
  export POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/trading?sslmode=disable"
  echo "Using fallback POSTGRES_DSN"
fi


# Keep the local token masters used by Contract Master and Feed Decoder
# synchronized with the current SMB-mounted source. The script no-ops when
# both local copies already match today's validated source files.
echo "Synchronizing token master files"
if ! "${BASE_DIR}/scripts/update_index_tokens_daily.sh"; then
  echo "ERROR: token-master synchronization failed; refusing to start services." >&2
  exit 1
fi

echo "Applying DB migration"

if [ -f "$BASE_DIR/libs/db/migrations/0003_contract_master_raw.sql" ]; then
  psql "$POSTGRES_DSN" -f "$BASE_DIR/libs/db/migrations/0003_contract_master_raw.sql" > /dev/null 2>&1 || true
fi

echo "DB ready"

if [ "$BUILD_CPP" = "1" ]; then
  echo "Building C++ services"
  cmake -B "$BUILD_DIR" "$BASE_DIR"
  cmake --build "$BUILD_DIR" -j"$(nproc)"
else
  echo "Fast mode: skipping C++ build"
fi

echo "Ensuring services"

start_if_needed \
  "Contract Master" \
  "8010" \
  "contract-master" \
  "cd '$BASE_DIR/services/contract-master' && go run ./cmd" \
  "$LOG_DIR/1_contract-master.log"

sleep 3

start_if_needed \
  "Feed Decoder" \
  "5556" \
  "feed-decoder" \
  "cd '$BUILD_DIR' && ./services/feed-decoder/feed-decoder" \
  "$LOG_DIR/2_feed-decoder.log"

sleep 2

start_if_needed \
  "Snapshot Service" \
  "8003" \
  "snapshot-service" \
  "$BUILD_DIR/snapshot-service" \
  "$LOG_DIR/3_snapshot.log"

sleep 2

start_if_needed \
  "Execution Gateway" \
  "8005" \
  "execution-gateway" \
  "$BUILD_DIR/execution-gateway" \
  "$LOG_DIR/4_execution.log"

sleep 2

# Reconciler: consumes GreekSoft's live Iris order-push feed and persists
# canonical order/fill state to Postgres. See
# docs/greeksoft-integration-architecture.md. Requires the GREEK_* and
# POSTGRES_DSN vars already loaded from .env above.
start_if_needed \
  "Reconciler" \
  "8021" \
  "services/reconciler" \
  "cd '$BASE_DIR/services/reconciler' && go run ./cmd" \
  "$LOG_DIR/6_reconciler.log"

sleep 2

# GreekSoft feed bridge: Apollo market-data websocket as a staleness-gated
# backup to the existing primary feed (see docs/greeksoft-integration-architecture.md
# section 3). GREEK_APOLLO_TOKENS is optional -- set it in .env to back up
# specific instruments; unsolicited broadcasts (e.g. index ticks) work
# without it.
start_if_needed \
  "GreekSoft Feed Bridge" \
  "8022" \
  "services/greeksoft-feed-bridge" \
  "cd '$BASE_DIR/services/greeksoft-feed-bridge' && go run ./cmd" \
  "$LOG_DIR/7_greeksoft-feed-bridge.log"

sleep 2

# Latency dashboard: read-only REST API over latency_samples (written by
# the reconciler -- see services/reconciler/internal/persistence/latency.go).
# No NATS/websocket -- REST polling from the UI is sufficient at this
# platform's order volume. Requires POSTGRES_DSN already loaded from .env.
start_if_needed \
  "Latency Dashboard" \
  "8023" \
  "services/latency-dashboard" \
  "cd '$BASE_DIR/services/latency-dashboard' && go run ./cmd" \
  "$LOG_DIR/9_latency-dashboard.log"

sleep 2

if [ "$FORCE_RESTART" = "1" ]; then
  kill_by_match "trade-worker"
fi

# trade-worker now requires explicit <trade_id> <symbol> <quantity> args
# and refuses to run without them (it used to hardcode a live NIFTY
# 50-qty straddle that started unconditionally on every launch -- see
# services/trade-worker/src/main.cpp). Left uninvoked here on purpose:
# a real trade should be started deliberately (e.g. once trade-supervisor
# spawns workers per-trade, or by running the binary directly with args),
# not automatically every time the platform starts.
echo "Trade Worker: not auto-started (requires explicit trade args -- see main.cpp)"

if [ "$FORCE_RESTART" = "1" ]; then
  kill_by_port 3000
  pkill -f vite || true
fi

if is_port_open 3000; then
  echo "UI already running on port 3000"
else
  echo "Starting UI"
  (
    cd "$BASE_DIR/ui"
    if [ ! -d "node_modules" ]; then
      npm install
    fi
    npm run dev -- --host
  ) > "$LOG_DIR/8_ui.log" 2>&1 &
fi

sleep 5

HTTP_PROTO="http"

echo "Port status"
ss -lntp | egrep '5556|8003|8005|8010|8021|8022|8023|3000' || true
echo

echo "Contract Master"
safe_curl "${HTTP_PROTO}://localhost:8010/api/health"
echo

echo "Snapshot Service"
safe_curl "${HTTP_PROTO}://localhost:8003/api/health"
echo

echo "Reconciler"
safe_curl "${HTTP_PROTO}://localhost:8021/api/health"
echo

echo "GreekSoft Feed Bridge"
safe_curl "${HTTP_PROTO}://localhost:8022/api/health"
echo

echo "Latency Dashboard"
safe_curl "${HTTP_PROTO}://localhost:8023/api/health"
echo

echo "Chain readiness"
safe_curl "${HTTP_PROTO}://localhost:8003/api/option-chain/NIFTY" | grep -o '"success":[^,]*' || true
echo

echo "SYSTEM READY"
echo "UI: ${HTTP_PROTO}://localhost:3000"
echo

if compgen -G "$LOG_DIR/*.log" > /dev/null; then
  tail -f "$LOG_DIR"/*.log
else
  echo "No log files found in $LOG_DIR"
  echo "Use: ls -la $LOG_DIR"
fi
