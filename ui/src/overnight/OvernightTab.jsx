import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// OVERNIGHT -- positional NIFTY monthly: CE / PE picked by delta, BOUGHT
// over a set time, carried for days, then exited / partly exited / rolled.
// Every number here is GreekSoft's: minute ends are 1-minute candle closes,
// live values are the Apollo websocket ticks.
// Backend: services/execution-gateway/internal/overnight (self-contained).
// This folder has no imports from the rest of the UI, so it moves as is.

const API = '/api/overnight';
const box = { border: '1px solid rgba(255,255,255,0.10)', 'border-radius': '8px', padding: '10px 12px', margin: '10px 0' };
const head = { 'font-weight': 600, 'margin-bottom': '8px' };
const muted = { opacity: 0.65, 'font-size': '12px' };
const th = { 'text-align': 'right', padding: '6px 8px', 'font-size': '11px', opacity: 0.7, 'white-space': 'nowrap' };
const td = { 'text-align': 'right', padding: '5px 8px', 'font-size': '13px', 'white-space': 'nowrap' };
const left = { 'text-align': 'left' };
const inp = { width: '90px', padding: '4px 6px', background: 'rgba(255,255,255,0.06)', color: 'inherit', border: '1px solid rgba(255,255,255,0.18)', 'border-radius': '4px' };
const btn = { padding: '5px 12px', 'border-radius': '4px', border: '1px solid rgba(255,255,255,0.25)', background: 'rgba(255,255,255,0.08)', color: 'inherit', cursor: 'pointer' };
const row = { display: 'flex', gap: '10px', 'align-items': 'center', 'flex-wrap': 'wrap' };
const STATUS_BG = { FILLED: '#2e7d32', DONE: '#2e7d32', OPEN: '#c98500', RUNNING: '#c98500', SCHEDULED: '#1565c0', NEW: '#546e7a', REJECTED: '#b23b3b', FAILED: '#b23b3b', CANCELLED: '#546e7a', STOPPED: '#546e7a' };

const n2 = (v) => (v == null || v === 0 ? '—' : Number(v).toFixed(2));
const n4 = (v) => (v == null || v === 0 ? '—' : Number(v).toFixed(4));
const money = (v) => (v == null ? '—' : (v < 0 ? '−' : '') + '₹' + Math.abs(Number(v)).toLocaleString('en-IN', { maximumFractionDigits: 2 }));
const pnlColor = (v) => ({ color: v > 0 ? '#4caf50' : v < 0 ? '#ef5350' : 'inherit' });
const srcMark = (s) => (s === 'ws' ? ' ᵗ' : s === 'old' ? ' ⚠' : '');
const srcText = (s) => (s === 'ws' ? 'candle not fresh: GreekSoft websocket tick used' : s === 'old' ? 'stale candle, no live tick' : 'GreekSoft 1-minute candle close');
const badge = (s) => ({ background: STATUS_BG[s] || '#546e7a', color: '#fff', padding: '1px 7px', 'border-radius': '10px', 'font-size': '11px' });

const avg = (value, qty) => (qty > 0 ? value / qty : 0);

// PnL of one position, end to end: every strike it ever held (current and
// the ones rolled out of or exited), then what was executed day by day.
function PositionPnL(props) {
  const p = () => props.pos;
  const unreal = (l) => (props.liveLeg && l.qty > 0 ? props.liveLeg(l.token)?.pnl ?? null : l.qty > 0 ? null : 0);
  const totals = () => {
    let realized = 0, u = 0, known = true;
    for (const l of p().legs || []) {
      realized += l.realized || 0;
      const x = unreal(l);
      if (x == null) known = false; else u += x;
    }
    return { realized, unrealized: known ? u : null, total: known ? realized + u : null };
  };
  return (
    <div>
      <table style={{ width: '100%', 'border-collapse': 'collapse' }}>
        <thead><tr>
          <th style={{ ...th, ...left }}>Strike</th><th style={{ ...th, ...left }}>State</th><th style={th}>Bought</th><th style={th}>Avg buy</th>
          <th style={th}>Sold</th><th style={th}>Avg sell</th><th style={th}>Held</th><th style={th}>Realized</th><th style={th}>Unrealized</th><th style={th}>Total</th>
        </tr></thead>
        <tbody>
          <For each={p().legs || []}>{(l) => (
            <tr style={{ opacity: l.qty > 0 ? 1 : 0.75 }}>
              <td style={{ ...td, ...left }}>{l.strike} {l.opt}</td>
              <td style={{ ...td, ...left }}>{l.qty > 0 ? 'held' : 'previous (closed)'}</td>
              <td style={td}>{l.bought}</td><td style={td}>{n2(avg(l.buy_value, l.bought))}</td>
              <td style={td}>{l.sold}</td><td style={td}>{n2(avg(l.sell_value, l.sold))}</td>
              <td style={td}>{l.qty}</td>
              <td style={{ ...td, ...pnlColor(l.realized) }}>{money(l.realized)}</td>
              <td style={{ ...td, ...pnlColor(unreal(l)) }}>{l.qty > 0 ? money(unreal(l)) : '—'}</td>
              <td style={{ ...td, ...pnlColor(unreal(l) == null ? 0 : l.realized + unreal(l)) }}>{unreal(l) == null ? '—' : money(l.realized + unreal(l))}</td>
            </tr>
          )}</For>
          <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.2)', 'font-weight': 600 }}>
            <td style={{ ...td, ...left }} colSpan={7}>Total — all strikes</td>
            <td style={{ ...td, ...pnlColor(totals().realized) }}>{money(totals().realized)}</td>
            <td style={{ ...td, ...pnlColor(totals().unrealized) }}>{money(totals().unrealized)}</td>
            <td style={{ ...td, ...pnlColor(totals().total) }}>{money(totals().total)}</td>
          </tr>
        </tbody>
      </table>
      <Show when={(p().acts || []).length}>
        <div style={{ ...muted, 'margin-top': '10px' }}>Executed each day (Sold = quantity actually squared off that day, by exit, partial exit or roll)</div>
        <table style={{ 'border-collapse': 'collapse' }}>
          <thead><tr>
            <th style={{ ...th, ...left }}>Day</th><th style={{ ...th, ...left }}>Strike</th><th style={th}>Bought</th><th style={th}>Avg buy</th>
            <th style={th}>Sold</th><th style={th}>Avg sell</th><th style={th}>Realized that day</th>
          </tr></thead>
          <tbody>
            <For each={p().acts}>{(a) => (
              <tr>
                <td style={{ ...td, ...left }}>{a.day}</td><td style={{ ...td, ...left }}>{a.strike} {a.opt}</td>
                <td style={td}>{a.bought || '—'}</td><td style={td}>{n2(avg(a.buy_value, a.bought))}</td>
                <td style={td}>{a.sold || '—'}</td><td style={td}>{n2(avg(a.sell_value, a.sold))}</td>
                <td style={{ ...td, ...pnlColor(a.realized) }}>{a.sold ? money(a.realized) : '—'}</td>
              </tr>
            )}</For>
          </tbody>
        </table>
      </Show>
      <Show when={(p().days || []).length}>
        <div style={{ ...muted, 'margin-top': '10px' }}>Day-end marks (a day's PnL is measured from the previous trading day held, so holidays and weekends are skipped)</div>
        <table style={{ 'border-collapse': 'collapse' }}>
          <thead><tr>
            <th style={{ ...th, ...left }}>Day</th><th style={th}>Marked at</th><th style={th}>Qty held</th><th style={th}>Future</th>
            <th style={{ ...th, ...left }}>Closes</th><th style={th}>Day PnL</th><th style={th}>Total PnL</th>
          </tr></thead>
          <tbody>
            <For each={[...p().days].reverse()}>{(d) => (
              <tr>
                <td style={{ ...td, ...left }}>{d.day}</td><td style={td}>{d.minute}</td><td style={td}>{d.qty}</td><td style={td}>{n2(d.fut)}</td>
                <td style={{ ...td, ...left }}>{Object.entries(d.closes || {}).map(([k, v]) => `${k} ${Number(v).toFixed(2)}`).join(' · ')}</td>
                <td style={{ ...td, ...pnlColor(d.day_pnl) }}>{money(d.day_pnl)}</td>
                <td style={{ ...td, ...pnlColor(d.cum) }}>{money(d.cum)}</td>
              </tr>
            )}</For>
          </tbody>
        </table>
      </Show>
    </div>
  );
}

export default function OvernightTab() {
  const [st, setSt] = createSignal(null);
  const [msg, setMsg] = createSignal('');
  const [busy, setBusy] = createSignal(false);
  const [delta, setDelta] = createSignal('');
  const [qty, setQty] = createSignal('');
  const [minutes, setMinutes] = createSignal('10');
  const [entryAt, setEntryAt] = createSignal('');
  const [exitAt, setExitAt] = createSignal('');
  const [exitMinutes, setExitMinutes] = createSignal('');
  const [partQty, setPartQty] = createSignal('');
  const [partAt, setPartAt] = createSignal('');
  const [rollAt, setRollAt] = createSignal('');
  const [showRows, setShowRows] = createSignal(false);

  const load = async () => {
    try {
      const r = await fetch(API);
      if (r.ok) setSt(await r.json());
    } catch (e) { setMsg(String(e)); }
  };
  const post = async (body) => {
    setBusy(true);
    setMsg('');
    try {
      const r = await fetch(API, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      const j = await r.json();
      if (!r.ok) setMsg(j.error || 'failed');
      else { setSt(j); setMsg(j.message === 'ok' ? '' : j.message); }
    } catch (e) { setMsg(String(e)); }
    setBusy(false);
  };

  let timer;
  onMount(() => { load(); timer = setInterval(load, 1500); });
  onCleanup(() => clearInterval(timer));

  const s = () => st() || {};
  const pos = () => s().position;
  const live = () => s().live;
  const last = () => s().last;
  const picks = () => (last() && last().picks ? last().picks[s().delta_key] : null);
  const lot = () => (pos() && pos().lot_size) || s().lot_size || 0;
  const whole = (v) => { const q = parseInt(v || '0', 10) || 0; const l = lot(); return l > 0 ? q - (q % l) : q; };
  const isLive = () => (pos() ? pos().mode : s().mode) === 'LIVE';
  const when = (t) => (t ? `at ${t}` : 'now');

  const confirmLive = (text) => !isLive() || window.confirm(`${text}\n\nThis sends REAL orders to the broker.`);
  const setMode = (m) => {
    if (m === 'LIVE') {
      const c = window.prompt('Type LIVE to send real broker orders from this tab');
      if (c == null) return;
      post({ action: 'config', mode: 'LIVE', confirm: c });
    } else post({ action: 'config', mode: 'PAPER' });
  };

  const liveLeg = (token) => (live() && live().legs ? live().legs.find((l) => l.token === token && l.label === 'HELD') : null);
  const liveRow = (label, opt) => (live() && live().legs ? live().legs.find((l) => l.label.startsWith(label) && l.opt === opt) : null);

  return (
    <div style={{ padding: '4px 2px' }}>
      <div style={{ ...row, 'justify-content': 'space-between' }}>
        <div>
          <b>Overnight</b> <span style={muted}>
            {s().symbol} monthly {s().expiry} · {n2(s().days_to_expiry)} days to expiry · lot {s().lot_size || '—'} · max per order {s().max_order_qty || '—'}
          </span>
        </div>
        <div style={row}>
          <span style={badge(isLive() ? 'REJECTED' : 'SCHEDULED')}>{isLive() ? 'LIVE — real orders' : 'PAPER — no orders sent'}</span>
          <Show when={!pos() && !s().job}>
            <button style={btn} disabled={busy()} onClick={() => setMode(s().mode === 'LIVE' ? 'PAPER' : 'LIVE')}>
              Switch to {s().mode === 'LIVE' ? 'PAPER' : 'LIVE'}
            </button>
          </Show>
        </div>
      </div>
      <Show when={msg()}><div style={{ ...box, 'border-color': '#c98500' }}>{msg()}</div></Show>
      <Show when={s().chain_error}><div style={{ ...box, 'border-color': '#b23b3b' }}>Contract file: {s().chain_error}</div></Show>
      <Show when={s().feed_error}><div style={{ ...box, 'border-color': '#b23b3b' }}>GreekSoft live ticks: {s().feed_error}</div></Show>
      <Show when={s().broker_error}><div style={{ ...box, 'border-color': '#b23b3b' }}>Broker positions: {s().broker_error}</div></Show>

      {/* ───────── 1. net position, PnL, live scenarios ───────── */}
      <div style={box}>
        <div style={head}>1 · Net position, PnL and live scenarios</div>
        <div style={{ ...row, 'margin-bottom': '8px' }}>
          <span>Delta</span>
          <input style={inp} placeholder={String(s().delta ?? 0.25)} value={delta()} onInput={(e) => setDelta(e.target.value)} />
          <button style={btn} disabled={busy() || !delta()} onClick={() => post({ action: 'config', delta: parseFloat(delta()) })}>Follow this delta</button>
          <button style={btn} disabled={busy()} onClick={() => post({ action: 'refresh' })}>Recompute last minute</button>
          <span style={muted}>following {n4(s().delta)} · last minute end {last() ? `${last().day} ${last().minute}` : '—'} · live {live() ? live().at : '—'}</span>
        </div>
        <table style={{ width: '100%', 'border-collapse': 'collapse' }}>
          <thead><tr>
            <th style={{ ...th, ...left }}></th><th style={th}>Spot</th><th style={th}>Future</th>
            <th style={th}>ATM strike</th><th style={th}>ATM straddle</th>
            <th style={th}>CE strike</th><th style={th}>CE delta</th><th style={th}>CE price</th>
            <th style={th}>PE strike</th><th style={th}>PE delta</th><th style={th}>PE price</th>
          </tr></thead>
          <tbody>
            <tr>
              <td style={{ ...td, ...left }}>Minute end {last() ? last().minute : ''}</td>
              <td style={td}>{n2(last()?.spot)}</td><td style={td}>{n2(last()?.fut)}</td>
              <td style={td}>{last()?.atm || '—'}</td><td style={td}>{n2(last()?.straddle)}</td>
              <td style={td}><b>{picks()?.ce?.strike || '—'}</b></td><td style={td}>{n4(picks()?.ce?.delta)}</td><td style={td} title={srcText(picks()?.ce?.src)}>{n2(picks()?.ce?.price)}{srcMark(picks()?.ce?.src)}</td>
              <td style={td}><b>{picks()?.pe?.strike || '—'}</b></td><td style={td}>{n4(picks()?.pe?.delta)}</td><td style={td} title={srcText(picks()?.pe?.src)}>{n2(picks()?.pe?.price)}{srcMark(picks()?.pe?.src)}</td>
            </tr>
            <tr>
              <td style={{ ...td, ...left }}>Live {live() ? live().at : ''}</td>
              <td style={td}>{n2(live()?.spot)}</td><td style={td}>{n2(live()?.fut)}</td>
              <td style={td}>{live()?.atm || '—'}</td><td style={td}>{n2(live()?.straddle)}</td>
              <td style={td}>{liveRow('DELTA', 'CE')?.strike || '—'}</td><td style={td}>{n4(liveRow('DELTA', 'CE')?.delta)}</td><td style={td}>{n2(liveRow('DELTA', 'CE')?.ltp)}</td>
              <td style={td}>{liveRow('DELTA', 'PE')?.strike || '—'}</td><td style={td}>{n4(liveRow('DELTA', 'PE')?.delta)}</td><td style={td}>{n2(liveRow('DELTA', 'PE')?.ltp)}</td>
            </tr>
          </tbody>
        </table>

        <div style={{ ...muted, 'margin-top': '4px' }}>
          Underlying is the monthly future itself. Minute end: strikes fixed from that minute's closes (ᵗ = live tick used because the candle was not fresh).
          Live: strikes re-picked every second from live prices, so they can differ from the minute-end row. Entry and roll use the minute-end row.
        </div>
        <Show when={pos()} fallback={<div style={{ ...muted, 'margin-top': '10px' }}>No overnight position.</div>}>
          <div style={{ ...row, 'margin-top': '12px' }}>
            <b>Position {pos().id}</b>
            <span style={badge(pos().mode === 'LIVE' ? 'REJECTED' : 'SCHEDULED')}>{pos().mode}</span>
            <span style={muted}>delta {n4(pos().delta)} · opened {pos().opened_at} · rolls {pos().rolls} · broker userTag <code>{pos().user_tag}</code></span>
          </div>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '4px' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Strike</th><th style={th}>Qty held</th><th style={th}>Broker qty</th><th style={th}>Avg buy</th>
              <th style={th}>Live price</th><th style={th}>Bid</th><th style={th}>Ask</th><th style={th}>Live delta</th>
              <th style={th}>Bought</th><th style={th}>Sold</th><th style={th}>Realized</th><th style={th}>Unrealized</th>
            </tr></thead>
            <tbody>
              <For each={pos().legs}>{(l) => {
                const q = () => liveLeg(l.token);
                return (
                  <tr style={{ opacity: l.qty > 0 ? 1 : 0.5 }}>
                    <td style={{ ...td, ...left }}>{l.strike} {l.opt}</td>
                    <td style={td}><b>{l.qty}</b></td>
                    <td style={{ ...td, color: l.broker_qty != null && l.broker_qty !== l.qty ? '#ef5350' : 'inherit' }}>{l.broker_qty ?? '—'}</td>
                    <td style={td}>{n2(l.avg)}</td>
                    <td style={td}>{n2(q()?.ltp)}</td><td style={td}>{n2(q()?.bid)}</td><td style={td}>{n2(q()?.ask)}</td><td style={td}>{n4(q()?.delta)}</td>
                    <td style={td}>{l.bought}</td><td style={td}>{l.sold}</td>
                    <td style={{ ...td, ...pnlColor(l.realized) }}>{money(l.realized)}</td>
                    <td style={{ ...td, ...pnlColor(q()?.pnl) }}>{l.qty > 0 ? money(q()?.pnl) : '—'}</td>
                  </tr>
                );
              }}</For>
            </tbody>
          </table>
          <div style={{ ...row, 'margin-top': '8px', gap: '22px' }}>
            <span>Total PnL <b style={pnlColor(live()?.pnl)}>{money(live()?.pnl)}</b></span>
            <span>Today <b style={pnlColor(live()?.day_pnl)}>{money(live()?.day_pnl)}</b></span>
            <span>Realized <b style={pnlColor(live()?.realized)}>{money(live()?.realized)}</b></span>
            <span>Unrealized <b style={pnlColor(live()?.unrealized)}>{money(live()?.unrealized)}</b></span>
            <Show when={live() && !live().priced}><span style={{ color: '#c98500' }}>a held strike has no live tick yet</span></Show>
          </div>
        </Show>

        <div style={{ 'margin-top': '10px' }}>
          <button style={btn} onClick={() => setShowRows(!showRows())}>{showRows() ? 'Hide' : 'Show'} minute-end history</button>
        </div>
        <Show when={showRows()}>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Minute end</th><th style={th}>Spot</th><th style={th}>Future</th><th style={th}>ATM</th><th style={th}>Straddle</th>
              <th style={th}>CE strike</th><th style={th}>CE delta</th><th style={th}>CE close</th>
              <th style={th}>PE strike</th><th style={th}>PE delta</th><th style={th}>PE close</th><th style={th}>Position PnL</th>
            </tr></thead>
            <tbody>
              <For each={s().rows || []}>{(r) => {
                const p = r.picks ? r.picks[s().delta_key] : null;
                return (
                  <tr>
                    <td style={{ ...td, ...left }}>{r.minute}</td><td style={td}>{n2(r.spot)}</td><td style={td}>{n2(r.fut)}</td>
                    <td style={td}>{r.atm}</td><td style={td}>{n2(r.straddle)}</td>
                    <td style={td}>{p?.ce?.strike || '—'}</td><td style={td}>{n4(p?.ce?.delta)}</td><td style={td}>{n2(p?.ce?.price)}</td>
                    <td style={td}>{p?.pe?.strike || '—'}</td><td style={td}>{n4(p?.pe?.delta)}</td><td style={td}>{n2(p?.pe?.price)}</td>
                    <td style={{ ...td, ...pnlColor(r.pnl) }}>{r.held ? money(r.pnl) : '—'}</td>
                  </tr>
                );
              }}</For>
            </tbody>
          </table>
        </Show>
      </div>

      {/* ───────── running job ───────── */}
      <Show when={s().job}>
        <div style={{ ...box, 'border-color': '#c98500' }}>
          <div style={{ ...row, 'justify-content': 'space-between' }}>
            <div>
              <b>{s().job.kind}</b> <span style={badge(s().job.status)}>{s().job.status}</span>{' '}
              <span style={muted}>{s().job.id} {s().job.at ? `· starts ${s().job.at}` : ''} {s().job.minute ? `· strikes from the ${s().job.minute} minute end` : ''} · chunk {s().job.chunk}</span>
            </div>
            <button style={{ ...btn, 'border-color': '#b23b3b' }} disabled={busy()} onClick={() => post({ action: 'stop' })}>Stop</button>
          </div>
          <For each={s().job.plan || []}>{(p) => (
            <div style={{ 'font-size': '13px', 'margin-top': '4px' }}>{p.side} {p.strike} {p.opt}: <b>{p.done}</b> / {p.target}</div>
          )}</For>
        </div>
      </Show>

      {/* ───────── 2. entry ───────── */}
      <div style={box}>
        <div style={head}>2 · Entry (buy)</div>
        <div style={row}>
          <span>Delta</span>
          <input style={inp} disabled={!!pos()} placeholder={String(pos() ? pos().delta : s().delta ?? 0.25)} value={pos() ? '' : delta()} onInput={(e) => setDelta(e.target.value)} />
          <span>Qty per leg</span>
          <input style={inp} placeholder="e.g. 1000" value={qty()} onInput={(e) => setQty(e.target.value)} />
          <span>Build over (minutes)</span>
          <input style={inp} value={minutes()} onInput={(e) => setMinutes(e.target.value)} />
          <span>Start at</span>
          <input style={inp} type="time" value={entryAt()} onInput={(e) => setEntryAt(e.target.value)} />
          <button style={btn} disabled={busy() || !!s().job || whole(qty()) <= 0}
            onClick={() => confirmLive(`BUY ${whole(qty())} CE + ${whole(qty())} PE ${when(entryAt())}`) &&
              post({ action: 'entry', delta: parseFloat(delta() || s().delta), qty: whole(qty()), minutes: parseInt(minutes() || '0', 10) || 0, at: entryAt() })}>
            {entryAt() ? `Build at ${entryAt()}` : 'Build now'}
          </button>
        </div>
        <div style={{ ...muted, 'margin-top': '6px' }}>
          <Show when={whole(qty()) > 0}>
            Buys {whole(qty())} of each leg{parseInt(qty() || '0', 10) !== whole(qty()) ? ` (rounded down to whole lots of ${lot()})` : ''}
            {(parseInt(minutes() || '0', 10) || 0) > 0
              ? ` — in equal slices of whole lots, finishing on the clock minute (pressed at 13:05:20 with 5 minutes it is done by 13:10:00). Anything a slice leaves unfilled goes with the next slice.`
              : ' — as fast as it fills, the largest order size per chunk.'}{' '}
          </Show>
          <Show when={pos()} fallback={<>Strikes are fixed from the last finished minute's closes when the build starts: CE {picks()?.ce?.strike || '—'}, PE {picks()?.pe?.strike || '—'} as of {last()?.minute || '—'}.</>}>
            Adds to the held strikes (the strikes and delta of the entry stay fixed).
          </Show>
        </div>
      </div>

      {/* ───────── 3. exit ───────── */}
      <div style={box}>
        <div style={head}>3 · Exit (sell everything)</div>
        <div style={row}>
          <span>Exit over (minutes)</span>
          <input style={inp} placeholder="0 = at once" value={exitMinutes()} onInput={(e) => setExitMinutes(e.target.value)} />
          <span>Start at</span>
          <input style={inp} type="time" value={exitAt()} onInput={(e) => setExitAt(e.target.value)} />
          <button style={btn} disabled={busy() || !!s().job || !pos()}
            onClick={() => confirmLive(`SELL the whole position ${when(exitAt())}`) && post({ action: 'exit', at: exitAt(), minutes: parseInt(exitMinutes() || '0', 10) || 0 })}>
            {exitAt() ? `Exit at ${exitAt()}` : 'Exit now'}
          </button>
        </div>
        <div style={{ ...muted, 'margin-top': '6px' }}>
          {(parseInt(exitMinutes() || '0', 10) || 0) > 0
            ? `Sells both legs in equal slices of whole lots, finishing on the clock minute ${exitMinutes()} minute(s) ahead. Anything a slice leaves unfilled goes with the next slice.`
            : 'Both legs together, the largest order size per chunk; the next chunk only after both are filled.'}
        </div>
      </div>

      {/* ───────── 4. partial exit ───────── */}
      <div style={box}>
        <div style={head}>4 · Partial exit</div>
        <div style={row}>
          <span>Qty per leg</span>
          <input style={inp} placeholder="e.g. 325" value={partQty()} onInput={(e) => setPartQty(e.target.value)} />
          <span>Start at</span>
          <input style={inp} type="time" value={partAt()} onInput={(e) => setPartAt(e.target.value)} />
          <button style={btn} disabled={busy() || !!s().job || !pos() || whole(partQty()) <= 0}
            onClick={() => confirmLive(`SELL ${whole(partQty())} of each leg ${when(partAt())}`) && post({ action: 'partial', qty: whole(partQty()), at: partAt() })}>
            {partAt() ? `Partial exit at ${partAt()}` : 'Partial exit now'}
          </button>
          <Show when={whole(partQty()) > 0}><span style={muted}>Sells {whole(partQty())} of each held strike (CE and PE together). In LIVE the broker's net position is read before and after, and nothing is sold if the broker holds less.</span></Show>
        </div>
      </div>

      {/* ───────── 5. roll ───────── */}
      <div style={box}>
        <div style={head}>5 · Roll</div>
        <div style={row}>
          <span>Start at</span>
          <input style={inp} type="time" value={rollAt()} onInput={(e) => setRollAt(e.target.value)} />
          <button style={btn} disabled={busy() || !!s().job || !pos()}
            onClick={() => confirmLive(`ROLL to the current ${pos().delta}-delta strikes ${when(rollAt())}`) && post({ action: 'roll', at: rollAt() })}>
            {rollAt() ? `Roll at ${rollAt()}` : 'Roll now'}
          </button>
        </div>
        <div style={{ ...muted, 'margin-top': '6px' }}>
          Sells the held strikes and buys the {pos() ? n4(pos().delta) : n4(s().delta)}-delta strikes of the last finished minute, same quantity, delta unchanged.
          Each chunk sends the sell and the buy of both sides together ({s().max_order_qty || 'max'} per order, or what is left), waits until all are filled
          (re-pricing the unfilled ones every 2 s) and only then sends the next chunk. A side already at the right strike is left alone.
        </div>
      </div>

      {/* ───────── PnL ───────── */}
      <div style={box}>
        <div style={head}>PnL — all strikes, current and previous</div>
        <Show when={pos()} fallback={<div style={muted}>No open position.</div>}>
          <div style={{ ...muted, 'margin-bottom': '4px' }}>Open position {pos().id} · {pos().mode} · delta {n4(pos().delta)} · opened {pos().opened_at} · rolls {pos().rolls}</div>
          <PositionPnL pos={pos()} liveLeg={liveLeg} />
        </Show>
        <For each={s().history || []}>{(h) => (
          <details style={{ 'margin-top': '8px' }}>
            <summary style={{ cursor: 'pointer', 'font-size': '13px' }}>
              Closed {h.id} · {h.mode} · {h.expiry} · delta {n4(h.delta)} · {h.opened_at} → {h.closed_at} · rolls {h.rolls} · <b style={pnlColor(h.realized)}>{money(h.realized)}</b>
            </summary>
            <div style={{ 'margin-top': '6px' }}><PositionPnL pos={h} /></div>
          </details>
        )}</For>
      </div>

      {/* ───────── orders (dropdown) ───────── */}
      <div style={box}>
        <details>
          <summary style={{ cursor: 'pointer', 'font-weight': 600 }}>
            Orders sent by this tab ({(s().orders || []).length}) <span style={muted}>— click to open; the broker stores "tag" (in its userTag column)</span>
          </summary>
          <div style={{ 'margin-top': '8px' }}>
        <Show when={(s().orders || []).length} fallback={<div style={muted}>None yet.</div>}>
          <table style={{ width: '100%', 'border-collapse': 'collapse' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Time</th><th style={{ ...th, ...left }}>Side</th><th style={{ ...th, ...left }}>Strike</th><th style={th}>Qty</th><th style={th}>Filled</th>
              <th style={th}>Limit</th><th style={th}>Avg fill</th><th style={th}>Modifies</th><th style={th}>Status</th>
              <th style={{ ...th, ...left }}>Order id</th><th style={{ ...th, ...left }}>tag</th><th style={{ ...th, ...left }}>userTag</th>
            </tr></thead>
            <tbody>
              <For each={s().orders}>{(o) => (
                <tr>
                  <td style={{ ...td, ...left }}>{o.time}</td><td style={{ ...td, ...left }}>{o.side}</td><td style={{ ...td, ...left }}>{o.strike} {o.opt}</td>
                  <td style={td}>{o.qty}</td><td style={td}>{o.filled}</td><td style={td}>{n2(o.price)}</td><td style={td}>{n2(o.avg_fill)}</td><td style={td}>{o.modifies}</td>
                  <td style={td} title={o.reason || ''}><span style={badge(o.status)}>{o.mode === 'PAPER' ? 'PAPER ' : ''}{o.status}</span></td>
                  <td style={{ ...td, ...left }}>{o.order_id}</td><td style={{ ...td, ...left }}><code>{o.tag}</code></td><td style={{ ...td, ...left }}><code>{o.user_tag}</code></td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
          </div>
        </details>
      </div>

      {/* ───────── jobs ───────── */}
      <div style={box}>
        <div style={head}>Recent jobs</div>
        <For each={s().jobs || []} fallback={<div style={muted}>None yet.</div>}>{(j) => (
          <details style={{ 'margin-bottom': '4px' }}>
            <summary style={{ cursor: 'pointer', 'font-size': '13px' }}>
              {j.created_at} · <b>{j.kind}</b> <span style={badge(j.status)}>{j.status}</span> <span style={muted}>{j.mode} · {j.id} {j.note ? `· ${j.note}` : ''}</span>
            </summary>
            <pre style={{ 'font-size': '12px', 'white-space': 'pre-wrap', margin: '4px 0 0 14px', opacity: 0.85 }}>{(j.log || []).join('\n')}</pre>
          </details>
        )}</For>
      </div>

      <div style={muted}>State and minute-end files: {s().data_dir}</div>
    </div>
  );
}
