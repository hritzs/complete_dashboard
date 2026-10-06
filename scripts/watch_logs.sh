#!/usr/bin/env bash
# Readable, merged, colour-coded live view of every service log.
#
#   scripts/watch_logs.sh                    key events + once-a-minute HEDGE/SL/TP/TIME lines for every open trade
#                                             (the per-tick SNAP line is too frequent for this view -- use --monitor/--all)
#   scripts/watch_logs.sh --all              everything except heartbeats and raw market ticks
#   scripts/watch_logs.sh --errors           warnings and errors only
#   scripts/watch_logs.sh --monitor          ONLY the monitor/PnL lines: once-a-minute HEDGE/SL/TP/TIME, plus a
#                                             SNAP line every tick (every PollIntervalSec, default 1s)
#   scripts/watch_logs.sh --trade 122431     only lines mentioning this text (e.g. the tail of a trade uid)
#   scripts/watch_logs.sh --only trading     one area of the gateway log: trading (real orders, fills, builds,
#                                             hedges, exits, live straddle builds) | lut | sbuild_shadow | paper
#   options: --feed (include feed-decoder ticks)  --history N (lines of history, default 40)
#            --width N  --no-color
# Ctrl+C only stops this viewer; the services keep running -- true as long as
# they were started by start_platform.sh, which launches each one with setsid
# so it survives Ctrl+C here (see its start_if_needed comment). A service you
# started some other way (a bare `cmd &`) shares this terminal's process
# group and WILL die with this viewer.

set -u
BASE_DIR=$(cd "$(dirname "$0")/.." && pwd)
LOG_DIR="$BASE_DIR/logs"

MODE=events; TRADE=""; HISTORY=40; FEED=0; ONLY=""
WIDTH=$(tput cols 2>/dev/null || echo 220)
COLOR=0; [ -t 1 ] && COLOR=1

while [ $# -gt 0 ]; do
  case "$1" in
    --events) MODE=events ;;
    --all) MODE=all ;;
    --errors) MODE=errors ;;
    --monitor) MODE=monitor ;;
    --trade) TRADE="${2:-}"; shift ;;
    --only) ONLY="${2:-}"; shift ;;
    --history) HISTORY="${2:-40}"; shift ;;
    --width) WIDTH="${2:-220}"; shift ;;
    --feed) FEED=1 ;;
    --no-color) COLOR=0 ;;
    --color) COLOR=1 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 1 ;;
  esac
  shift
done

read -r -d '' AWK_PROG <<'AWK'
function paint(code, s) { return color ? "\033[" code "m" s "\033[0m" : s }
function field(str, key,    m) {
  if (match(str, key "=[^ ]*")) { m = substr(str, RSTART + length(key) + 1, RLENGTH - length(key) - 1); return m }
  return ""
}
BEGIN {
  tagcolor["EXEC"] = "34"; tagcolor["RECON"] = "35"; tagcolor["LAT"] = "90"
  tagcolor["SNAP"] = "90"; tagcolor["FEED"] = "90"; tagcolor["CONTRACT"] = "90"
  tagcolor["BRIDGE"] = "90"; tagcolor["UI"] = "90"
}
{
  line = $0
  lc0 = ""
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
  # NATS is not deployed in this environment; the reconciler already logs this
  # as a graceful degradation ("continuing without event publishing"), not a
  # real problem -- shown normally, just never colored/counted as an error.
  isbenign = (line ~ /NATS connect failed, continuing without event publishing/)
  iserr = (!isnoise && !isbenign && probe ~ /ERROR|FATAL|panic|[Ff]ailed|❌|⚠|WARN|mismatch|refused|giving up|HEDGE_FAILED|no matching order|error=/)

  # The per-tick snapshot line (tick=...) fires every PollIntervalSec --
  # too frequent for the default "key events" view. It still shows up in
  # --monitor and --all, just not folded into "events" alongside the
  # once-a-minute HEDGE/SL/TP/TIME lines and real triggers.
  isticksnap = (line ~ /\[MONITOR\]\[[^]]*\] tick=/)
  iskey = !isticksnap && (line ~ /\[RISK\]|\[WINGS\]|\[LUT(-SIM)?\]|\[SBUILD-(SHADOW|LIVE)\]|\[MODIFY\]|\[PARTIAL\]|\[BUILD-CHASE\]|_TRIGGER|HEDGE|Square-off|SQF reconciliation|BUILD (submitted|outcome|verification|submission)|\[GREEKSOFT ORDER\] (sending|submitted)|\[IRIS-WS\]|IRIS RX\] streaming_type=(Order|Trade)Response|DeployStraddle|Persisting SQF|PersistVerifiedFills done|login (successful|failed)|Greeksoft login|[Ll]istening|SYSTEM READY|\[MONITOR\]\[(TRD|SB-)|MINUTE-CHECK\]/)

  if (mode == "monitor")     show = (line ~ /\[MONITOR\]\[(TRD|SB-)|MINUTE-CHECK\]/)
  else if (mode == "errors") show = iserr
  else if (mode == "events") show = (iserr || iskey) && !isnoise
  else                       show = !isnoise && (tag != "FEED" || feed || iserr)
  if (!show) next
  if (trade != "" && index(line, trade) == 0) next

  # Live per-minute monitor/PnL lines: always shown as one compact, aligned
  # line (in every mode that lets them through -- events/all/monitor), not
  # buried in raw JSON-ish log text. Two independent per-minute lines exist
  # per trade: [MONITOR] (hedge decision) and [MINUTE-CHECK] (PnL/greeks).
  formatted = 0
  if (match(line, /\[MONITOR\]\[[^]]*\]/)) {
    uid = substr(line, RSTART + 10, RLENGTH - 11)
    rest = substr(line, RSTART + RLENGTH + 1)
    shortuid = substr(uid, length(uid) - 13)
    minute = field(rest, "minute")
    chk = field(rest, "check")
    act = field(rest, "action")
    if (chk == "SL" || chk == "TP") {
      st = field(rest, "status")
      line = sprintf("%-6s %-14s  min %s  %-14s  PnL/straddle %+9.2f  vs threshold %+9.2f",
                     chk, shortuid, minute, st, field(rest, "pnl_per_straddle") + 0, field(rest, "threshold") + 0)
      if (st == "BREACHED") lc0 = "33"
      formatted = 1
    } else if (chk == "TIME") {
      st = field(rest, "status")
      line = sprintf("TIME   %-14s  min %s  %-14s  target %s  remaining %s",
                     shortuid, minute, st, field(rest, "target"), field(rest, "remaining"))
      if (st == "BREACHED") lc0 = "33"
      formatted = 1
    } else if (line ~ /\] tick=[0-9:]+ snapshot /) {
      line = sprintf("SNAP   %-14s  %s  Spot %9.2f  SynFut %9.2f  PnL %+9.2f  |  Delta %+9.3f  Gamma %+10.6f  Theta %+8.2f  Vega %+8.2f  |  Strike %6s  CE %8.2f x%-5s  PE %8.2f x%-5s  Lot %s",
                     shortuid, field(rest, "tick"), field(rest, "spot") + 0, field(rest, "syn_fut") + 0, field(rest, "total_pnl") + 0,
                     field(rest, "delta") + 0, field(rest, "gamma") + 0, field(rest, "theta") + 0, field(rest, "vega") + 0,
                     field(rest, "strike"), field(rest, "ce_ltp") + 0, field(rest, "ce_qty"), field(rest, "pe_ltp") + 0, field(rest, "pe_qty"), field(rest, "lot_size"))
      formatted = 1
    } else if (act != "" && minute != "") {
      # Only the once-a-minute decision line carries minute=; the separate
      # "invoking trade-scoped hedge" line (same event, no minute=) is left
      # as its own raw line instead of being rendered as a second HEDGE row.
      held = field(rest, "hedge_held")
      synfut = field(rest, "syn_fut"); synspot = field(rest, "syn_spot"); futltp = field(rest, "fut_ltp")
      syntxt = sprintf("SynFut %s  SynSpot %s", (synfut == "" ? "-" : synfut), (synspot == "" ? "-" : synspot))
      if (futltp != "" && futltp + 0 > 0) syntxt = syntxt sprintf("  Fut %s", futltp)
      line = sprintf("HEDGE  %-14s  min %s  %-22s  Out %6.2f / Allowed %6.2f  Floor %5.2f  Delta %+8.3f  |  %s  |  Held %s",
                     shortuid, minute, act,
                     field(rest, "points_out") + 0, field(rest, "points_allowed") + 0,
                     field(rest, "min_points") + 0, field(rest, "net_delta") + 0,
                     syntxt, (held == "" ? "none" : held))
      if (act != "OK") lc0 = "33"
      formatted = 1
    }
  } else if (match(line, /\[MINUTE-CHECK\] trade=[^ ]*/)) {
    uid = substr(line, RSTART + 21, RLENGTH - 21)
    rest = substr(line, RSTART + RLENGTH + 1)
    act = field(rest, "action")
    rval = ""; uval = ""
    if (match(rest, /\(r=-?[0-9.]+,u=-?[0-9.]+\)/)) {
      paren = substr(rest, RSTART, RLENGTH)
      if (match(paren, /r=-?[0-9.]+/)) rval = substr(paren, RSTART + 2, RLENGTH - 2)
      if (match(paren, /u=-?[0-9.]+/)) uval = substr(paren, RSTART + 2, RLENGTH - 2)
    }
    line = sprintf("PNL    %-14s  %-6s  Total %+9.2f (Realized %+9.2f / Unrealized %+9.2f)  Delta %+8.3f  Gamma %+9.6f  %s",
                   substr(uid, length(uid) - 13), field(rest, "status"),
                   field(rest, "pnl") + 0, rval + 0, uval + 0,
                   field(rest, "delta") + 0, field(rest, "gamma") + 0, act)
    if (act != "OK" && act != "") lc0 = "33"
    formatted = 1
  }

  # Pre-formatted monitor/PnL lines above are deliberately built to be
  # read in full (this was getting silently chopped mid-field with "…",
  # e.g. cutting off theta/vega on the SNAP line) -- only raw, unbounded
  # passthrough log lines get truncated to terminal width.
  # Lines are printed in full (the terminal wraps them): cutting them at
  # the terminal width hid the end of warnings like FREEZE QTY and orders.

  lc = lc0
  if (iserr) lc = "31"
  else if (line ~ /\[RISK\]|\[WINGS\]|\[BUILD-CHASE\]|_TRIGGER|HEDGE_TRIGGERED|invoking trade-scoped hedge/) lc = "33"
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
FILES=("$LOG_DIR"/*.log)
if [ -n "$ONLY" ]; then
  FILES=("$LOG_DIR/exec/$ONLY.log")
  [ "$MODE" = events ] && MODE=all
  [ -e "${FILES[0]}" ] || { echo "no $ONLY log yet: ${FILES[0]} (choices: trading lut sbuild_shadow paper)" >&2; }
fi
for f in "${FILES[@]}"; do
  [ -e "$f" ] || continue
  tag=$(tag_for "$(basename "$f" .log)")
  [ -n "$ONLY" ] && tag=EXEC
  "${TAIL_CMD[@]}" -n "$HISTORY" -F "$f" 2>/dev/null | "$AWK_BIN" "${AWK_OPTS[@]}" -v tag="$tag" -v mode="$MODE" -v trade="$TRADE" \
      -v width="$WIDTH" -v feed="$FEED" -v color="$COLOR" "$AWK_PROG" &
done
wait
