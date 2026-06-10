#!/bin/bash

BASE_DIR=$(pwd)

echo "🧹 Cleaning up old zombie processes..."
# Kill anything holding our known ports
fuser -k 8003/tcp 2>/dev/null
fuser -k 5555/tcp 2>/dev/null
fuser -k 5556/tcp 2>/dev/null
fuser -k 5557/tcp 2>/dev/null
fuser -k 5558/tcp 2>/dev/null
fuser -k 3000/tcp 2>/dev/null

pkill -9 -f "market-data-gateway|feed-decoder|snapshot-service|execution-gateway|vite" 2>/dev/null

sleep 1

# Create a fresh logs directory
mkdir -p "$BASE_DIR/logs"
rm -f "$BASE_DIR/logs"/*.log

echo "🔑 Loading XTS Credentials from .env file..."
if [ -f "$BASE_DIR/.env" ]; then
    # Strip Windows carriage returns (\r) to prevent XTS login errors
    sed -i 's/\r$//' "$BASE_DIR/.env"
    set -a
    source "$BASE_DIR/.env"
    set +a
    echo "   ✅ Loaded from $BASE_DIR/.env"
elif [ -f "$BASE_DIR/../.env" ]; then
    # Strip Windows carriage returns (\r)
    sed -i 's/\r$//' "$BASE_DIR/../.env"
    set -a
    source "$BASE_DIR/../.env"
    set +a
    echo "   ✅ Loaded from $BASE_DIR/../.env"
else
    echo "⚠️  WARNING: No .env file found in $BASE_DIR! Services may fail to login to XTS."
fi

echo "🚀 Starting Ultra-Low Latency Trading Platform (Core 6 Services)..."

# 1. Database & Infra (Docker)
echo "   -> Starting Infra (Postgres/Redis)..."
docker compose up -d

# 2. Market Data Gateway (Go)
echo "   -> Starting Market Data Gateway..."
((cd "$BASE_DIR/services/market-data-gateway" && go run cmd/main.go > "$BASE_DIR/logs/1_market-data.log" 2>&1) & echo $! > /tmp/md.pid)

# Give MD Gateway time to login to XTS and bind ZMQ 5555
sleep 3

# 3. Feed Decoder (C++)
echo "   -> Starting C++ Feed Decoder..."
((cd "$BASE_DIR/services/feed-decoder/build" && ./feed-decoder > "$BASE_DIR/logs/2_feed-decoder.log" 2>&1) & echo $! > /tmp/fd.pid)

# Give C++ a second to map the memory
sleep 1

# 4. Snapshot Service (Go)
echo "   -> Starting Snapshot Service..."
((cd "$BASE_DIR/services/snapshot-service" && go run cmd/main.go > "$BASE_DIR/logs/3_snapshot.log" 2>&1) & echo $! > /tmp/snap.pid)

# 5. Execution Gateway (Go)
echo "   -> Starting Execution Gateway..."
((cd "$BASE_DIR/services/execution-gateway" && go run main.go > "$BASE_DIR/logs/4_execution.log" 2>&1) & echo $! > /tmp/exec.pid)

# 6. React UI (Vite)
echo "   -> Starting React UI Server..."
((cd "$BASE_DIR/ui" && export NVM_DIR="$HOME/.nvm" && [ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh" && npm install && npm run dev -- --host > "$BASE_DIR/logs/8_ui.log" 2>&1) & echo $! > /tmp/ui.pid)

echo "✅ All services running in background!"
echo "📜 Tailing logs... (Press Ctrl+C to shutdown everything)"
echo "------------------------------------------------------"

# Trap Ctrl+C to kill all background processes cleanly
trap "echo -e '\n🛑 Shutting down all services...'; kill \$(cat /tmp/md.pid) \$(cat /tmp/fd.pid) \$(cat /tmp/snap.pid) \$(cat /tmp/exec.pid) \$(cat /tmp/ui.pid) 2>/dev/null; docker compose down 2>/dev/null; exit 0" SIGINT SIGTERM

# Give services a second to create log files, then tail them
sleep 2
tail -f "$BASE_DIR"/logs/*.log