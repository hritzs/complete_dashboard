#!/bin/bash

echo "🛑 Stopping Low-Latency Trading Platform..."

# Stop Go processes
pkill -f "go run"
pkill -f "main.go"

# Force kill any ghost processes holding our API ports
fuser -k 8080/tcp 8081/tcp 8003/tcp 5555/tcp 2>/dev/null || true

# Stop any spawned C++ binaries
pkill -f "trade-worker"
pkill -f "feed-decoder"

# Stop Docker UI
docker compose down

echo "✅ All services stopped."