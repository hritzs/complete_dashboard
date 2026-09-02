#!/bin/bash

echo "🛑 Stopping Low-Latency Trading Platform..."

# Stop Go processes
pkill -f "go run"
pkill -f "main.go"

# Force kill any ghost processes holding our API ports
fuser -k 3000/tcp 8000/tcp 8001/tcp 8003/tcp 8005/tcp 5555/tcp 5556/tcp 5557/tcp 5558/tcp 2>/dev/null || true

# Stop any spawned C++ binaries
pkill -f "trade-worker"
pkill -f "feed-decoder"

# Stop the React UI
pkill -f "vite"

# Stop Docker UI
docker compose down

echo "✅ All services stopped."