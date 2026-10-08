import { createSignal, createRoot, Show, For, Index } from 'solid-js';

// Straddle-build positions as NORMAL rows of the Portfolio table (same
// columns as every other trade); click a row for its details, legs and
// Pause / Resume / Stop building / Square off. Data: /api/sbuild/shadow/state
// and the "sbuild_update" push (one shared store for the whole app).

const fmt = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? n.toFixed(d) : '—';
};
const sgn = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? `${n >= 0 ? '+' : ''}${n.toFixed(d)}` : '—';
};
const pnlCls = (v) => ((Number(v) || 0) >= 0 ? 'positive' : 'negative');
const muted = { color: 'rgba(255,255,255,0.55)', 'font-size': '12px' };
const label = { color: 'rgba(255,255,255,0.5)', 'font-size': '11px', 'text-transform': 'uppercase', 'letter-spacing': '0.04em' };
const td = { padding: '10px' };

// A run that holds (or may hold) a real or simulated position.
// HANDED_OFF: the position is now a normal trade (its own Portfolio row).
export const sbHolding = (s) =>
  s?.phase !== 'HANDED_OFF' &&
  (['BUILDING', 'PAUSED', 'COMPLETE', 'EXITING', 'HALTED'].includes(s?.phase) || (s?.position && !s.position.flat));

export const sbStore = createRoot(() => {
  const [runs, setRuns] = createSignal([]);
  const load = async () => {
    try {
      const d = await (await fetch('/api/sbuild/shadow/state')).json();
      if (d.success) setRuns(d.runs || []);
    } catch (_) { /* gateway restarting */ }
  };
  load();
  setInterval(load, 5000); // backstop if the push stream is down
  window.addEventListener('sbuild_update', (e) => { if (e.detail?.runs) setRuns(e.detail.runs); });
  const holding = () => runs().filter(sbHolding);
  return { runs, holding, load };
});

const post = async (url, body) => {
  try {
    const d = await (await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })).json();
    if (!d.success) { window.alert(d.error || 'failed'); return false; }
    sbStore.load();
    return true;
  } catch (err) { window.alert(String(err)); return false; }
};

function Metric(props) {
  return (
    <div class="trade-metric-row">
      <span>{props.k}</span>
      <strong class={props.cls || ''}>{props.v}</strong>
    </div>
  );
}

function Kpi(props) {
  return (
    <div style={{ padding: '2px 14px', 'border-left': props.first ? 'none' : '1px solid rgba(255,255,255,0.08)', 'min-width': '105px' }}>
      <div style={label}>{props.k}</div>
      <div style={{ 'font-size': '16px', 'font-weight': 600, 'margin-top': '2px', 'font-variant-numeric': 'tabular-nums' }} class={props.cls || ''}>{props.v}</div>
      <Show when={props.sub}><div style={{ ...muted, 'margin-top': '1px' }}>{props.sub}</div></Show>
    </div>
  );
}

function SBRow(props) {
  const s = () => props.run;
  const p = () => s().position || {};
  const cfg = () => s().config || {};
  const legs = () => (p().legs || []).filter((l) => Number(l.qty) !== 0 || Number(l.realized) !== 0);
  const legsOf = (t) => legs().filter((l) => l.option_type === t && Number(l.qty) !== 0);
  const qtyOf = (t) => legsOf(t).reduce((a, l) => a + Number(l.qty), 0);
  const ltpOf = (t) => (legsOf(t).length === 1 ? legsOf(t)[0].mark : null);
  const realized = () => legs().reduce((a, l) => a + (Number(l.realized) || 0), 0);
  const total = () => Number(p().pnl) || 0;
  const unreal = () => total() - realized();
  // "OK: out 25.84 / allowed 49.91 (floor 18.18), net delta ..." from the minute-end check
  const hedgeNum = (k) => {
    const m = String(s().risk?.HEDGE || '').match(new RegExp(`${k} (-?[0-9.]+)`));
    return m ? Number(m[1]) : null;
  };
  // Live, every update: |net delta / net gamma| of the position (the
  // minute-end check's own value is used only if gamma is unavailable).
  const pointsOut = () => (Math.abs(Number(p().net_gamma)) > 1e-6
    ? Math.abs(Number(p().net_delta) / Number(p().net_gamma))
    : (hedgeNum('out') ?? 0));
  const uid = () => s().trade_uid || `SB-SHADOW-${s().rule_id}`;
  const open = () => props.expanded === uid();
  const target = () => Number(s().target_qty) || 0;
  const sold = () => (Number(p().build_ce) || 0) + (Number(p().build_pe) || 0);

  const pause = (on) => post('/api/sbuild/pause', { id: s().rule_id, pause: on });
  const stopBuild = () => {
    if (!window.confirm('Stop building more? The position built so far stays open and monitored (SL / TP / hedge / exit time).')) return;
    post('/api/sbuild/shadow/stop', { id: s().rule_id });
  };
  const squareOff = () => {
    if (!window.confirm(
      `Square off ${cfg().name || s().rule_id} (${uid()}) with MARKET orders?\nOpen: CE ${qtyOf('CE')} / PE ${qtyOf('PE')}, P&L ₹${fmt(total(), 0)}.`)) return;
    post('/api/sbuild/exit', { id: s().rule_id, confirm: 'EXIT' });
  };

  return (
    <>
      <tr onClick={() => props.onToggle(uid())}
        style={{ cursor: 'pointer', background: open() ? '#2a2a3e' : 'transparent', 'border-bottom': '1px solid #333' }}>
        <td style={td}>{uid().slice(-8)}</td>
        <td style={td}>{cfg().symbol}</td>
        <td style={td}>{(s().strikes || []).map((k) => fmt(k, 0)).join(', ') || fmt(s().atm, 0)}</td>
        <td style={td}>
          <span class={`status-badge ${String(s().phase || '').toLowerCase()}`}>{s().mode === 'LIVE' ? 'SBUILD' : 'SBUILD SHADOW'} · {s().phase}</span>
          <Show when={s().phase === 'BUILDING' || s().phase === 'PAUSED'}>
            <div style={{ 'font-size': '11px', opacity: 0.75, 'margin-top': '2px' }}>Built {sold()}/{target()}</div>
          </Show>
        </td>
        <td style={td}>{Math.abs(qtyOf('CE'))}</td>
        <td style={td}>{ltpOf('CE') == null ? '—' : `₹${fmt(ltpOf('CE'))}`}</td>
        <td style={td}>{Math.abs(qtyOf('PE'))}</td>
        <td style={td}>{ltpOf('PE') == null ? '—' : `₹${fmt(ltpOf('PE'))}`}</td>
        <td style={td}>{fmt(p().net_delta, 4)}</td>
        <td style={td}>{fmt(pointsOut(), 2)}</td>
        <td style={td} title="from the minute-end hedge check">{hedgeNum('allowed') == null ? '—' : fmt(hedgeNum('allowed'), 2)}</td>
        <td style={td} class={pnlCls(unreal())}>₹{fmt(unreal())}</td>
        <td style={td} class={pnlCls(realized())}>₹{fmt(realized())}</td>
        <td style={td} class={pnlCls(total())}>₹{fmt(total())}</td>
        <td style={td} class={pnlCls(p().pnl_per_straddle)}>₹{fmt(p().pnl_per_straddle)}</td>
        <td style={{ ...td, opacity: 0.8 }}>—</td>
      </tr>
      <Show when={open()}>
        <tr class="details-row">
          <td colSpan="17" style={{ padding: '16px', background: '#101a33' }}>
            <div class="trade-dashboard">
              <div class="trade-dashboard-header">
                <div>
                  <div class="trade-dashboard-title">Trade Details</div>
                  <div class="trade-dashboard-subtitle">{uid()} · straddle build {cfg().name || s().rule_id}</div>
                </div>
                <span class={`status-badge ${String(s().phase || '').toLowerCase()}`}>{s().mode === 'LIVE' ? 'SBUILD' : 'SBUILD SHADOW'} · {s().phase}</span>
              </div>
              <Show when={s().halt}><div style={{ color: '#ff5252', 'font-size': '12px', margin: '0 0 8px' }}>HALTED: {s().halt}</div></Show>

              <div class="trade-dashboard-grid">
                <section class="trade-card pnl-risk-card">
                  <div class="trade-card-title">PnL &amp; Risk</div>
                  <Metric k="Total PnL" v={`₹${fmt(total())}`} cls={pnlCls(total())} />
                  <Metric k="Unrealized PnL" v={`₹${fmt(unreal())}`} cls={pnlCls(unreal())} />
                  <Metric k="Realized PnL" v={`₹${fmt(realized())}`} cls={pnlCls(realized())} />
                  <Metric k="Points Out" v={fmt(pointsOut())} />
                  <Metric k="Points Allowed" v={hedgeNum('allowed') == null ? '—' : fmt(hedgeNum('allowed'))} />
                  <Metric k="PnL / Straddle" v={`₹${fmt(p().pnl_per_straddle)}`} cls={pnlCls(p().pnl_per_straddle)} />
                  <Metric k="Underlying" v={fmt(s().spot)} />
                </section>

                <section class="trade-card">
                  <div class="trade-card-title">Net Greeks</div>
                  <Metric k="Net Delta" v={fmt(p().net_delta, 4)} />
                  <Metric k="Net Gamma" v={fmt(p().net_gamma, 6)} />
                  <Metric k="Lot Size" v={s().lot_size || '—'} />
                  <Metric k="Absolute Delta" v={fmt(Math.abs(Number(p().net_delta) || 0))} />
                </section>

                <section class="trade-card">
                  <div class="trade-card-title">Monitor Status</div>
                  <Metric k="Monitor" v={s().phase === 'HANDED_OFF' ? 'standard monitor' : 'straddle-build monitor'} />
                  <Metric k="SL (bps of spot)" v={cfg().sl_bps ? `${cfg().sl_bps} bps (₹${fmt(-(Number(s().spot) || 0) * cfg().sl_bps / 10000)}/straddle)` : 'off'} />
                  <Metric k="TP (bps of spot)" v={cfg().tp_bps ? `${cfg().tp_bps} bps (₹${fmt((Number(s().spot) || 0) * cfg().tp_bps / 10000)}/straddle)` : 'off'} />
                  <Metric k="Exit Time" v={cfg().exit_time || 'not set'} />
                  <Metric k="Hedge" v={(s().risk?.HEDGE || '—').split(':')[0]} />
                  <Metric k="H-Div / S-Div" v={`${cfg().hedge_div || '—'} / ${cfg().straddle_div || '—'}`} />
                  <Metric k="Minimum Hedge" v={cfg().hedge_min_bps ? `${cfg().hedge_min_bps} bps` : 'default'} />
                </section>

                <section class="trade-card">
                  <div class="trade-card-title">Build</div>
                  <Metric k="Built" v={`${sold()} / ${target()}`} />
                  <Metric k="CE / PE sold" v={`${p().build_ce || 0} / ${p().build_pe || 0}`} />
                  <Metric k="Sold straddle" v={`${fmt(p().build_straddle)} (${fmt(p().build_avg_ce)} + ${fmt(p().build_avg_pe)})`} />
                  <Metric k="Target" v={`₹${fmt(cfg().target_straddle)}`} />
                  <Metric k="Strikes" v={(s().strikes || []).map((k) => fmt(k, 0)).join(', ') || '—'} />
                  <Metric k="Started" v={`${s().started_at || '—'}${s().resumed_at ? ` · resumed ${s().resumed_at}` : ''}`} />
                </section>
              </div>

              <section class="trade-card manual-actions-card">
                <div class="trade-card-title">Manual Actions</div>
                <div class="manual-actions">
                  <Show when={s().phase === 'BUILDING'}><button class="dashboard-btn yellow" onClick={(e) => { e.stopPropagation(); pause(true); }}>Pause Building</button></Show>
                  <Show when={s().phase === 'PAUSED'}><button class="dashboard-btn blue" onClick={(e) => { e.stopPropagation(); pause(false); }}>Resume Building</button></Show>
                  <Show when={s().phase === 'BUILDING' || s().phase === 'PAUSED'}><button class="dashboard-btn purple" onClick={(e) => { e.stopPropagation(); stopBuild(); }}>Stop Building</button></Show>
                  <Show when={!p().flat}><button class="dashboard-btn red" onClick={(e) => { e.stopPropagation(); squareOff(); }}>Full Exit</button></Show>
                </div>
              </section>

              <section class="trade-card position-details-card">
                <div class="trade-card-title">Position Details</div>
                <div class="position-table-wrap">
                  <table class="trade-position-table">
                    <thead><tr><th>Leg</th><th>Strike</th><th>Action</th><th>Qty</th><th>Entry</th><th>LTP</th><th>PnL</th><th>Delta</th></tr></thead>
                    <tbody>
                      <For each={legs()}>
                        {(l) => (
                          <tr>
                            <td>{l.option_type}</td>
                            <td>{fmt(l.strike, 0)}</td>
                            <td class={Number(l.qty) > 0 ? 'positive' : 'negative'}>{Number(l.qty) > 0 ? 'BUY' : Number(l.qty) < 0 ? 'SELL' : 'FLAT'}</td>
                            <td>{Math.abs(Number(l.qty) || 0)}</td>
                            <td>₹{fmt(l.avg_price)}</td>
                            <td>₹{fmt(l.mark)}</td>
                            <td class={pnlCls(l.pnl)}>₹{fmt(l.pnl)}</td>
                            <td>{sgn((Number(l.delta) || 0) * (Number(l.qty) || 0))}</td>
                          </tr>
                        )}
                      </For>
                    </tbody>
                  </table>
                </div>
              </section>

              <Show when={(s().fills || []).length > 0}>
                <section class="trade-card position-details-card">
                  <div class="trade-card-title">Executions</div>
                  <div class="position-table-wrap">
                    <table class="trade-position-table">
                      <thead><tr><th>Time</th><th>Type</th><th>Leg</th><th>Side</th><th>Qty</th><th>Price</th></tr></thead>
                      <tbody>
                        <For each={s().fills}>
                          {(f) => (
                            <tr>
                              <td>{f.time || '—'}</td>
                              <td>{f.role === 'BUILD' ? 'ENTRY' : f.role}</td>
                              <td>{f.option_type} {fmt(f.strike, 0)}</td>
                              <td class={f.side === 'BUY' ? 'positive' : 'negative'}>{f.side}</td>
                              <td>{f.qty}</td>
                              <td>₹{fmt(f.price)}</td>
                            </tr>
                          )}
                        </For>
                      </tbody>
                    </table>
                  </div>
                </section>
              </Show>
            </div>
          </td>
        </tr>
      </Show>
    </>
  );
}

// Rows for the Portfolio table's <tbody>.
export default function SBPortfolioRows(props) {
  return (
    <Index each={sbStore.holding()}>
      {(run) => <SBRow run={run()} expanded={props.expanded} onToggle={props.onToggle} />}
    </Index>
  );
}
