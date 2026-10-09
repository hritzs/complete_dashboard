import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// PORTFOLIO MTM SQUARE-OFF -- REAL ORDERS. Day MTM of the live account
// (today's realized + open trades valued through the depth, wings out).
//   >= book level (a profit, or a smaller loss like -10000): every open
//      trade closed lot by lot (IOC), day MTM never below the level.
//   <= portfolio SL: every open trade's normal Full Exit.
//   Square off ALL: emergency Full Exit of everything, now.
// Once per day; saving re-arms. Never blocks new entries.
// Backend: services/execution-gateway/internal/trading/portfolio_mtm.go

const STATUS_BG = {
  OFF: '#455a64', ARMED: '#2e7d32', PROFIT_EXITING: '#c98500', LOSS_EXITING: '#b23b3b',
  DONE_PROFIT: '#1b5e20', DONE_LOSS: '#7f1d1d',
};
const money = (v) => (Number.isFinite(Number(v)) ? `₹${Number(v).toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : '—');
const lvl = (v) => (v === null || v === undefined ? '∞ (off)' : money(v));
const n = (v, d = 2) => (Number.isFinite(Number(v)) ? Number(v).toLocaleString('en-IN', { minimumFractionDigits: d, maximumFractionDigits: d }) : '—');
const cls = (v) => (Number(v) >= 0 ? 'positive' : 'negative');
const th = { padding: '5px 8px', 'text-align': 'right', 'font-size': '11px', opacity: 0.7, 'font-weight': 600, 'white-space': 'nowrap' };
const td = { padding: '4px 8px', 'text-align': 'right', 'font-size': '12px', 'white-space': 'nowrap' };
const kpi = { display: 'flex', 'flex-direction': 'column', gap: '1px', 'min-width': '92px' };
const kl = { 'font-size': '10.5px', opacity: 0.6, 'text-transform': 'uppercase', 'letter-spacing': '0.03em' };

export default function PortfolioMTMPanel() {
  const [d, setD] = createSignal(null);
  const [profit, setProfit] = createSignal('');
  const [loss, setLoss] = createSignal('');
  const [dirty, setDirty] = createSignal(false);
  const [msg, setMsg] = createSignal('');

  const apply = (x) => {
    setD(x);
    if (!dirty() && x?.config) {
      setProfit(x.config.profit_level ?? '');
      setLoss(x.config.loss_level ?? '');
    }
  };
  const load = async () => {
    try { apply(await (await fetch('/api/portfolio/mtm-exit')).json()); } catch (e) { setMsg(String(e)); }
  };
  const num = (v) => (String(v).trim() === '' ? null : Number(v));

  const save = async () => {
    const p = num(profit()), l = num(loss());
    if ((p !== null && !Number.isFinite(p)) || (l !== null && !Number.isFinite(l))) { setMsg('levels must be numbers (blank = off)'); return; }
    const mtm = Number(d()?.view?.day_mtm);
    const now = [];
    if (p !== null && l !== null && l >= p) { setMsg('the portfolio SL must be below the book level'); return; }
    if (p !== null && mtm >= p) now.push(`day MTM ${money(mtm)} is already ≥ the book level — it closes every open trade lot by lot IMMEDIATELY`);
    if (l !== null && mtm <= l) now.push(`day MTM ${money(mtm)} is already ≤ the portfolio SL — Full Exit on every open trade IMMEDIATELY`);
    const text = `REAL ORDERS — portfolio MTM square-off on ${d()?.config?.broker_name}/${d()?.config?.account_id}\n\n`
      + `Book MTM at ≥ ${lvl(p)} → close every open trade lot by lot (IOC), day MTM never below it\n`
      + `Portfolio SL ≤ ${lvl(l)} → normal Full Exit on every open trade\n\n`
      + `Fires once today (saving re-arms). New trades are not blocked.`
      + (now.length ? `\n\n⚠ ${now.join('\n⚠ ')}` : '') + `\n\nSave?`;
    if ((p !== null || l !== null) && !window.confirm(text)) return;
    setMsg('');
    try {
      const x = await (await fetch('/api/portfolio/mtm-exit', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ profit_level: p, loss_level: l }) })).json();
      if (!x.success) { setMsg(x.error || 'failed'); return; }
      setDirty(false);
      apply(x);
      setMsg(p === null && l === null ? 'saved — portfolio square-off OFF' : 'saved — ARMED for today');
    } catch (e) { setMsg(String(e)); }
  };

  const disarm = async () => {
    const running = ['PROFIT_EXITING', 'LOSS_EXITING'].includes(st().status);
    if (running && !window.confirm('A portfolio square-off is running. Disarm stops it before its next lot (orders already sent finish). Open trades stay monitored by their own rules.\n\nDisarm?')) return;
    setMsg('');
    try {
      const x = await (await fetch('/api/portfolio/mtm-exit', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ disarm: true }) })).json();
      if (!x.success) { setMsg(x.error || 'failed'); return; }
      apply(x);
      setMsg('disarmed — levels kept; Save & arm to use them again');
    } catch (e) { setMsg(String(e)); }
  };

  const squareOffAll = async () => {
    const open = openTrades();
    if (!open.length) { setMsg('no open position to square off'); return; }
    const list = open.map((t) => `  ${t.trade_uid}  ${money(t.mtm)}`).join('\n');
    if (!window.confirm(`EMERGENCY — SQUARE OFF ALL (real orders, now)\n\nNormal Full Exit on every trade holding a position (${open.length}):\n${list}\n\nDay MTM ${money(view().day_mtm)}. Straddle Build rules still waiting to sell and the LUT build are NOT stopped.\n\nSquare off everything?`)) return;
    setMsg('');
    try {
      const x = await (await fetch('/api/portfolio/square-off-all', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ confirm: 'SQUARE OFF ALL' }) })).json();
      if (!x.success) { setMsg(x.error || 'failed'); return; }
      setMsg(`square off ALL sent: ${Object.entries(x.trades || {}).map(([u, r]) => `${u.slice(-10)} ${r}`).join(' · ') || 'nothing open'}`);
      load();
    } catch (e) { setMsg(String(e)); }
  };

  let timer;
  onMount(() => { load(); timer = setInterval(() => { if (!document.hidden) load(); }, 1000); });
  onCleanup(() => clearInterval(timer));

  const st = () => d()?.state || {};
  const view = () => d()?.view || {};
  const openTrades = () => (view().trades || []).filter((t) => t.open);
  const cfg = () => d()?.config || {};
  const bookLvl = () => cfg().profit_level;
  const slLvl = () => cfg().loss_level;
  const has = (v) => v !== null && v !== undefined;
  const exiting = () => ['PROFIT_EXITING', 'LOSS_EXITING'].includes(st().status);
  const done = () => ['DONE_PROFIT', 'DONE_LOSS'].includes(st().status);
  const closedQty = () => Math.max(0, Number(st().fired_open_qty || 0) - Number(view().open_qty || 0));
  const progress = () => (Number(st().fired_open_qty) > 0 ? Math.min(100, (closedQty() / Number(st().fired_open_qty)) * 100) : 0);
  // Where the day MTM sits between the SL and the book level (0..100%).
  const gauge = () => {
    const m = Number(view().day_mtm), lo = has(slLvl()) ? Number(slLvl()) : null, hi = has(bookLvl()) ? Number(bookLvl()) : null;
    if (lo === null || hi === null || hi <= lo) return null;
    return Math.max(0, Math.min(100, ((m - lo) / (hi - lo)) * 100));
  };
  const instr = (p) => `${p.symbol} ${p.expiry} ${n(p.strike, 0)} ${p.leg}`;

  return (
    <div style={{ margin: '0 0 14px', padding: '12px 14px', border: `1px solid ${exiting() ? '#c98500' : st().status === 'ARMED' ? 'rgba(46,125,50,0.6)' : 'rgba(255,255,255,0.10)'}`, 'border-radius': '10px', background: exiting() ? 'rgba(201,133,0,0.06)' : 'rgba(255,255,255,0.015)' }}>
      {/* header */}
      <div style={{ display: 'flex', gap: '12px', 'align-items': 'center', 'flex-wrap': 'wrap' }}>
        <strong style={{ 'font-size': '15px' }}>Portfolio — complete position</strong>
        <span style={{ padding: '1px 8px', 'border-radius': '8px', 'font-size': '12px', background: STATUS_BG[st().status] || '#455a64' }}>{st().status || '…'}</span>
        <span style={{ opacity: 0.6, 'font-size': '12px' }}>{cfg().broker_name}/{cfg().account_id} · live {view().at || '…'} · {openTrades().length} open trade(s) · {(view().trades || []).length} today</span>
        <button class="dashboard-btn red" style={{ 'margin-left': 'auto', 'font-weight': 700 }} onClick={squareOffAll}
          title="Emergency: normal Full Exit on every trade holding a position, now">Square off ALL</button>
      </div>

      {/* KPIs */}
      <div style={{ display: 'flex', gap: '18px', 'flex-wrap': 'wrap', 'margin-top': '10px', 'align-items': 'flex-end' }}>
        <div style={kpi}><span style={kl}>Day MTM (closeable now)</span><strong style={{ 'font-size': '20px' }} class={cls(view().day_mtm)}>{money(view().day_mtm)}</strong></div>
        <div style={kpi} title="open legs valued at LTP instead of the price they close at"><span style={kl}>At LTP</span><strong class={cls(view().day_mtm_ltp)}>{money(view().day_mtm_ltp)}</strong></div>
        <div style={kpi} title="LTP value minus what closing through the depth gives"><span style={kl}>Spread to close</span><strong>{money(Number(view().day_mtm_ltp) - Number(view().day_mtm))}</strong></div>
        <div style={kpi}><span style={kl}>Realized</span><strong class={cls(view().realized)}>{money(view().realized)}</strong></div>
        <div style={kpi}><span style={kl}>Open contracts</span><strong>{n(view().open_qty, 0)}</strong></div>
        <div style={kpi}><span style={kl}>Net Δ</span><strong class={cls(view().greeks?.delta)}>{n(view().greeks?.delta, 2)}</strong></div>
        <div style={kpi}><span style={kl}>Γ</span><strong>{n(view().greeks?.gamma, 4)}</strong></div>
        <div style={kpi}><span style={kl}>Θ / day</span><strong class={cls(view().greeks?.theta)}>{n(view().greeks?.theta, 0)}</strong></div>
        <div style={kpi}><span style={kl}>Vega</span><strong class={cls(view().greeks?.vega)}>{n(view().greeks?.vega, 0)}</strong></div>
        <Show when={Number(view().greeks?.wing_delta)}><div style={kpi} title="wings (RM) -- not in the totals"><span style={kl}>Wings Δ</span><strong>{n(view().greeks?.wing_delta, 2)}</strong></div></Show>
      </div>
      <Show when={view().priced === false}><div style={{ color: '#e6a067', 'font-size': '12px', 'margin-top': '4px' }}>An open leg has no price in the chain — the portfolio rule will not fire until it does.</div></Show>
      <Show when={view().error}><div style={{ 'font-size': '12px', color: '#e66767' }}>{view().error}</div></Show>

      {/* triggers */}
      <div style={{ display: 'flex', gap: '24px', 'flex-wrap': 'wrap', 'margin-top': '10px', 'font-size': '13px', 'align-items': 'center' }}>
        <span>Book at ≥ <strong>{lvl(bookLvl())}</strong>
          <Show when={has(bookLvl()) && !done()}>
            <span style={{ 'margin-left': '6px' }} class={Number(view().day_mtm) >= Number(bookLvl()) ? 'positive' : ''}>
              {Number(view().day_mtm) >= Number(bookLvl()) ? '— reached: cutting lot by lot' : `— needs +${money(Number(bookLvl()) - Number(view().day_mtm))} more`}
            </span>
          </Show>
        </span>
        <span>Portfolio SL ≤ <strong>{lvl(slLvl())}</strong>
          <Show when={has(slLvl()) && !done()}>
            <span style={{ 'margin-left': '6px' }} class={Number(view().day_mtm) <= Number(slLvl()) ? 'negative' : ''}>
              {Number(view().day_mtm) <= Number(slLvl()) ? '— hit: Full Exit' : `— room ${money(Number(view().day_mtm) - Number(slLvl()))}`}
            </span>
          </Show>
        </span>
        <Show when={gauge() !== null}>
          <span style={{ display: 'flex', 'align-items': 'center', gap: '6px', 'min-width': '240px' }} title="where the day MTM sits between the SL (left) and the book level (right)">
            <span style={{ 'font-size': '11px', opacity: 0.6 }}>SL</span>
            <span style={{ flex: 1, height: '6px', background: 'linear-gradient(90deg, #b23b3b, #455a64 50%, #2e7d32)', 'border-radius': '3px', position: 'relative' }}>
              <span style={{ position: 'absolute', top: '-4px', left: `calc(${gauge()}% - 2px)`, width: '4px', height: '14px', background: '#fff', 'border-radius': '2px' }} />
            </span>
            <span style={{ 'font-size': '11px', opacity: 0.6 }}>Book</span>
          </span>
        </Show>
      </div>

      {/* square-off progress */}
      <Show when={st().fired_at}>
        <div style={{ 'margin-top': '8px', padding: '8px 10px', 'border-radius': '8px', background: 'rgba(201,133,0,0.10)', 'font-size': '13px' }}>
          <strong>{st().status === 'LOSS_EXITING' || st().status === 'DONE_LOSS' ? 'Portfolio SL fired' : 'Book level fired'}</strong> at {st().fired_at} (day MTM {money(st().fired_mtm)})
          {st().done_at ? ` · done ${st().done_at}` : ''} · closed {n(closedQty(), 0)} of {n(st().fired_open_qty, 0)} contracts · {n(view().open_qty, 0)} still open
          <div style={{ height: '6px', background: 'rgba(255,255,255,0.08)', 'border-radius': '3px', 'margin-top': '5px' }}>
            <div style={{ height: '6px', width: `${progress()}%`, background: '#c98500', 'border-radius': '3px', transition: 'width 0.3s' }} />
          </div>
        </div>
      </Show>
      <Show when={st().note}><div style={{ 'font-size': '12px', 'margin-top': '4px', opacity: 0.8 }}>{st().note}</div></Show>

      {/* complete position, netted by instrument across every trade */}
      <Show when={(view().positions || []).length}>
        <div style={{ 'overflow-x': 'auto', 'margin-top': '10px' }}>
          <table style={{ width: '100%', 'border-collapse': 'collapse' }}>
            <thead>
              <tr style={{ 'border-bottom': '1px solid rgba(255,255,255,0.10)' }}>
                <th style={{ ...th, 'text-align': 'left' }}>Instrument</th>
                <th style={th}>Net qty</th><th style={th}>Avg</th><th style={th}>LTP</th><th style={th}>Bid / Ask</th>
                <th style={th} title="VWAP to close the whole quantity through L1-L5 (short: asks, long: bids), and the deepest level it reaches">Closes at</th>
                <th style={th}>MTM</th><th style={th}>MTM @LTP</th>
                <th style={th}>Δ</th><th style={th}>Γ</th><th style={th}>Θ</th><th style={th}>Vega</th><th style={th}>IV</th>
                <th style={{ ...th, 'text-align': 'left' }}>Trades</th>
              </tr>
            </thead>
            <tbody>
              <For each={view().positions}>{(p) => (
                <tr style={{ 'border-bottom': '1px solid rgba(255,255,255,0.04)', opacity: p.wing ? 0.6 : 1 }}>
                  <td style={{ ...td, 'text-align': 'left' }}>{instr(p)}{p.wing ? ' · WING (RM)' : ''}</td>
                  <td style={td} class={p.net_qty < 0 ? 'negative' : 'positive'}>{p.net_qty > 0 ? '+' : ''}{n(p.net_qty, 0)}</td>
                  <td style={td}>{n(p.avg_price)}</td>
                  <td style={td}>{n(p.ltp)}</td>
                  <td style={td}>{n(p.bid)} / {n(p.ask)}</td>
                  <td style={td}>{n(p.close_px)}<span style={{ opacity: 0.55 }}> →{n(p.close_to)}</span></td>
                  <td style={td} class={cls(p.mtm)}>{money(p.mtm)}</td>
                  <td style={td} class={cls(p.mtm_ltp)}>{money(p.mtm_ltp)}</td>
                  <td style={td}>{n(p.delta, 2)}</td>
                  <td style={td}>{n(p.gamma, 4)}</td>
                  <td style={td}>{n(p.theta, 0)}</td>
                  <td style={td}>{n(p.vega, 0)}</td>
                  <td style={td}>{p.iv ? `${n(p.iv * (p.iv < 3 ? 100 : 1), 1)}%` : '—'}</td>
                  <td style={{ ...td, 'text-align': 'left', opacity: 0.6, 'font-size': '11px' }}>{(p.trades || []).map((u) => u.slice(-8)).join(', ')}</td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </div>
      </Show>
      <Show when={openTrades().length}>
        <div style={{ 'font-size': '11px', opacity: 0.7, 'margin-top': '6px' }}>
          By trade: <For each={openTrades()}>{(t) => <span style={{ 'margin-right': '12px' }}>{t.trade_uid.slice(-8)} <span class={cls(t.mtm)}>{money(t.mtm)}</span></span>}</For>
        </div>
      </Show>

      {/* settings */}
      <div style={{ display: 'flex', gap: '10px', 'align-items': 'flex-end', 'flex-wrap': 'wrap', 'margin-top': '10px', 'padding-top': '8px', 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
        <label class="control-block">
          <span class="control-label">Book MTM at ≥ ₹ (profit or smaller loss, e.g. 10000 / -10000; blank = ∞) — IOC lot by lot, never below</span>
          <input class="symbol-select" style={{ width: '170px' }} type="text" inputmode="decimal" step="any" value={profit()} placeholder="∞"
            onInput={(e) => { setProfit(e.currentTarget.value); setDirty(true); }} />
        </label>
        <label class="control-block">
          <span class="control-label">Portfolio SL ≤ ₹ (e.g. -20000; blank = off) — Full Exit</span>
          <input class="symbol-select" style={{ width: '170px' }} type="text" inputmode="decimal" step="any" value={loss()} placeholder="off"
            onInput={(e) => { setLoss(e.currentTarget.value); setDirty(true); }} />
        </label>
        <button class="tab-btn" onClick={save}>Save &amp; arm</button>
        <Show when={st().status && st().status !== 'OFF' && !done()}>
          <button class="tab-btn" style={{ border: '1px solid #e66767' }} onClick={disarm}>Disarm</button>
        </Show>
        <span style={{ 'font-size': '12px', opacity: 0.8 }}>{msg()}</span>
      </div>
    </div>
  );
}
