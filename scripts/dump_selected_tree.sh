#!/usr/bin/env bash
set -euo pipefail

ROOT="$(pwd)"
OUT="selected-services-libs-dump.txt"

TARGETS=(
  "services/control-api"
  "services/execution-gateway"
  "libs"
)

{
  echo "ROOT: $ROOT"
  echo "GENERATED: $(date '+%Y-%m-%d %H:%M:%S %Z')"
  echo "TARGETS: ${TARGETS[*]}"
  echo

  find "${TARGETS[@]}" -type f \
    ! -path "*/node_modules/*" \
    ! -path "*/build/*" \
    ! -path "*/build_worker/*" \
    ! -path "*/.git/*" \
    ! -path "*/__pycache__/*" \
    ! -path "*/dist/*" \
    | sort | while read -r f; do
        echo "===== FILE: $f ====="
        if file --mime "$f" | grep -q 'charset=binary'; then
          echo "<BINARY FILE SKIPPED>"
        else
          cat "$f"
        fi
        echo
      done
} > "$OUT"

echo "Dump written to: $ROOT/$OUT"
