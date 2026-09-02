#!/bin/bash
#
# This script scaffolds the full directory structure for the new trading platform
# as defined in the architecture.MD plan.
#

echo "🚀 Scaffolding the trading platform directory structure..."

# Top-level directories
mkdir -p docs
mkdir -p services
mkdir -p libs
mkdir -p ui/src ui/public
mkdir -p infra/docker infra/systemd infra/scripts infra/env
mkdir -p tests/integration tests/replay tests/perf

# C++ Services
for service in feed-decoder market-state trade-worker risk-engine execution-gateway; do
  mkdir -p "services/$service/src"
  mkdir -p "services/$service/include"
  mkdir -p "services/$service/tests"
  touch "services/$service/CMakeLists.txt"
  echo "Created C++ service: $service"
done

# Go Services
for service in session-manager reconciler contract-master snapshot-service trade-supervisor control-api; do
  mkdir -p "services/$service/cmd"
  mkdir -p "services/$service/internal"
  touch "services/$service/cmd/main.go"
  if [ ! -f "services/$service/go.mod" ]; then
    (cd "services/$service" && go mod init "trading-platform/services/$service")
  fi
  echo "Created Go service: $service"
done

# Libs
mkdir -p libs/contracts
mkdir -p libs/cpp-common/logging libs/cpp-common/config libs/cpp-common/time libs/cpp-common/zmq libs/cpp-common/shm libs/cpp-common/utils
mkdir -p libs/go-common/config libs/go-common/db libs/go-common/logging libs/go-common/events
mkdir -p libs/broker-greeksoft/auth libs/broker-greeksoft/models libs/broker-greeksoft/iris libs/broker-greeksoft/apollo libs/broker-greeksoft/rest libs/broker-greeksoft/mapping
mkdir -p libs/db/migrations libs/db/schema

# Add placeholder files to libs
touch libs/contracts/events.go
touch libs/db/schema.sql
touch libs/broker-greeksoft/client.go

echo "✅ Directory structure created successfully."

# --- File Content Placeholders ---

echo "📝 Adding placeholder content to key files..."

# Control API placeholder
cat <<EOF > services/control-api/cmd/main.go
package main
import "fmt"
func main() {
	fmt.Println("Control API service placeholder.")
}
EOF

# Snapshot Service placeholder
cat <<EOF > services/snapshot-service/cmd/main.go
package main
import "fmt"
func main() {
	// This service will be event-driven, consuming messages from the
	// reconciler (fills) and market-state (prices) to compute PnL.
	fmt.Println("Event-Driven Snapshot Service placeholder.")
}
EOF

echo "✅ Placeholder content added."
echo "🎉 All done! You can now start implementing the services."