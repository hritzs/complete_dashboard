#!/usr/bin/env bash
set -euo pipefail

ROOT="$(pwd)"

FILES=(
  "services/control-api/internal/execution/interfaces.go"
  "services/control-api/internal/execution/models.go"
  "services/control-api/internal/execution/registry.go"
  "services/control-api/internal/execution/service.go"
  "services/control-api/internal/handlers/straddle.go"
  "services/control-api/internal/config/config.go"
  "services/execution-gateway/internal/brokers/xts/client.go"
  "services/execution-gateway/internal/brokers/xts/auth.go"
  "services/execution-gateway/internal/brokers/xts/orders.go"
  "services/execution-gateway/internal/brokers/xts/quotes.go"
  "services/execution-gateway/internal/brokers/greek/client.go"
  "services/execution-gateway/internal/brokers/greek/auth.go"
  "services/execution-gateway/internal/brokers/greek/orders.go"
  "services/execution-gateway/internal/brokers/greek/quotes.go"
  "services/execution-gateway/internal/router/router.go"
)

for f in "${FILES[@]}"; do
  mkdir -p "$(dirname "$f")"
  [ -f "$f" ] || touch "$f"
done

OUT="exec-layer-files-dump.txt"
{
  echo "ROOT: $ROOT"
  echo "GENERATED: $(date '+%Y-%m-%d %H:%M:%S %Z')"
  echo
  for f in "${FILES[@]}"; do
    echo "===== FILE: $f ====="
    if [ -s "$f" ]; then
      cat "$f"
    else
      echo "<EMPTY FILE>"
    fi
    echo
  done
} > "$OUT"

echo "Created/verified files and wrote dump to: $ROOT/$OUT"
