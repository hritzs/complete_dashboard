#!/usr/bin/env bash
# Readable, merged, colour-coded live view of every service log.
#
#   scripts/watch_logs.sh                    key events only (orders, fills, hedges, SL/TP/TIME, Iris pushes, warnings)
#   scripts/watch_logs.sh --all              everything except heartbeats and raw market ticks
#   scripts/watch_logs.sh --errors           warnings and errors only
#   scripts/watch_logs.sh --trade 122431     only lines mentioning this text (e.g. the tail of a trade uid)
#   options: --feed (include feed-decoder ticks)  --history N (lines of history, default 40)
#            --width N  --no-color
# Ctrl+C only stops this viewer; the services keep running.

set -u
BASE_DIR=$(cd "$(dirname "$0")/.." && pwd)
LOG_DIR="$BASE_DIR/logs"

MODE=events; TRADE=""; HISTORY=40; FEED=0
WIDTH=$(tput cols 2>/dev/null || echo 220)
COLOR=0; [ -t 1 ] && COLOR=1

while [ $# -gt 0 ]; do
  case "$1" in
    --events) MODE=events ;;
    --all) MODE=all ;;
    --errors) MODE=errors ;;
    --trade) TRADE="${2:-}"; shift ;;
    --history) HISTORY="${2:-40}"; shift ;;
    --width) WIDTH="${2:-220}"; shift ;;
    --feed) FEED=1 ;;
    --no-color) COLOR=0 ;;
    --color) COLOR=1 ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 1 ;;
  esac
  shift
done

read -r -d '' AWK_PROG <<'AWK'
function paint(code, s) { return color ? "\033[" code "m" s "\033[0m" : s }
BEGIN {
  tagcolor["EXEC"] = "34"; tagcolor["RECON"] = "35"; tagcolor["LAT"] = "90"
  tagcolor["SNAP"] = "90"; tagcolor["FEED"] = "90"; tagcolor["CONTRACT"] = "90"
  tagcolor["BRIDGE"] = "90"; tagcolor["UI"] = "90"
}
{
  line = $0
  ts = "        "
  if (match(line, /^[0-9]+\/[0-9]+\/[0-9]+ [0-9:]+ /)) {
    ts = substr(line, 12, 8)
    line = substr(line, RSTART + RLENGTH)
  }
  if (line == "") next

  # Errors: ignore "<nil>" error fields and Iris heartbeat/licence JSON, which
  # carry "error_code":"0".
  probe = line
  gsub(/[A-Za-z_]*err(or)?=<nil>/, "", probe)
  isnoise = (line ~ /streaming_type=(HeartBeat|LicenseResponse|LoginResponse|Login)/)
  iserr = (!isnoise && probe ~ /ERROR|FATAL|panic|[Ff]ailed|❌|⚠|WARN|mismatch|refused|giving up|HEDGE_FAILED|no matching order|error=/)

  iskey = (line ~ /\[RISK\]|_TRIGGER|HEDGE|Square-off|SQF reconciliation|BUILD (submitted|outcome|verification|submission)|\[GREEKSOFT ORDER\] (sending|submitted)|\[IRIS-WS\]|IRIS RX\] streaming_type=(Order|Trade)Response|DeployStraddle|Persisting SQF|PersistVerifiedFills done|login (successful|failed)|Greeksoft login|[Ll]istening|SYSTEM READY|MONITOR\].*action=(HEDGE|DELTA_BELOW|BELOW_MIN|NO_)/)

  if (mode == "errors")      show = iserr
  else if (mode == "events") show = (iserr || iskey) && !isnoise
  else                       show = !isnoise && (tag != "FEED" || feed || iserr)
  if (!show) next
  if (trade != "" && index(line, trade) == 0) next

  if (length(line) > width - 16) line = substr(line, 1, width - 17) "…"

  lc = ""
  if (iserr) lc = "31"
  else if (line ~ /\[RISK\]|_TRIGGER|HEDGE_TRIGGERED|invoking trade-scoped hedge/) lc = "33"
  else if (line ~ /\[IRIS-WS\]|IRIS RX\]/) lc = "36"
  else if (line ~ /✅|Square-off completed|HEDGE result|verified=|FILLED/) lc = "32"

  t = (tag in tagcolor) ? tagcolor[tag] : "37"
  printf "%s %s %s\n", paint("90", ts), paint(t, sprintf("%-8s", tag)), (lc != "" ? paint(lc, line) : line)
  fflush()
}
AWK

tag_for() {
  case "$1" in
    1_contract-master) echo CONTRACT ;;
    2_feed-decoder)    echo FEED ;;
    3_snapshot)        echo SNAP ;;
    4_execution)       echo EXEC ;;
    6_reconciler)      echo RECON ;;
    7_greeksoft-feed-bridge) echo BRIDGE ;;
    8_ui)              echo UI ;;
    9_latency-dashboard) echo LAT ;;
    *) echo "${1:0:8}" ;;
  esac
}

trap 'trap - INT TERM EXIT; pkill -P $$ 2>/dev/null; exit 0' INT TERM EXIT

# mawk buffers non-interactive input, which would hold live lines back until its
# buffer fills; -W interactive makes it read and flush per line.
AWK_BIN=$(command -v mawk || command -v gawk || command -v awk)
AWK_OPTS=()
[ "$(basename "$AWK_BIN")" = "mawk" ] && AWK_OPTS=(-W interactive)
TAIL_CMD=(tail)
command -v stdbuf >/dev/null 2>&1 && TAIL_CMD=(stdbuf -oL tail)

echo "watching $LOG_DIR  mode=$MODE${TRADE:+  trade=$TRADE}  (Ctrl+C stops the viewer only)" >&2
for f in "$LOG_DIR"/*.log; do
  [ -e "$f" ] || continue
  tag=$(tag_for "$(basename "$f" .log)")
  "${TAIL_CMD[@]}" -n "$HISTORY" -F "$f" 2>/dev/null | "$AWK_BIN" "${AWK_OPTS[@]}" -v tag="$tag" -v mode="$MODE" -v trade="$TRADE" \
      -v width="$WIDTH" -v feed="$FEED" -v color="$COLOR" "$AWK_PROG" &
done
wait
