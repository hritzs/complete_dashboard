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

  for port in 8003 8005 8010 5556 5557 3000; do
    kill_by_port "$port"
  done

  pkill -9 -f contract-master || true
  pkill -9 -f feed-decoder || true
  pkill -9 -f snapshot-service || true
  pkill -9 -f execution-gateway || true
  pkill -9 -f trade-worker || true
  pkill -9 -f vite || true

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
  "cd '$BASE_DIR/services/snapshot-service' && go run ./cmd" \
  "$LOG_DIR/3_snapshot.log"

sleep 2

start_if_needed \
  "Execution Gateway" \
  "8005" \
  "execution-gateway" \
  "cd '$BASE_DIR/services/execution-gateway' && go run ." \
  "$LOG_DIR/4_execution.log"

sleep 2

if [ "$FORCE_RESTART" = "1" ]; then
  kill_by_match "trade-worker"
fi

if is_proc_running "trade-worker"; then
  echo "Trade Worker already running"
else
  echo "Starting Trade Worker"
  (
    cd "$BUILD_DIR"
    ./services/trade-worker/trade-worker
  ) > "$LOG_DIR/5_trade-worker.log" 2>&1 &
fi

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
ss -lntp | egrep '5556|8003|8005|8010|3000' || true
echo

echo "Contract Master"
safe_curl "${HTTP_PROTO}://localhost:8010/api/health"
echo

echo "Snapshot Service"
safe_curl "${HTTP_PROTO}://localhost:8003/api/health"
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
