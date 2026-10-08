import { createSignal, createMemo, onCleanup, onMount, Show, For } from 'solid-js';

// Paper Sim -- PAPER ONLY. Sells the ATM straddle at 09:16-09:19 (and any
// "Start now" minute) off the stored minute data of the current or next
// expiry, then replays every minute: SL / TP / exit and the hedge rule.
// Never places an order.

const fmt = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? n.toFixed(d) : '—';
};
const sgn = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? `${n >= 0 ? '+' : ''}${n.toFixed(d)}` : '—';
};
const pnlCls = (v) => ((Number(v) || 0) >= 0 ? 'positive' : 'negative');
const toMin = (t) => {
  const [h, m] = String(t || '0:0').split(':').map(Number);
  return h * 60 + m;
};
const today = () => new Date(Date.now() + 5.5 * 3600e3).toISOString().slice(0, 10);
const SERIES = ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767'];
const SRC = { chain: 'recorded', import: 'imported', ltp: 'ATM LTP', bs: 'model (BS)' };

const S = {
  muted: { color: 'rgba(255,255,255,0.55)', 'font-size': '12px' },
  label: { color: 'rgba(255,255,255,0.5)', 'font-size': '11px', 'text-transform': 'uppercase', 'letter-spacing': '0.04em' },
  panel: { border: '1px solid rgba(255,255,255,0.08)', 'border-radius': '10px', background: 'rgba(255,255,255,0.015)', padding: '14px 16px', margin: '12px 0' },
  seg: { display: 'inline-flex', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', overflow: 'hidden' },
  segBtn: (on) => ({ padding: '6px 12px', 'font-size': '12px', cursor: 'pointer', border: 'none', color: on ? '#fff' : 'rgba(255,255,255,0.65)', background: on ? 'rgba(57,135,229,0.35)' : 'transparent' }),
  chip: (bg) => ({ padding: '2px 9px', 'border-radius': '10px', 'font-size': '11px', 'font-weight': 700, background: bg, 'letter-spacing': '0.03em' }),
  th: { 'text-align': 'right' },
};
const statusBg = { RUNNING: '#1e6fd0', OPEN: '#1e6fd0', EXITED: '#7b1fa2', 'NO DATA': '#455a64' };

function Seg(props) {
  return (
    <div style={S.seg}>
      <For each={props.options}>
        {([v, l]) => <button style={S.segBtn(props.value === v)} onClick={() => props.onChange(v)}>{l}</button>}
      </For>
    </div>
  );
}

function Field(props) {
  return (
    <label style={{ display: 'flex', 'flex-direction': 'column', gap: '4px' }}>
      <span style={S.label}>{props.label}</span>
      {props.children}
    </label>
  );
}

function Kpi(props) {
  return (
    <div style={{ padding: '4px 16px', 'border-left': props.first ? 'none' : '1px solid rgba(255,255,255,0.08)', 'min-width': '120px' }}>
      <div style={S.label}>{props.k}</div>
      <div style={{ 'font-size': props.big ? '22px' : '17px', 'font-weight': 600, 'margin-top': '2px' }} class={props.cls || ''}>{props.v}</div>
      <Show when={props.sub}><div style={{ ...S.muted, 'margin-top': '1px' }}>{props.sub}</div></Show>
    </div>
  );
}

function Row(props) {
  return (
    <div style={{ display: 'flex', 'justify-content': 'space-between', padding: '4px 0', 'border-bottom': '1px solid rgba(255,255,255,0.04)', 'font-size': '13px' }}>
      <span style={{ color: 'rgba(255,255,255,0.6)' }}>{props.k}</span>
      <span class={props.cls || ''} style={{ 'font-variant-numeric': 'tabular-nums' }}>{props.v}</span>
    </div>
  );
}

function PnlChart(props) {
  const W = 1000, H = props.height || 260, L = 56, R = 12, T = 12, B = 26;
  const [hover, setHover] = createSignal(null);
  const series = () => props.series || [];
  const dom = createMemo(() => {
    let x0 = Infinity, x1 = -Infinity, y0 = 0, y1 = 0;
    for (const s of series()) for (const p of (s.points || [])) {
      const x = toMin(p.time);
      x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, p.pnl); y1 = Math.max(y1, p.pnl);
    }
    if (!Number.isFinite(x0)) return null;
    if (x1 === x0) x1 = x0 + 1;
    const pad = (y1 - y0) * 0.1 || 1;
    return { x0, x1, y0: y0 - pad, y1: y1 + pad };
  });
  const sx = (m) => L + ((m - dom().x0) / (dom().x1 - dom().x0)) * (W - L - R);
  const sy = (v) => T + (1 - (v - dom().y0) / (dom().y1 - dom().y0)) * (H - T - B);
  const ticksY = () => {
    const d = dom(); const span = d.y1 - d.y0;
    const step0 = Math.pow(10, Math.floor(Math.log10(span / 4)));
    const step = [1, 2, 5, 10].map((k) => k * step0).find((s) => span / s <= 5) || step0 * 10;
    const out = [];
    for (let v = Math.ceil(d.y0 / step) * step; v <= d.y1; v += step) out.push(v);
    return out;
  };
  const ticksX = () => {
    const d = dom(); const out = [];
    const step = d.x1 - d.x0 > 240 ? 60 : d.x1 - d.x0 > 90 ? 30 : 15;
    for (let m = Math.ceil(d.x0 / step) * step; m <= d.x1; m += step) out.push(m);
    return out;
  };
  const hhmm = (m) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
  const dec = () => (dom().y1 - dom().y0 < 50 ? 1 : 0);
  const onMove = (e) => {
    const r = e.currentTarget.getBoundingClientRect();
    const x = ((e.clientX - r.left) / r.width) * W;
    if (!dom() || x < L || x > W - R) { setHover(null); return; }
    const m = Math.round(dom().x0 + ((x - L) / (W - L - R)) * (dom().x1 - dom().x0));
    const rows = series().map((s) => {
      let best = null;
      for (const p of s.points) if (toMin(p.time) <= m) best = p;
      return best ? { s, p: best } : null;
    }).filter(Boolean);
    setHover({ m, x: sx(m), left: (sx(m) / W) * 100, rows });
  };
  return (
    <Show when={dom()} fallback={<div style={{ ...S.muted, padding: '24px 0' }}>No stored minutes for this view yet.</div>}>
      <div style={{ position: 'relative' }}>
        <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: 'auto', display: 'block' }} onMouseMove={onMove} onMouseLeave={() => setHover(null)} role="img" aria-label="Simulated P&L through the day">
          <For each={ticksY()}>{(v) => (
            <g>
              <line x1={L} x2={W - R} y1={sy(v)} y2={sy(v)} stroke="rgba(255,255,255,0.06)" />
              <text x={L - 8} y={sy(v) + 4} text-anchor="end" font-size="11" fill="rgba(255,255,255,0.5)">{fmt(v, dec())}</text>
            </g>
          )}</For>
          <line x1={L} x2={W - R} y1={sy(0)} y2={sy(0)} stroke="rgba(255,255,255,0.3)" />
          <For each={ticksX()}>{(m) => <text x={sx(m)} y={H - 8} text-anchor="middle" font-size="11" fill="rgba(255,255,255,0.5)">{hhmm(m)}</text>}</For>
          <For each={series()}>{(s) => (
            <g>
              <polyline fill="none" stroke={s.color} stroke-width="2" stroke-linejoin="round" stroke-linecap="round"
                points={s.points.map((p) => `${sx(toMin(p.time))},${sy(p.pnl)}`).join(' ')} />
              <For each={s.points.filter((p) => p.event && /HEDGE|EXIT|neutral|MTM SQF/.test(p.event))}>
                {(p) => <circle cx={sx(toMin(p.time))} cy={sy(p.pnl)} r="4" fill={s.color} stroke="#14161a" stroke-width="2"><title>{`${s.label} ${p.time}: ${p.event}`}</title></circle>}
              </For>
            </g>
          )}</For>
          <Show when={hover()}>
            <line x1={hover().x} x2={hover().x} y1={T} y2={H - B} stroke="rgba(255,255,255,0.35)" />
            <For each={hover().rows}>{(r) => <circle cx={hover().x} cy={sy(r.p.pnl)} r="4" fill={r.s.color} stroke="#14161a" stroke-width="2" />}</For>
          </Show>
        </svg>
        <Show when={hover() && hover().rows.length}>
          <div style={{ position: 'absolute', top: '6px', left: `min(calc(${hover().left}% + 12px), calc(100% - 240px))`, background: 'rgba(18,20,24,0.96)', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', padding: '6px 10px', 'font-size': '12px', 'pointer-events': 'none', 'min-width': '200px' }}>
            <div style={{ 'font-weight': 600, 'margin-bottom': '3px' }}>{hhmm(hover().m)}</div>
            <For each={hover().rows}>{(r) => (
              <div style={{ display: 'flex', 'justify-content': 'space-between', gap: '12px' }}>
                <span><span style={{ display: 'inline-block', width: '10px', height: '3px', background: r.s.color, 'vertical-align': 'middle', 'margin-right': '6px' }} />{r.s.label}</span>
                <span class={pnlCls(r.p.pnl)}>{sgn(r.p.pnl)}</span>
              </div>
            )}</For>
          </div>
        </Show>
        <Show when={series().length > 1}>
          <div style={{ display: 'flex', gap: '14px', 'flex-wrap': 'wrap', 'margin-top': '6px', 'font-size': '12px', color: 'rgba(255,255,255,0.75)' }}>
            <For each={series()}>{(s) => <span><span style={{ display: 'inline-block', width: '14px', height: '3px', background: s.color, 'vertical-align': 'middle', 'margin-right': '6px' }} />{s.label}</span>}</For>
          </div>
        </Show>
      </div>
    </Show>
  );
}

function SimDetail(props) {
  const s = () => props.sim;
  const cur = () => s().live || (s().series || []).slice(-1)[0] || {};
  const per = (v) => (Number(v) || 0) / (s().qty || 1);
  const isLive = () => !!s().live;
  const [tab, setTab] = createSignal('minutes');
  const riskPct = () => Math.min(100, ((cur().points_out || 0) / (cur().points_allowed || 1)) * 100);
  const minutes = () => [...(s().live ? [{ ...s().live, _live: true }] : []), ...[...(s().series || [])].reverse()];
  return (
    <div>
      <div style={{ ...S.panel, padding: '12px 4px' }}>
        <div style={{ display: 'flex', gap: '10px', 'align-items': 'center', 'flex-wrap': 'wrap', padding: '0 16px 10px' }}>
          <span style={S.chip(statusBg[s().status] || '#455a64')}>{s().status}</span>
          <span style={{ 'font-size': '15px', 'font-weight': 600 }}>
            Short {fmt(s().strike, 0)} straddle @ {fmt(s().entry_straddle)} <span style={S.muted}>· {s().entry_time}</span>
          </span>
          <span style={S.muted}>
            LUT {s().lut_answer || '—'} · qty {s().qty} · hedge {s().config.hedge_mode} · {SRC[cur().source] || cur().source || '—'}
            {s().exit_reason ? ` · exit ${s().exit_reason} ${s().exit_time}` : ''}{isLive() ? ` · live ${cur().time}` : ''}
          </span>
          <Show when={s().error}><span class="negative" style={{ 'font-size': '12px' }}>{s().error}</span></Show>
        </div>
        <div style={{ display: 'flex', 'flex-wrap': 'wrap', 'row-gap': '10px' }}>
          <Kpi first big k="Total P&L" v={sgn(cur().pnl)} cls={pnlCls(cur().pnl)} sub={`${sgn(per(cur().pnl))} per qty`} />
          <Kpi k="Option / hedge" v={<span><span class={pnlCls(cur().option_pnl)}>{sgn(cur().option_pnl)}</span> <span style={S.muted}>/</span> <span class={pnlCls(cur().hedge_pnl)}>{sgn(cur().hedge_pnl)}</span></span>} />
          <Kpi k="Position strike" v={fmt(s().strike, 0)} sub={`market ATM ${fmt(cur().atm, 0)}${cur().atm && cur().atm !== s().strike ? ' (moved)' : ''}`} />
          <Kpi k="Straddle now" v={fmt(cur().straddle)} cls={cur().straddle <= s().entry_straddle ? 'positive' : 'negative'} sub={`entry ${fmt(s().entry_straddle)} · ${sgn(cur().straddle - s().entry_straddle)}`} />
          <Kpi k="Net delta" v={fmt(cur().net_delta, 4)} sub={`hedge ${fmt(cur().hedge_fut_qty, 4)} / qty`} />
          <Kpi k="Theta / day" v={fmt(cur().net_theta, 2)} sub={`vega ${fmt(cur().net_vega, 2)}`} />
          <Kpi k="Risk (out / allowed)" v={`${fmt(cur().points_out, 1)} / ${fmt(cur().points_allowed, 1)}`}
            sub={<div style={{ height: '4px', width: '110px', background: 'rgba(255,255,255,0.08)', 'border-radius': '2px', 'margin-top': '5px' }}>
              <div style={{ height: '4px', width: `${riskPct()}%`, background: riskPct() >= 100 ? '#e66767' : riskPct() > 75 ? '#c98500' : '#199e70', 'border-radius': '2px' }} />
            </div>} />
        </div>
      </div>

      <div style={{ display: 'grid', 'grid-template-columns': 'minmax(0, 2.2fr) minmax(260px, 1fr)', gap: '12px' }}>
        <div style={{ ...S.panel, margin: 0 }}>
          <div style={S.label}>P&L through the day</div>
          <div style={{ 'margin-top': '8px' }}><PnlChart series={[{ id: s().config.id, label: s().config.source, color: props.color, points: [...(s().series || []), ...(s().live ? [s().live] : [])] }]} /></div>
        </div>
        <div style={{ ...S.panel, margin: 0 }}>
          <div style={S.label}>Entry</div>
          <Row k={`Strike ${fmt(s().strike, 0)} CE / PE`} v={`${fmt(s().ce_entry)} / ${fmt(s().pe_entry)}`} />
          <Row k="Entry recorded at" v={(s().series || [])[0]?.quote_ts || s().entry_time} />
          <Row k="Future" v={fmt(s().entry_future)} />
          <div style={{ ...S.label, 'margin-top': '10px' }}>Now</div>
          <Row k={`Position ${fmt(s().strike, 0)} CE / PE`} v={`${fmt(cur().ce)} / ${fmt(cur().pe)}`} />
          <Row k="Future · ATM" v={`${fmt(cur().future)} · ${fmt(cur().atm, 0)}`} />
          <Row k={`ATM ${fmt(cur().atm, 0)} CE / PE`} v={`${fmt(cur().atm_ce)} / ${fmt(cur().atm_pe)}`} />
          <Row k="ATM straddle" v={fmt(cur().atm_straddle)} />
          <Row k="Prices recorded at" v={cur().quote_ts || '—'} />
          <Row k="Min / max straddle" v={`${fmt(s().min_straddle)} / ${fmt(s().max_straddle)}`} />
          <div style={{ ...S.label, 'margin-top': '10px' }}>Rules</div>
          <Row k="TP" v={`${fmt(s().tp_points)} pts · ${fmt(s().tp_bps, 2)} bps`} />
          <Row k="SL" v={`${fmt(-s().sl_points)} pts · ${fmt(s().config.sl_bps, 0)} bps`} />
          <Show when={s().config.mtm_sqf_on}>
            <Row k="MTM square-off" v={`≥ ₹${fmt(s().mtm_sqf_floor)} (${fmt(s().config.mtm_sqf_level)} ${s().config.mtm_sqf_unit === 'rs' ? '₹' : s().config.mtm_sqf_unit}) · ${fmt(s().config.mtm_sqf_pct, 0)}%${s().mtm_sqf_time ? ` · fired ${s().mtm_sqf_time}, ${s().mtm_sqf_lots} lot(s)` : ' · waiting'}`} />
            <Row k="MTM at bid/ask now" v={sgn(cur().exec_pnl)} cls={pnlCls(cur().exec_pnl)} />
          </Show>
          <Row k="Hedge check" v={cur().hedge_check || '—'} cls={/BREACH/.test(cur().hedge_check || '') ? 'negative' : ''} />
          <Row k="Hedge target / qty" v={fmt(cur().hedge_target, 4)} />
          <Row k="Gamma" v={fmt(cur().net_gamma, 5)} />
          <Row k="Max / min P&L" v={`${sgn(s().max_pnl)} / ${sgn(s().min_pnl)}`} />
        </div>
      </div>

      <div style={S.panel}>
        <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', 'margin-bottom': '10px', 'flex-wrap': 'wrap' }}>
          <Seg value={tab()} onChange={setTab} options={[['minutes', 'Minutes'], ['hedges', `Hedges (${(s().hedges || []).length})`], ['position', 'Position']]} />
          <span style={S.muted}>
            {tab() === 'minutes' ? 'Each stored minute\'s first tick; SL / TP / exit and hedges are decided on these rows only. The top row is a live preview.' : ''}
            {tab() === 'hedges' ? 'Delta-neutral at entry, then re-neutralised at each minute end where points out > allowed.' : ''}
          </span>
        </div>

        <Show when={tab() === 'minutes'}>
          <div class="position-table-wrap" style={{ 'max-height': '460px', overflow: 'auto' }}>
            <table class="trade-position-table" style={{ 'font-size': '12px' }}>
              <thead><tr>
                <th>Time</th><th>Recorded at</th><th style={S.th}>Future</th><th style={S.th}>ATM</th><th style={S.th}>ATM CE</th><th style={S.th}>ATM PE</th>
                <th style={S.th}>Pos K</th><th style={S.th}>CE</th><th style={S.th}>PE</th><th style={S.th}>Straddle</th>
                <th style={S.th}>Option</th><th style={S.th}>Hedge</th><th style={S.th}>Total</th><Show when={s().config.mtm_sqf_on}><th style={S.th} title="MTM if closed now at bid/ask">Exec MTM</th></Show><th style={S.th}>Δ</th><th style={S.th}>Γ</th><th style={S.th}>Θ</th><th style={S.th}>V</th>
                <th style={S.th}>Hedge / qty</th><th style={S.th}>Out / allowed</th><th>Check</th><th>Event</th>
              </tr></thead>
              <tbody>
                <For each={minutes()}>{(p) => (
                  <tr style={{ background: p._live ? 'rgba(57,135,229,0.10)' : p.event ? 'rgba(201,133,0,0.07)' : '' }}>
                    <td>{p._live ? `${p.time} ·live` : p.time}</td>
                    <td style={{ ...S.muted, 'white-space': 'nowrap' }}>{p.quote_ts || ''}</td>
                    <td style={S.th}>{fmt(p.future)}</td><td style={S.th}>{fmt(p.atm, 0)}</td><td style={S.th}>{fmt(p.atm_ce)}</td><td style={S.th}>{fmt(p.atm_pe)}</td>
                    <td style={S.th}>{fmt(p.strike || s().strike, 0)}</td>
                    <td style={S.th}>{fmt(p.ce)}</td><td style={S.th}>{fmt(p.pe)}</td><td style={S.th}>{fmt(p.straddle)}</td>
                    <td style={S.th} class={pnlCls(p.option_pnl)}>{sgn(p.option_pnl)}</td>
                    <td style={S.th} class={pnlCls(p.hedge_pnl)}>{sgn(p.hedge_pnl)}</td>
                    <td style={{ ...S.th, 'font-weight': 600 }} class={pnlCls(p.pnl)}>{sgn(p.pnl)}</td>
                    <Show when={s().config.mtm_sqf_on}><td style={S.th} class={pnlCls(p.exec_pnl)}>{sgn(p.exec_pnl)}</td></Show>
                    <td style={S.th}>{fmt(p.net_delta, 3)}</td><td style={S.th}>{fmt(p.net_gamma, 5)}</td><td style={S.th}>{fmt(p.net_theta, 2)}</td><td style={S.th}>{fmt(p.net_vega, 2)}</td>
                    <td style={S.th}>{fmt(p.hedge_fut_qty, 4)}</td>
                    <td style={S.th}>{p.points_allowed ? `${fmt(p.points_out, 1)} / ${fmt(p.points_allowed, 1)}` : '—'}</td>
                    <td style={{ color: /BREACH/.test(p.hedge_check || '') ? '#e66767' : 'rgba(255,255,255,0.6)' }}>{p.hedge_check || ''}</td>
                    <td style={{ color: '#d9a441', 'white-space': 'nowrap' }} title={p.event || ''}>{(p.event || '').replace(/^ENTRY.*?; /, 'ENTRY; ').slice(0, 70)}</td>
                  </tr>
                )}</For>
              </tbody>
            </table>
          </div>
        </Show>

        <Show when={tab() === 'hedges'}>
          <Show when={(s().hedges || []).length} fallback={<div style={S.muted}>No hedges.</div>}>
            <table class="trade-position-table" style={{ 'font-size': '12px' }}>
              <thead><tr><th>Time</th><th>Type</th><th style={S.th}>Future</th><th style={S.th}>Old / qty</th><th style={S.th}>New / qty</th><th style={S.th}>Change</th><th style={S.th}>Delta before → after</th><th style={S.th}>Out / allowed</th></tr></thead>
              <tbody><For each={s().hedges}>{(h) => (
                <tr>
                  <td>{h.time}</td><td>{h.kind || h.mode}</td><td style={S.th}>{fmt(h.future)}</td>
                  <td style={S.th}>{h.mode === 'synthetic' ? fmt(h.old_hedge_qty, 4) : '—'}</td>
                  <td style={S.th}>{h.mode === 'synthetic' ? fmt(h.new_hedge_qty, 4) : '—'}</td>
                  <td style={S.th}>{h.mode === 'synthetic' ? sgn(h.new_hedge_qty - h.old_hedge_qty, 4) : `${h.lots} lot(s) ${h.ce_side} CE / ${h.pe_side} PE @${fmt(h.strike, 0)}`}</td>
                  <td style={S.th}>{fmt(h.delta_before, 4)} → {fmt(h.delta_after, 4)}</td>
                  <td style={S.th}>{h.allowed ? `${fmt(h.points_out, 1)} / ${fmt(h.allowed, 1)}` : '—'}</td>
                </tr>
              )}</For></tbody>
            </table>
          </Show>
        </Show>

        <Show when={tab() === 'position'}>
          <table class="trade-position-table" style={{ 'font-size': '12px' }}>
            <thead><tr><th>Leg</th><th style={S.th}>Qty</th><th style={S.th}>Avg</th><th style={S.th}>Mark</th><th style={S.th}>IV</th><th style={S.th}>Δ</th><th style={S.th}>Γ</th><th style={S.th}>Θ</th><th style={S.th}>V</th><th style={S.th}>P&L</th><th>Priced</th></tr></thead>
            <tbody><For each={(isLive() ? s().live.legs : s().legs) || []}>{(l) => (
              <tr>
                <td>{l.option_type === 'FUT' ? 'Synthetic future (hedge)' : `${fmt(l.strike, 0)} ${l.option_type}`}{l.role === 'HEDGE' && l.option_type !== 'FUT' ? ' · hedge' : ''}</td>
                <td style={S.th} class={(l.qty || l.qty_f) < 0 ? 'negative' : 'positive'}>{l.option_type === 'FUT' ? fmt(l.qty_f, 4) : l.qty}</td>
                <td style={S.th}>{fmt(l.avg_price)}</td><td style={S.th}>{fmt(l.mark)}</td>
                <td style={S.th}>{l.option_type === 'FUT' ? '—' : `${fmt((l.iv || 0) * 100, 2)}%`}</td>
                <td style={S.th}>{fmt(l.delta, 4)}</td><td style={S.th}>{fmt(l.gamma, 6)}</td><td style={S.th}>{fmt(l.theta, 3)}</td><td style={S.th}>{fmt(l.vega, 3)}</td>
                <td style={S.th} class={pnlCls(l.pnl)}>{sgn(l.pnl)}</td><td style={S.muted}>{SRC[l.source] || l.source}</td>
              </tr>
            )}</For></tbody>
          </table>
        </Show>
      </div>
    </div>
  );
}

export default function PaperSimTab() {
  const [day, setDay] = createSignal(today());
  const [view, setView] = createSignal('current');
  const [size, setSize] = createSignal('1');
  const [hedge, setHedge] = createSignal('synthetic');
  // MTM square-off, applied to every simulated build; remembered per browser.
  const saved = (k, d) => { try { return localStorage.getItem(k) ?? d; } catch { return d; } };
  const remember = (k, set) => (v) => { set(v); try { localStorage.setItem(k, v); } catch { /* storage unavailable */ } };
  const [mtmLevel, setMtmLevelRaw] = createSignal(saved('paper.mtm.level', ''));
  const [mtmUnit, setMtmUnitRaw] = createSignal(saved('paper.mtm.unit', 'rs'));
  const [mtmPct, setMtmPctRaw] = createSignal(saved('paper.mtm.pct', '100'));
  const setMtmLevel = remember('paper.mtm.level', setMtmLevelRaw);
  const setMtmUnit = remember('paper.mtm.unit', setMtmUnitRaw);
  const setMtmPct = remember('paper.mtm.pct', setMtmPctRaw);
  const mtmQuery = () => (String(mtmLevel()).trim() === '' ? '' : `&mtm=${encodeURIComponent(mtmLevel())}&mtm_unit=${mtmUnit()}&mtm_pct=${encodeURIComponent(mtmPct() || '100')}`);
  const [data, setData] = createSignal(null);
  const [sel, setSel] = createSignal('overview');
  const [startAt, setStartAt] = createSignal('');
  const [msg, setMsg] = createSignal('');
  const slots = new Map();
  const colorOf = (id) => {
    if (!slots.has(id)) slots.set(id, slots.size);
    const i = slots.get(id);
    return i < SERIES.length ? SERIES[i] : 'rgba(255,255,255,0.35)';
  };

  let busy = false;
  const load = async () => {
    if (busy) return;
    busy = true;
    try {
      const n = Math.max(1, Number(size()) || 1);
      const d = await (await fetch(`/api/paper/sim?day=${day()}&expiry=${view()}&lots=${n}&size=${n}&hedge=${hedge()}${mtmQuery()}`)).json();
      if (d.success) {
        for (const s of d.sims || []) colorOf(s.config.id);
        setData(d);
        if (sel() !== 'overview' && !(d.sims || []).some((s) => s.config.id === sel())) setSel('overview');
      } else setMsg(d.error || 'failed');
    } catch (e) { setMsg(String(e)); }
    busy = false;
  };
  let timer;
  onMount(() => { load(); timer = setInterval(() => { if (day() === today() && !document.hidden) load(); }, 2000); });
  onCleanup(() => clearInterval(timer));
  const reload = (fn) => (v) => { fn(v); load(); };

  const startNow = async () => {
    setMsg('');
    try {
      const res = await fetch('/api/paper/start', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ entry: startAt().trim(), lots: Math.max(1, Number(size()) || 1), view: view() === 'next' ? 'next' : '' }) });
      const d = await res.json();
      if (!d.success) { setMsg(d.error || 'failed'); return; }
      setMsg(`Paper entry at ${d.paper.entry}`);
      await load();
      setSel(d.paper.id);
    } catch (e) { setMsg(String(e)); }
  };
  const remove = async (id) => {
    const s = simById(id);
    if (!window.confirm(`Remove simulation "${s ? label(s) : id}"?`)) return;
    await fetch('/api/paper/remove', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id, view: view(), day: day() }) });
    setSel('overview');
    load();
  };
  const restoreHidden = async () => {
    await fetch('/api/paper/remove', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ restore: true, view: view(), day: day() }) });
    load();
  };

  const sims = () => data()?.sims || [];
  const cur = (s) => s.live || (s.series || []).slice(-1)[0] || {};
  const label = (s) => {
    const id = s.config.id;
    if (id.startsWith('AUTO-')) return id.slice(5);
    if (id.startsWith('PAPER-')) return `Start ${s.config.entry}`;
    return `Actual ${s.entry_time || s.config.entry}`;
  };
  const simById = (id) => sims().find((s) => s.config.id === id);
  const chartSeries = createMemo(() => sims().filter((s) => (s.series || []).length)
    .map((s) => ({ id: s.config.id, label: label(s), color: colorOf(s.config.id), points: [...(s.series || []), ...(s.live ? [s.live] : [])] })));

  return (
    <section class="tab-panel">
      <div class="panel-header" style={{ 'align-items': 'center' }}>
        <div class="panel-title">
          Paper Sim
          <span style={{ ...S.chip('rgba(255,255,255,0.08)'), 'margin-left': '10px', 'font-weight': 600 }}>PAPER · never executed</span>
        </div>
        <div style={S.muted}>
          {data()?.expiry ? `${data().expiry} · ` : ''}{data()?.minutes || 0} minutes {data()?.first ? `${data().first}–${data().last}` : ''}{data()?.live_at ? ` · live ${data().live_at}` : ''}
        </div>
      </div>

      <div style={{ display: 'flex', gap: '14px', 'flex-wrap': 'wrap', 'align-items': 'flex-end', padding: '4px 0 2px' }}>
        <Field label="Day"><input class="symbol-select" type="date" value={day()} onChange={(e) => { setDay(e.currentTarget.value); load(); }} /></Field>
        <Field label="Expiry"><Seg value={view()} onChange={reload(setView)} options={[['current', 'Current'], ['next', 'Next week']]} /></Field>
        <Field label="Size (qty)"><input class="symbol-select" style={{ width: '90px' }} type="number" min="1" value={size()} onInput={(e) => setSize(e.currentTarget.value)} onChange={load} title="1 = one straddle (per-qty values); 65 = one NIFTY lot" /></Field>
        <Field label="Hedge"><Seg value={hedge()} onChange={reload(setHedge)} options={[['synthetic', 'Synthetic'], ['lots', 'ATM lots'], ['off', 'Off']]} /></Field>
        <Field label="MTM square-off (every build)">
          <div style={{ display: 'flex', gap: '6px', 'align-items': 'center' }}>
            <input class="symbol-select" style={{ width: '90px' }} type="number" step="any" placeholder="off" value={mtmLevel()}
              onInput={(e) => setMtmLevel(e.currentTarget.value)} onChange={load}
              title="Close the trade lot by lot at bid/ask once it can end with total MTM at or above this level (may be negative). Blank = off." />
            <Seg value={mtmUnit()} onChange={reload(setMtmUnit)} options={[['rs', '₹'], ['pts', 'pts'], ['bps', 'bps']]} />
            <input class="symbol-select" style={{ width: '60px' }} type="number" min="1" max="100" value={mtmPct()}
              onInput={(e) => setMtmPct(e.currentTarget.value)} onChange={load} title="% of the position to square off (100 = complete)" />
            <span style={S.muted}>%</span>
          </div>
        </Field>
        <Show when={day() === today()}>
          <Field label="Start at">
            <div style={{ display: 'flex', gap: '6px' }}>
              <input class="symbol-select" style={{ width: '90px' }} type="text" placeholder="now" value={startAt()} onInput={(e) => setStartAt(e.currentTarget.value)} />
              <button class="tab-btn" onClick={startNow}>Start</button>
            </div>
          </Field>
        </Show>
        <span style={{ ...S.muted, 'padding-bottom': '8px' }}>{msg()}</span>
      </div>

      <Show when={data()}>
        <div style={{ display: 'flex', gap: '6px', 'flex-wrap': 'wrap', margin: '14px 0 2px' }}>
          <button class={`tab-btn ${sel() === 'overview' ? 'active' : ''}`} onClick={() => setSel('overview')}>Overview</button>
          <For each={sims()}>{(s) => (
            <button class={`tab-btn ${sel() === s.config.id ? 'active' : ''}`} onClick={() => setSel(s.config.id)}>
              <span style={{ display: 'inline-block', width: '8px', height: '8px', 'border-radius': '50%', background: colorOf(s.config.id), 'margin-right': '6px' }} />
              {label(s)} <span class={pnlCls(cur(s).pnl)} style={{ 'margin-left': '4px' }}>{s.error ? '—' : sgn(cur(s).pnl)}</span>
            </button>
          )}</For>
        </div>

        <Show when={sel() !== 'overview' && simById(sel())}>
          <div style={{ display: 'flex', 'justify-content': 'flex-end', 'margin-top': '6px' }}>
            <button class="tab-btn" onClick={() => remove(sel())} title={String(sel()).startsWith('PAPER-') ? 'Delete this paper trade' : 'Hide this simulation for this day (Overview → Show hidden brings it back)'}>Remove</button>
          </div>
          <SimDetail sim={simById(sel())} color={colorOf(sel())} />
        </Show>

        <Show when={sel() === 'overview'}>
          <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(200px, 1fr))', gap: '10px', margin: '12px 0' }}>
            <For each={sims()}>{(s) => (
              <div style={{ ...S.panel, margin: 0, cursor: 'pointer', 'border-top': `3px solid ${colorOf(s.config.id)}` }} onClick={() => setSel(s.config.id)}>
                <div style={{ display: 'flex', 'justify-content': 'space-between', 'align-items': 'center' }}>
                  <strong>{label(s)}</strong>
                  <span style={S.chip(statusBg[s.status] || '#455a64')}>{s.status}</span>
                </div>
                <Show when={!s.error} fallback={<div style={{ ...S.muted, 'margin-top': '8px' }}>{s.error}</div>}>
                  <div style={{ 'font-size': '22px', 'font-weight': 600, margin: '6px 0 2px' }} class={pnlCls(cur(s).pnl)}>{sgn(cur(s).pnl)}</div>
                  <div style={S.muted}>{sgn(cur(s).pnl_per_straddle)} per qty · LUT {s.lut_answer}</div>
                  <div style={{ ...S.muted, 'margin-top': '6px' }}>{fmt(s.strike, 0)} · {fmt(s.entry_straddle)} → {fmt(cur(s).straddle)} · {(s.hedges || []).length} hedges</div>
                </Show>
              </div>
            )}</For>
          </div>

          <div style={S.panel}>
            <div style={S.label}>P&L through the day</div>
            <div style={{ 'margin-top': '8px' }}><PnlChart series={chartSeries()} height={300} /></div>
          </div>

          <Show when={(data().actual || []).length}>
            <div style={S.panel}>
              <div style={S.label}>Actual trades vs simulation (same entry minute)</div>
              <table class="trade-position-table" style={{ 'font-size': '12px', 'margin-top': '8px' }}>
                <thead><tr><th>Trade</th><th>Entry</th><th style={S.th}>Strike</th><th style={S.th}>Actual CE + PE</th><th style={S.th}>Sim CE + PE</th><th style={S.th}>Diff</th><th style={S.th}>Actual realized</th><th style={S.th}>Sim P&L</th><th style={S.th}>Hedges actual / sim</th><th>Status</th></tr></thead>
                <tbody><For each={data().actual}>{(a) => {
                  const sim = () => simById(a.sim_id) || {};
                  const act = () => (a.ce_entry || 0) + (a.pe_entry || 0);
                  return (
                    <tr style={{ cursor: 'pointer' }} onClick={() => setSel(a.sim_id)}>
                      <td>{a.trade_uid}</td><td>{a.entry_time}</td><td style={S.th}>{fmt(a.strike, 0)}</td>
                      <td style={S.th}>{fmt(act())}</td><td style={S.th}>{fmt(sim().entry_straddle)}</td>
                      <td style={S.th} class={pnlCls(act() - (sim().entry_straddle || 0))}>{sgn(act() - (sim().entry_straddle || 0))}</td>
                      <td style={S.th} class={pnlCls(a.realized_pnl)}>{sgn(a.realized_pnl)}</td><td style={S.th} class={pnlCls(sim().pnl)}>{sgn(sim().pnl)}</td>
                      <td style={S.th}>{(a.hedges || []).length} / {(sim().hedges || []).length}</td><td>{a.status}</td>
                    </tr>
                  );
                }}</For></tbody>
              </table>
            </div>
          </Show>

          <Show when={data().hidden > 0}>
            <div style={{ 'margin-top': '6px' }}><button class="tab-btn" onClick={restoreHidden}>Show hidden ({data().hidden})</button></div>
          </Show>
          <div style={{ ...S.muted, 'margin-top': '4px' }}>
            Sources: {data().minute_sources?.chain || 0} recorded · {data().minute_sources?.lut_atm_only || 0} LUT-ATM · {(data().minute_sources?.lut_atm_plus_import || 0) + (data().minute_sources?.import_only || 0)} imported minutes. Strikes missing from a minute are priced by Black-Scholes and flagged.
          </div>
        </Show>
      </Show>
    </section>
  );
}
