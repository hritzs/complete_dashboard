#!/usr/bin/env bash
# Whole-day log for one service. Since start_platform.sh now APPENDS across
# restarts, today's live file already holds the whole day (each restart marked
# "===== START"); a past day is logs/archive/YYYY-MM-DD/<file>. Older
# per-restart archives (logs/archive/YYYY-MM-DD/HHMMSS/) are included first.
#   scripts/day_logs.sh [YYYY-MM-DD] [file]     e.g. scripts/day_logs.sh 2026-10-06 4_execution.log
#   scripts/day_logs.sh 2026-10-06 exec/trading.log | grep 120000103
set -euo pipefail
BASE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LOG_DIR="${LOG_DIR:-$BASE_DIR/logs}"
DAY="${1:-$(date +%F)}"
FILE="${2:-4_execution.log}"
for run in "$LOG_DIR/archive/$DAY"/*/; do
  [ -f "$run$FILE" ] || continue
  echo "===== run archived $(basename "$run") ====="
  cat "$run$FILE"
done
if [ -f "$LOG_DIR/archive/$DAY/$FILE" ]; then
  echo "===== day archive ====="
  cat "$LOG_DIR/archive/$DAY/$FILE"
fi
if [ "$(cat "$LOG_DIR/.log_day" 2>/dev/null || date +%F)" = "$DAY" ] && [ -f "$LOG_DIR/$FILE" ]; then
  echo "===== live ====="
  cat "$LOG_DIR/$FILE"
fi
