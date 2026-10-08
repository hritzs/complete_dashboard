#!/usr/bin/env bash
# Records the NSE F&O multicast feed per packet for feed audits
# (scripts/feed_compare.py), alongside the bridge's Apollo recording:
# NIFTY nearest expiry ATM +/- FEEDREC_STRIKES strikes (CE+PE) plus the
# near-month NIFTY future, until 15:40, to
# $FEEDREC_DIR (default ~/.trading-platform/feedrec)/YYYY-MM-DD_nse.csv.
# Read-only: its own multicast socket, never touches feed-decoder.
# Started by start_platform.sh; FEEDREC=0 disables it.
set -u

BASE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${BUILD_DIR:-$BASE_DIR/build}/services/feed-decoder/nse-feedrec"
DIR="${FEEDREC_DIR:-$HOME/.trading-platform/feedrec}"
STRIKES="${FEEDREC_STRIKES:-6}"
SNAPSHOT="${SNAPSHOT_URL:-http://127.0.0.1:8003}"
UNTIL="15:40"

if [ "${FEEDREC:-1}" = "0" ]; then
  echo "[FEEDREC-NSE] disabled (FEEDREC=0)"
  exit 0
fi
if [ ! -x "$BIN" ]; then
  echo "[FEEDREC-NSE] $BIN not built" >&2
  exit 1
fi
mkdir -p "$DIR"

# Wait for a live NIFTY chain (prices at the ATM), then pick the tokens.
TOKENS=""
while [ -z "$TOKENS" ]; do
  if [ "$(date +%s)" -ge "$(date -d "$UNTIL" +%s)" ]; then
    echo "[FEEDREC-NSE] past $UNTIL, nothing to record"
    exit 0
  fi
  TOKENS="$(curl -s --max-time 3 "$SNAPSHOT/api/option-chain/NIFTY" | python3 -c '
import csv, datetime, json, sys
strikes, index_csv = int(sys.argv[1]), sys.argv[2]
try:
    d = json.load(sys.stdin)["data"]
except Exception:
    sys.exit(0)
rows = d.get("chain") or []
atm = [i for i, r in enumerate(rows) if r["strike"] == d.get("atm") and (r.get("ce_ltp", 0) > 0 or r.get("pe_ltp", 0) > 0)]
if not atm:
    sys.exit(0)
i = atm[0]
toks = [t for r in rows[max(0, i - strikes):i + strikes + 1] for t in (r["ce_token"], r["pe_token"]) if t]
# Near-month NIFTY future from the token master (IndexTokens.csv).
today, futs = datetime.date.today(), []
try:
    for r in csv.reader(open(index_csv)):
        if len(r) > 7 and r[2] == "FUTIDX" and r[3] == "NIFTY":
            exp = datetime.datetime.strptime(r[4], "%d-%b-%y").date()
            if exp >= today:
                futs.append((exp, int(r[7])))
except (OSError, ValueError):
    pass
if futs:
    toks.append(min(futs)[1])
print(",".join(map(str, toks)))
' "$STRIKES" "$BASE_DIR/IndexTokens.csv")"
  [ -z "$TOKENS" ] && sleep 15
done

SECS=$(( $(date -d "$UNTIL" +%s) - $(date +%s) ))
OUT="$DIR/$(date +%F)_nse.csv"
echo "[FEEDREC-NSE] recording $(echo "$TOKENS" | tr ',' '\n' | wc -l) tokens for ${SECS}s to $OUT"
exec "$BIN" "$SECS" "$TOKENS" >> "$OUT"
