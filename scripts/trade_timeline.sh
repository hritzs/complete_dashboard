#!/usr/bin/env bash
# Chronological story of one trade: its orders, how each order's state reached us
# (Iris websocket vs REST), confirmation latencies, legs, and the key log lines.
#
#   scripts/trade_timeline.sh 20260921122431     (any part of the trade uid; must match one trade)
#   scripts/trade_timeline.sh --last             most recent trade
set -eu
BASE_DIR=$(cd "$(dirname "$0")/.." && pwd)
DSN="${POSTGRES_DSN:-postgres://postgres:postgres@localhost:5432/trading?sslmode=disable}"
q() { psql "$DSN" -X -q -P pager=off "$@"; }

if [ "${1:-}" = "--last" ]; then
  UID_=$(q -Atc "select trade_uid from trades order by created_at desc limit 1")
else
  [ -n "${1:-}" ] || { sed -n '2,7p' "$0"; exit 1; }
  MATCHES=$(q -Atc "select trade_uid from trades where trade_uid like '%$1%' order by created_at")
  N=$(printf '%s\n' "$MATCHES" | grep -c . || true)
  [ "$N" = "1" ] || { echo "'$1' matches $N trades:"; printf '%s\n' "$MATCHES"; exit 1; }
  UID_="$MATCHES"
fi
echo "==================== $UID_ ===================="

echo; echo "--- trade ---"
q -c "select status, created_at::timestamp(0) as created, closed_at::timestamp(0) as closed, realized_pnl,
      config->>'ce_quantity' as ce_qty, config->>'pe_quantity' as pe_qty, config->>'lots' as lots
      from trades where trade_uid='$UID_'"

echo "--- orders (phase note: HEDGE orders are currently stored as PRIMARY) ---"
q -c "select o.created_at::time(3) as at, o.phase, o.side, o.quantity as qty, o.broker_order_id as broker_id, o.status,
      o.filled_qty as filled, round(o.avg_fill_price,2) as avg_px,
      round(a.latency_us/1000.0,1) as ack_ms, round(f.latency_us/1000.0,1) as fill_ms
      from orders o
      left join latency_samples a on a.order_id=o.id and a.stage='iris_confirmation'
      left join latency_samples f on f.order_id=o.id and f.stage='iris_fill'
      where o.trade_uid='$UID_' order by o.id"

echo "--- how each order's state reached us ---"
q -c "select o.broker_order_id as broker_id, e.event_timestamp::time(3) as at, e.status,
      case when e.raw_broker_response::text like '%streaming_type%' then 'IRIS websocket' else 'REST recovery' end as source
      from order_events e join orders o on o.id=e.order_id
      where o.trade_uid='$UID_' order by e.event_timestamp, e.id"

echo "--- legs (fills are currently double-written, so quantities can read 2x: see docs/TODO.md) ---"
q -c "select c.option_type as leg, l.current_quantity as qty, l.avg_entry_price as entry, l.avg_exit_price as exit, l.realized_pnl, l.status
      from trade_legs l join contracts c on c.id=l.contract_id join trades t on t.id=l.trade_id
      where t.trade_uid='$UID_' order by 1"

echo "--- key log lines ---"
for f in "$BASE_DIR"/logs/4_execution.log "$BASE_DIR"/logs/6_reconciler.log; do
  [ -f "$f" ] || continue
  grep -F "${UID_}" "$f" | grep -v "MINUTE-CHECK" | grep -E "_TRIGGER|HEDGE|Square-off|SQF reconciliation|BUILD (outcome|submitted)|ERROR|WARN|failed|⚠|❌" | sed -E 's/\{"request.*//' | cut -c1-240
done | sort | uniq
grep -hE "\[IRIS-WS\]" "$BASE_DIR"/logs/6_reconciler.log 2>/dev/null | grep -E "order=($(q -Atc "select string_agg(broker_order_id,'|') from orders where trade_uid='$UID_'"))\b" | cut -c1-200 || true

echo "--- monitor health ---"
STATUS=$(q -Atc "select status from trades where trade_uid='$UID_'")
TICKS=$(grep -F "MINUTE-CHECK] trade=${UID_}" "$BASE_DIR/logs/4_execution.log" 2>/dev/null | wc -l | tr -d ' ')
case "$STATUS" in
  CLOSED*|FAILED)
    LATE=$(grep -F "MINUTE-CHECK] trade=${UID_}" "$BASE_DIR/logs/4_execution.log" 2>/dev/null | grep -cE "status=(SQUARING_OFF|CLOSED)" || true)
    if [ "${LATE:-0}" -gt 0 ]; then
      echo "WARNING: $LATE minute-check lines were logged AFTER this trade closed -- its monitor goroutine was leaked."
      echo "         (fixed in runtime.go; restart execution-gateway to clear leaked monitors from before the fix)"
    else
      echo "OK: no monitor activity logged after close ($TICKS minute-checks in total)."
    fi ;;
  *) echo "trade is $STATUS -- $TICKS minute-checks so far" ;;
esac
