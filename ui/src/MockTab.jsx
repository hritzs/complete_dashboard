import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// MOCK SESSION -- sell / buy ONE instrument at a time (REAL broker orders).
// On an exchange mock day the feed is random, so this tab lists only the
// instruments that are tradable right now (a bid and an ask close together
// and a recent trade) and sends single LIMIT orders a few ticks through the
// book. It keeps its own orders and net positions, with close / cancel and
// an automatic sell-then-buy-back round trip.
// Backend: services/execution-gateway/internal/trading/mock_session.go

const box = { border: '1px solid rgba(255,255,255,0.10)', 'border-radius': '8px', padding: '10px 12px', margin: '10px 0' };
const muted = { opacity: 0.65, 'font-size': '12px' };
const th = { 'text-align': 'right', padding: '6px 8px', 'font-size': '11px', opacity: 0.7, 'white-space': 'nowrap' };
const td = { 'text-align': 'right', padding: '5px 8px', 'font-size': '13px', 'white-space': 'nowrap' };
const left = { 'text-align': 'left' };
const STATUS_BG = { FILLED: '#2e7d32', OPEN: '#c98500', SUBMITTED: '#c98500', PARTIALLY_FILLED: '#c98500', REJECTED: '#b23b3b', CANCELLED: '#546e7a', SUBMIT_FAILED: '#b23b3b' };
const n2 = (v) => (v == null ? '—' : Number(v).toFixed(2));

export default function MockTab() {
  const [st, setSt] = createSignal(null);
  const [msg, setMsg] = createSignal('');
  const [lots, setLots] = createSignal('1');
  const [busy, setBusy] = createSignal(false);

  const load = async () => {
    try {
      setSt(await (await fetch('/api/mock')).json());
    } catch (e) { setMsg(String(e)); }
  };
  const post = async (body) => {
    setBusy(true);
    setMsg('');
    try {
      const d = await (await fetch('/api/mock', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...body, confirm: st()?.confirm_text || 'MOCK ORDER' }) })).json();
      setSt(d);
      if (!d.success) setMsg(d.error || 'failed');
      else if (d.order) setMsg(`${d.order.side} ${d.order.name} ${d.order.qty} @ ${n2(d.order.limit)} → ${d.order.status}${d.order.filled_qty ? ` (filled ${d.order.filled_qty} @ ${n2(d.order.avg_price)})` : ''}`);
      return d;
    } catch (e) { setMsg(String(e)); } finally { setBusy(false); }
  };
  const nLots = () => Math.max(1, Math.floor(Number(lots()) || 1));

  const order = (i, side) => {
    const px = side === 'SELL' ? i.sell_at : i.buy_at;
    if (!window.confirm(`REAL ORDER on ${st()?.account}\n\n${side} ${nLots()} lot(s) (${nLots() * i.lot_size}) of ${i.symbol} ${i.expiry} ${i.strike} ${i.leg}\nLIMIT ${n2(px)}  (bid ${n2(i.bid)} / ask ${n2(i.ask)})\n\nSend?`)) return;
    post({ action: 'order', symbol: i.symbol, expiry: i.expiry, token: i.token, side, lots: nLots() });
  };
  const roundTrip = (i) => {
    if (!window.confirm(`REAL ORDERS on ${st()?.account}\n\nRound trip: SELL ${nLots()} lot(s) of ${i.symbol} ${i.expiry} ${i.strike} ${i.leg}, wait for the fill, then BUY it back.\n\nStart?`)) return;
    post({ action: 'roundtrip', symbol: i.symbol, expiry: i.expiry, token: i.token, lots: nLots() });
  };
  const close = (p) => {
    const side = p.net_qty < 0 ? 'BUY' : 'SELL';
    if (!window.confirm(`REAL ORDER on ${st()?.account}\n\nClose ${p.name}: ${side} ${Math.abs(p.net_qty)} (never more than the open quantity)\n\nSend?`)) return;
    post({ action: 'close', symbol: p.symbol, token: p.token });
  };
  const cancel = (o) => post({ action: 'cancel', broker_order_id: o.broker_order_id });

  let timer;
  onMount(() => { load(); timer = setInterval(() => { if (!document.hidden && !busy()) load(); }, 2000); });
  onCleanup(() => clearInterval(timer));

  return (
    <div class="lut-tab" style={{ padding: '4px 2px' }}>
      <div style={{ ...box, border: '2px solid #c98500', background: 'rgba(201,133,0,0.07)' }}>
        <div style={{ display: 'flex', 'align-items': 'center', gap: '14px', 'flex-wrap': 'wrap' }}>
          <strong style={{ 'font-size': '16px' }}>MOCK SESSION — single instrument</strong>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: '#b23b3b', 'font-size': '12px' }}>REAL broker orders · {st()?.account}</span>
          <Show when={st() && !st().in_session}><span style={{ color: '#e6a067', 'font-size': '12px' }}>outside the broker session — orders are refused</span></Show>
          <label class="control-block" style={{ 'margin-left': 'auto', display: 'flex', 'align-items': 'center', gap: '6px' }}>
            <span class="control-label">Lots per order</span>
            <input class="symbol-select" style={{ width: '70px' }} type="text" inputmode="numeric" value={lots()} onInput={(e) => setLots(e.currentTarget.value)} />
          </label>
        </div>
        <div style={muted}>
          Lists only instruments tradable right now ({st()?.filter}). A sell goes {`3`} ticks under the lower of bid / ask, a buy 3 ticks over the higher, as a LIMIT order. Nothing here creates a trade, monitor, hedge or exit rule.
        </div>
        <Show when={msg()}><div style={{ 'margin-top': '6px', color: '#e6c067', 'font-size': '13px' }}>{msg()}</div></Show>
        <Show when={st()?.round_trip_note}>
          <div style={{ 'margin-top': '4px', 'font-size': '13px' }}>
            <span style={{ padding: '1px 8px', 'border-radius': '8px', background: st()?.round_trip_running ? '#c98500' : '#455a64', 'margin-right': '8px' }}>{st()?.round_trip_running ? 'ROUND TRIP RUNNING' : 'round trip'}</span>
            {st()?.round_trip_note}
          </div>
        </Show>
      </div>

      <div style={box}>
        <div style={{ display: 'flex', 'align-items': 'baseline', gap: '12px' }}>
          <strong>Positions (this tab's fills today)</strong>
          <span style={muted}>total MTM {n2(st()?.total_mtm)} · shorts valued at the ask, longs at the bid</span>
        </div>
        <Show when={(st()?.positions || []).length} fallback={<div style={muted}>no mock fills today</div>}>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Instrument</th><th style={th}>Net qty</th><th style={th}>Avg</th><th style={th}>Bid</th><th style={th}>Ask</th><th style={th}>LTP</th><th style={th}>MTM</th><th style={th}></th>
            </tr></thead>
            <tbody>
              <For each={st()?.positions || []}>{(p) => (
                <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                  <td style={{ ...td, ...left }}>{p.name}</td>
                  <td style={{ ...td, color: p.net_qty < 0 ? '#e66767' : p.net_qty > 0 ? '#5fd38d' : undefined }}>{p.net_qty === 0 ? 'flat' : p.net_qty}</td>
                  <td style={td}>{p.net_qty === 0 ? '—' : n2(p.avg_price)}</td>
                  <td style={td}>{n2(p.bid)}</td><td style={td}>{n2(p.ask)}</td><td style={td}>{n2(p.ltp)}</td>
                  <td style={{ ...td, color: p.mtm < 0 ? '#e66767' : '#5fd38d' }}>{n2(p.mtm)}</td>
                  <td style={td}>
                    <Show when={p.net_qty !== 0}>
                      <button class="tab-btn" disabled={busy()} style={{ background: '#b23b3b' }} onClick={() => close(p)}>{p.net_qty < 0 ? 'Buy back' : 'Sell out'}</button>
                    </Show>
                  </td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
      </div>

      <div style={box}>
        <div style={{ display: 'flex', 'align-items': 'baseline', gap: '12px' }}>
          <strong>Tradable now — {st()?.symbol}</strong>
          <span style={muted}>{(st()?.instruments || []).length} instrument(s), tightest spread first · refreshes every 2 s</span>
          <Show when={st()?.scan_error}><span style={{ color: '#e66767', 'font-size': '12px' }}>{st().scan_error}</span></Show>
        </div>
        <Show when={(st()?.instruments || []).length} fallback={<div style={muted}>nothing passes the filter right now</div>}>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Expiry</th><th style={th}>Strike</th><th style={th}>Leg</th><th style={th}>Bid</th><th style={th}>Ask</th><th style={th}>LTP</th><th style={th}>Spread</th><th style={th}>Last trade</th><th style={th}>Sell at</th><th style={th}>Buy at</th><th style={th}></th>
            </tr></thead>
            <tbody>
              <For each={st()?.instruments || []}>{(i) => (
                <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                  <td style={{ ...td, ...left }}>{i.expiry}</td>
                  <td style={td}>{i.strike}</td><td style={td}>{i.leg}</td>
                  <td style={td}>{n2(i.bid)}</td><td style={td}>{n2(i.ask)}</td><td style={td}>{n2(i.ltp)}</td>
                  <td style={td}>{n2(i.spread_pct)}%</td><td style={td}>{i.last_trade_age_sec}s ago</td>
                  <td style={td}>{n2(i.sell_at)}</td><td style={td}>{n2(i.buy_at)}</td>
                  <td style={{ ...td, display: 'flex', gap: '6px', 'justify-content': 'flex-end' }}>
                    <button class="tab-btn" disabled={busy()} style={{ background: '#b23b3b' }} onClick={() => order(i, 'SELL')}>Sell</button>
                    <button class="tab-btn" disabled={busy()} style={{ background: '#2e7d32' }} onClick={() => order(i, 'BUY')}>Buy</button>
                    <button class="tab-btn" disabled={busy() || st()?.round_trip_running} title="Sell, wait for the fill, then buy it back automatically" onClick={() => roundTrip(i)}>Round trip</button>
                  </td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
      </div>

      <div style={box}>
        <strong>Orders today (newest first)</strong>
        <Show when={(st()?.orders || []).length} fallback={<div style={muted}>no mock orders today</div>}>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Time</th><th style={{ ...th, ...left }}>Instrument</th><th style={th}>Side</th><th style={th}>Qty</th><th style={th}>Limit</th><th style={th}>Status</th><th style={th}>Filled</th><th style={th}>Avg</th><th style={{ ...th, ...left }}>Order / note</th><th style={th}></th>
            </tr></thead>
            <tbody>
              <For each={st()?.orders || []}>{(o) => (
                <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                  <td style={{ ...td, ...left }}>{o.time}</td>
                  <td style={{ ...td, ...left }}>{o.name}</td>
                  <td style={{ ...td, color: o.side === 'SELL' ? '#e66767' : '#5fd38d' }}>{o.side}</td>
                  <td style={td}>{o.qty}</td><td style={td}>{n2(o.limit)}</td>
                  <td style={td}><span style={{ padding: '1px 8px', 'border-radius': '8px', background: STATUS_BG[o.status] || '#455a64' }}>{o.status}</span></td>
                  <td style={td}>{o.filled_qty || 0}</td><td style={td}>{o.filled_qty ? n2(o.avg_price) : '—'}</td>
                  <td style={{ ...td, ...left, 'white-space': 'normal' }}>{o.broker_order_id}{o.note ? ` · ${o.note}` : ''}{o.error ? ` · ${o.error}` : ''}</td>
                  <td style={td}>
                    <Show when={!['FILLED', 'CANCELLED', 'REJECTED', 'SUBMIT_FAILED'].includes(o.status) && o.broker_order_id}>
                      <button class="tab-btn" disabled={busy()} onClick={() => cancel(o)}>Cancel</button>
                    </Show>
                  </td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
      </div>
    </div>
  );
}
