import { createSignal, createMemo, onCleanup, onMount, Show, For } from 'solid-js';

// Straddle Build -- sell the ATM straddle at or above a target, sized off
// L1-L5 depth. Several rules can run at once; each has its own build, PMS,
// risk and audit trail. SHADOW = simulated fills; LIVE = real orders behind
// a typed confirmation. Live state arrives as "sbuild_update" ({runs}).

const fmt = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? n.toFixed(d) : '—';
};
const sgn = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? `${n >= 0 ? '+' : ''}${n.toFixed(d)}` : '—';
};
const pnlCls = (v) => ((Number(v) || 0) >= 0 ? 'positive' : 'negative');
const S = {
  muted: { color: 'rgba(255,255,255,0.55)', 'font-size': '12px' },
  label: { color: 'rgba(255,255,255,0.5)', 'font-size': '11px', 'text-transform': 'uppercase', 'letter-spacing': '0.04em' },
  panel: { border: '1px solid rgba(255,255,255,0.08)', 'border-radius': '10px', background: 'rgba(255,255,255,0.015)', padding: '14px 16px', 'margin-bottom': '12px' },
  seg: { display: 'inline-flex', border: '1px solid rgba(255,255,255,0.12)', 'border-radius': '8px', overflow: 'hidden' },
  segBtn: (on) => ({ padding: '6px 12px', 'font-size': '12px', cursor: 'pointer', border: 'none', color: on ? '#fff' : 'rgba(255,255,255,0.65)', background: on ? 'rgba(57,135,229,0.35)' : 'transparent' }),
  chip: (bg) => ({ padding: '2px 8px', 'border-radius': '10px', 'font-size': '10.5px', 'font-weight': 700, background: bg, 'letter-spacing': '0.03em', 'white-space': 'nowrap' }),
  th: { 'text-align': 'right' },
  input: { width: '100%', 'min-width': 0, 'box-sizing': 'border-box', padding: '7px 9px', 'font-size': '13px', 'border-radius': '8px' },
  // The app-wide .trade-position-table has min-width 760px; these tables sit in
  // half-width columns, so they must be allowed to shrink.
  table: { width: '100%', 'min-width': '0', 'font-size': '12px' },
  tableFixed: { width: '100%', 'min-width': '0', 'font-size': '12px', 'table-layout': 'fixed' },
  btnDanger: { background: '#b71c1c', color: '#fff', 'border-color': '#b71c1c' },
  btnExit: { background: '#6a1b9a', color: '#fff', 'border-color': '#6a1b9a' },
};
const phaseColor = { BUILDING: '#1e6fd0', PAUSED: '#f9a825', HANDED_OFF: '#00897b', COMPLETE: '#2e7d32', EXITING: '#ef6c00', EXITED: '#7b1fa2', HALTED: '#c62828', STOPPED: '#5d4037', IDLE: '#455a64' };
const kindColor = { PLAN: '#64b5f6', TRANCHE: '#81c784', HEDGE: '#ffb74d', RISK: '#90a4ae', EXIT: '#e57373', RULE: '#ba68c8', ATM: '#4dd0e1', RESUME: '#fff176', FILL: '#a5d6a7', COMPLETE: '#4db6ac', HANDOFF: '#80cbc4', ORDER: '#ef9a9a', LIVE: '#ff8a65', HALT: '#ff5252', INFO: '#b0bec5' };
const SYMBOLS = ['NIFTY', 'BANKNIFTY', 'FINNIFTY', 'MIDCPNIFTY', 'SENSEX', 'BANKEX'];
const FIELDS = ['name', 'symbol', 'expiry', 'target_straddle', 'straddles', 'sl_bps', 'tp_bps', 'exit_time', 'straddle_div', 'hedge_div', 'hedge_min_bps'];
const TEXT_FIELDS = ['name', 'symbol', 'expiry', 'exit_time'];
const isRunning = (s) => ['BUILDING', 'PAUSED', 'COMPLETE', 'EXITING', 'HALTED'].includes(s?.phase);
const isLive = (s) => s?.mode === 'LIVE';
const modeChip = (s) => S.chip(isLive(s) ? '#c62828' : 'rgba(255,255,255,0.10)');
const phaseChip = (p) => S.chip(phaseColor[p] || '#455a64');

function Seg(props) {
  return (
    <div style={S.seg}>
      <For each={props.options}>{([v, l]) => <button style={S.segBtn(props.value === v)} onClick={() => props.onChange(v)}>{l}</button>}</For>
    </div>
  );
}

function Kpi(props) {
  return (
    <div style={{ padding: '2px 14px', 'border-left': props.first ? 'none' : '1px solid rgba(255,255,255,0.08)', 'min-width': '110px' }}>
      <div style={S.label}>{props.k}</div>
      <div style={{ 'font-size': props.big ? '21px' : '16px', 'font-weight': 600, 'margin-top': '2px', 'font-variant-numeric': 'tabular-nums' }} class={props.cls || ''}>{props.v}</div>
      <Show when={props.sub}><div style={{ ...S.muted, 'margin-top': '1px' }}>{props.sub}</div></Show>
    </div>
  );
}

function Ladder(props) {
  return (
    <div>
      <div style={{ ...S.label, 'margin-bottom': '4px' }}>{props.title}</div>
      <table class="trade-position-table" style={S.tableFixed}>
        <thead><tr><th style={{ width: '44px' }}>Lvl</th><th style={S.th}>Bid qty</th><th style={S.th}>Bid</th><th style={S.th}>Ask</th><th style={S.th}>Ask qty</th></tr></thead>
        <tbody><For each={[0, 1, 2, 3, 4]}>{(i) => (
          <tr>
            <td style={S.muted}>L{i + 1}</td>
            <td style={S.th}>{props.bids?.[i]?.[1] ?? '—'}</td>
            <td style={S.th} class="positive">{props.bids?.[i] ? fmt(props.bids[i][0]) : '—'}</td>
            <td style={S.th} class="negative">{props.asks?.[i] ? fmt(props.asks[i][0]) : '—'}</td>
            <td style={S.th}>{props.asks?.[i]?.[1] ?? '—'}</td>
          </tr>
        )}</For></tbody>
      </table>
    </div>
  );
}

const toForm = (c) => {
  const r = { id: c?.id || '' };
  for (const k of FIELDS) r[k] = c?.[k] ?? '';
  return r;
};

export default function StraddleBuildTab() {
  const [runs, setRuns] = createSignal([]);
  const [selected, setSelected] = createSignal(''); // rule id, '' = new rule
  const [saved, setSaved] = createSignal(null); // form as last saved
  const [rule, setRule] = createSignal({});
  const [defaults, setDefaults] = createSignal({});
  const [expiries, setExpiries] = createSignal([]);
  const [nearest, setNearest] = createSignal('');
  const [lotSize, setLotSize] = createSignal(0);
  const [msg, setMsg] = createSignal('');

  const st = createMemo(() => runs().find((r) => r.rule_id === selected()) || null);
  const running = () => isRunning(st());
  const setField = (k) => (e) => setRule((r) => ({ ...r, [k]: e.currentTarget.value }));
  const dirty = createMemo(() => {
    const s = saved();
    if (!s) return true;
    return FIELDS.some((k) => String(rule()[k] ?? '') !== String(s[k] ?? ''));
  });

  const loadExpiries = async (sym) => {
    try {
      const d = await (await fetch(`/api/sbuild/expiries?symbol=${encodeURIComponent(sym || rule().symbol || 'NIFTY')}`)).json();
      if (d.success) { setExpiries(d.expiries || []); setNearest(d.nearest || ''); setLotSize(d.lot_size || 0); }
    } catch (_) { /* restarting */ }
  };
  const selectRule = (id, cfg) => {
    setSelected(id);
    const f = toForm(cfg || runs().find((r) => r.rule_id === id)?.config);
    setSaved(id ? f : null);
    setRule(f);
    setMsg('');
    loadExpiries(f.symbol);
  };
  const newRule = () => selectRule('', { ...defaults(), id: '', name: '' });

  const loadState = async () => {
    try {
      const d = await (await fetch('/api/sbuild/shadow/state')).json();
      if (d.success) setRuns(d.runs || []);
    } catch (_) { /* restarting */ }
  };
  const loadRules = async () => {
    try {
      const d = await (await fetch('/api/sbuild/rule')).json();
      if (d.default) setDefaults(d.default);
      return d.rules || [];
    } catch (_) { return []; }
  };

  // MROUND to the lot when the box is left (like the manual build), min 1 lot.
  const roundQty = () => {
    const lot = lotSize();
    const q = Number(rule().straddles) || 0;
    if (!lot || !q) return;
    const r = Math.max(lot, Math.round(q / lot) * lot);
    setRule((x) => ({ ...x, straddles: String(r) }));
  };
  const changeSymbol = (e) => {
    const sym = e.currentTarget.value;
    setRule((x) => ({ ...x, symbol: sym, expiry: '' }));
    loadExpiries(sym);
  };

  const onUpdate = (e) => { if (e.detail?.runs) setRuns(e.detail.runs); };
  onMount(async () => {
    const rules = await loadRules();
    await loadState();
    if (rules.length) selectRule(rules[0].id, rules[0]); else newRule();
    window.addEventListener('sbuild_update', onUpdate);
  });
  onCleanup(() => window.removeEventListener('sbuild_update', onUpdate));

  const body = () => {
    const b = { id: rule().id || '' };
    for (const k of FIELDS) b[k] = TEXT_FIELDS.includes(k) ? String(rule()[k] || '').trim() : Number(rule()[k]) || 0;
    return b;
  };
  const call = async (url, payload) => {
    setMsg('');
    try {
      const res = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload || {}) });
      const d = await res.json();
      if (!d.success) { setMsg(d.error || 'failed'); return false; }
      return d;
    } catch (err) { setMsg(String(err)); return false; }
  };
  const saveRule = async () => {
    const isNew = !rule().id;
    const d = await call('/api/sbuild/rule', body());
    if (!d) return false;
    await loadState();
    selectRule(d.rule.id, d.rule);
    setMsg(isNew ? `Rule ${d.rule.id} added` : d.changes?.length ? `Saved${running() ? ' and applied live' : ''}: ${d.changes.join('; ')}` : 'Saved (no changes)');
    return d.rule.id;
  };
  const start = async (id) => {
    let rid = id;
    if (!rid) { // from the editor: save first
      rid = dirty() ? await saveRule() : rule().id;
      if (!rid) return;
    }
    if (await call('/api/sbuild/shadow/start', { id: rid })) { setMsg(`Shadow build started (${rid})`); loadState(); }
  };
  const startLive = async (id) => {
    let rid = id;
    if (!rid) {
      rid = dirty() ? await saveRule() : rule().id;
      if (!rid) return;
    }
    const s = runs().find((x) => x.rule_id === rid);
    const c = s?.config || {};
    const typed = window.prompt(
      `REAL ORDERS on the broker account.\n\n${c.symbol} sell ATM straddle strictly ABOVE ₹${c.target_straddle} (avg CE + avg PE sold always > target), ` +
      `${c.straddles} per leg = ${2 * (c.straddles || 0)} contracts, one lot per IOC limit order. Hedge / SL / TP / exit ${c.exit_time || '—'} with MARKET orders.\n\n` +
      'Type SELL LIVE to start:');
    if (typed == null) return;
    if (await call('/api/sbuild/shadow/start', { id: rid, mode: 'LIVE', confirm: typed.trim() })) { setMsg(`LIVE build started (${rid}) — real orders`); loadState(); }
  };
  const exitNow = async (id) => {
    const s = runs().find((x) => x.rule_id === id);
    let confirm = '';
    if (isLive(s)) {
      const typed = window.prompt('Flatten this LIVE position now with MARKET orders?\nType EXIT to confirm:');
      if (typed == null) return;
      confirm = typed.trim();
    } else if (!window.confirm('Flatten this shadow position now?')) return;
    if (await call('/api/sbuild/exit', { id, confirm })) { setMsg(`Exit sent (${id})`); loadState(); }
  };
  // Shadow -> LIVE: the shadow run is stopped (its fills were simulated, never
  // real) and the same rule starts again with real orders.
  const switchToLive = async (id) => {
    const s = runs().find((x) => x.rule_id === id);
    const c = s?.config || {};
    const typed = window.prompt(
      `Switch ${c.symbol} target ₹${c.target_straddle} from SHADOW to LIVE (REAL ORDERS).\n\n` +
      'The shadow run stops (its simulated fills are discarded) and the rule restarts with real orders: ' +
      `${c.straddles} per leg = ${2 * (c.straddles || 0)} contracts, only above the target.\n\nType SELL LIVE to switch:`);
    if (typed == null) return;
    if (typed.trim() !== 'SELL LIVE') { setMsg('Not switched: confirmation text must be SELL LIVE'); return; }
    if (!(await call('/api/sbuild/shadow/stop', { id }))) return;
    if (await call('/api/sbuild/shadow/start', { id, mode: 'LIVE', confirm: 'SELL LIVE' })) { setMsg(`${id} switched to LIVE — real orders`); }
    loadState();
  };
  const pause = async (id, on) => { if (await call('/api/sbuild/pause', { id, pause: on })) { setMsg(on ? `OMS paused (${id}) — PMS keeps monitoring` : `Building resumed (${id})`); loadState(); } };
  const stop = async (id) => { if (await call('/api/sbuild/shadow/stop', { id })) { setMsg(`Stopped ${id}`); loadState(); } };
  const del = async (id) => {
    if (!window.confirm(`Delete rule ${id}?`)) return;
    if (await call('/api/sbuild/rule/delete', { id })) {
      await loadState();
      const first = runs()[0];
      if (first) selectRule(first.rule_id, first.config); else newRule();
    }
  };

  const ruleLabel = (c) => c?.name || `${c?.symbol} ₹${c?.target_straddle} × ${c?.straddles}`;
  const filledOf = (s) => (s?.position?.build_ce || 0) + (s?.position?.build_pe || 0);
  const totals = createMemo(() => {
    let pnl = 0, filled = 0, active = 0;
    const delta = {};
    for (const s of runs()) {
      if (!s.started_at) continue;
      // Handed to the standard monitor / finished: that position is a
      // normal Portfolio trade now (or closed) -- not counted here.
      if (s.phase === 'HANDED_OFF' || s.phase === 'EXITED' || s.phase === 'STOPPED') continue;
      pnl += s.position?.pnl || 0;
      filled += filledOf(s);
      if (isRunning(s)) active++;
      const k = s.config?.symbol || '?';
      delta[k] = (delta[k] || 0) + (s.position?.net_delta || 0);
    }
    return { pnl, filled, active, delta };
  });

  const pos = () => st()?.position || {};
  const filled = () => filledOf(st());
  const pct = () => (st()?.target_qty ? (filled() / st().target_qty) * 100 : 0);
  // Live quote for the editor's symbol / expiry (also before a rule exists).
  const [quote, setQuote] = createSignal(null);
  const [qErr, setQErr] = createSignal('');
  const [tab, setTab] = createSignal('depth');
  let qBusy = false;
  const loadQuote = async () => {
    if (qBusy || document.hidden) return;
    qBusy = true;
    try {
      const sym = rule().symbol || 'NIFTY';
      const d = await (await fetch(`/api/sbuild/quote?symbol=${encodeURIComponent(sym)}&expiry=${encodeURIComponent(rule().expiry || '')}`)).json();
      if (d.success) { setQuote({ ...d.quote, expiry: d.expiry, time: d.time }); setQErr(''); } else setQErr(d.error || 'no quote');
    } catch (e) { setQErr(String(e)); }
    qBusy = false;
  };
  let qTimer;
  onMount(() => { loadQuote(); qTimer = setInterval(loadQuote, 1000); });
  onCleanup(() => clearInterval(qTimer));

  // The run's own live view (with its tranche preview) when it matches the editor.
  const mkt = () => (st()?.live && st().live.symbol === (rule().symbol || 'NIFTY') && (!rule().expiry || st().live.expiry === rule().expiry) ? st().live : quote());
  const target = () => Number(rule().target_straddle) || 0;
  const gap = () => (mkt()?.bid_straddle || 0) - target();
  const above = () => target() > 0 && gap() > 0;
  const totalQty = () => (Number(rule().straddles) || 0) * 2;
  const lotsTxt = () => (lotSize() ? `${(Number(rule().straddles) || 0) / lotSize()} lot(s) of ${lotSize()}` : '');

  const In = (p) => (
    <label style={{ display: 'flex', 'flex-direction': 'column', gap: '4px', 'min-width': 0 }}>
      <span style={S.label}>{p.label}</span>
      <input class="symbol-select" style={S.input} type={p.type || 'number'} step="any" placeholder={p.ph || ''} value={rule()[p.k] ?? ''} onInput={setField(p.k)} onBlur={p.onBlur} disabled={p.disabled} />
      <Show when={p.hint}><span style={{ ...S.muted, 'font-size': '11px' }}>{p.hint}</span></Show>
    </label>
  );
  const Group = (p) => (
    <div style={{ 'min-width': 0 }}>
      <div style={{ ...S.label, color: 'rgba(255,255,255,0.75)', 'margin-bottom': '8px', 'font-weight': 600 }}>{p.title}</div>
      <div style={{ display: 'grid', 'grid-template-columns': `repeat(${p.cols || 2}, minmax(0, 1fr))`, gap: '10px' }}>{p.children}</div>
    </div>
  );

  const runTabs = () => [['depth', 'Depth'], ...(st()?.started_at ? [['position', 'Position'], ['risk', 'Risk & decision'], ['trail', `Audit (${(st()?.events || []).length})`], ['fills', `Fills (${(st()?.fills || []).length})`]] : [])];

  return (
    <section class="tab-panel">
      <div class="panel-header" style={{ 'align-items': 'center' }}>
        <div class="panel-title">
          Straddle Build
          <span style={{ ...S.chip('rgba(255,255,255,0.08)'), 'margin-left': '10px' }}>SHADOW simulated · LIVE real orders</span>
        </div>
        <div style={{ display: 'flex', gap: '18px', 'align-items': 'baseline' }}>
          <span style={S.muted}>{totals().active} active · {totals().filled} contracts</span>
          <span style={S.muted}>P&L <strong class={pnlCls(totals().pnl)} style={{ 'font-size': '15px' }}>₹{fmt(totals().pnl, 0)}</strong></span>
          <For each={Object.entries(totals().delta)}>{([k, d]) => <span style={S.muted}>{k} Δ {fmt(d, 1)}</span>}</For>
        </div>
      </div>

      <div style={{ display: 'grid', 'grid-template-columns': 'minmax(240px, 300px) minmax(0, 1fr)', gap: '14px', 'align-items': 'start' }}>
        {/* ---------------- Rules list ---------------- */}
        <div>
          <div style={{ display: 'flex', 'justify-content': 'space-between', 'align-items': 'center', 'margin-bottom': '8px' }}>
            <span style={S.label}>Rules ({runs().length})</span>
            <button class="tab-btn" onClick={newRule}>+ New rule</button>
          </div>
          <Show when={runs().length} fallback={<div style={{ ...S.panel, ...S.muted }}>No rules yet. Fill in the editor and press <strong>Add rule</strong>.</div>}>
            <For each={runs()}>{(s) => {
              const sel = () => s.rule_id === selected();
              const pct = () => (s.target_qty ? Math.min(100, (filledOf(s) / s.target_qty) * 100) : 0);
              return (
                <div onClick={() => selectRule(s.rule_id, s.config)}
                  style={{ ...S.panel, cursor: 'pointer', padding: '10px 12px', 'margin-bottom': '8px', border: `1px solid ${sel() ? 'rgba(57,135,229,0.7)' : 'rgba(255,255,255,0.08)'}`, background: sel() ? 'rgba(57,135,229,0.08)' : S.panel.background }}>
                  <div style={{ display: 'flex', 'justify-content': 'space-between', gap: '6px', 'align-items': 'center' }}>
                    <strong style={{ 'font-size': '13px', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>{ruleLabel(s.config)}</strong>
                    <span style={{ display: 'flex', gap: '4px' }}><Show when={isLive(s)}><span style={modeChip(s)}>LIVE</span></Show><span style={phaseChip(s.phase)}>{s.phase}</span></span>
                  </div>
                  <div style={{ ...S.muted, 'margin-top': '4px' }}>{s.config.symbol} · {s.expiry || s.live?.expiry || 'nearest'} · target {fmt(s.config.target_straddle)}</div>
                  <div style={{ display: 'flex', 'justify-content': 'space-between', 'margin-top': '6px', 'font-size': '12px' }}>
                    <span>bid <span class={s.live?.above_target ? 'positive' : 'negative'}>{fmt(s.live?.bid_straddle)}</span> <span style={S.muted}>({s.live?.target ? sgn(s.live.gap) : '—'})</span></span>
                    <span class={pnlCls(s.position?.pnl)}>{s.started_at ? `₹${fmt(s.position?.pnl, 0)}` : ''}</span>
                  </div>
                  <Show when={s.started_at}>
                    <div style={{ height: '4px', background: 'rgba(255,255,255,0.08)', 'border-radius': '2px', 'margin-top': '6px' }}>
                      <div style={{ height: '4px', width: `${pct()}%`, background: '#3987e5', 'border-radius': '2px' }} />
                    </div>
                    <div style={{ ...S.muted, 'font-size': '11px', 'margin-top': '3px' }}>{filledOf(s)} / {s.target_qty} sold{s.position?.build_straddle ? ` @ ${fmt(s.position.build_straddle)}` : ''}{s.busy ? ' · orders in flight' : ''}</div>
                  </Show>
                </div>
              );
            }}</For>
          </Show>
        </div>

        {/* ---------------- Workspace ---------------- */}
        <div style={{ 'min-width': 0 }}>
          {/* Live market for the editor's symbol / expiry */}
          <div style={{ ...S.panel, 'border-color': target() ? (above() ? 'rgba(46,125,50,0.6)' : 'rgba(198,40,40,0.45)') : 'rgba(255,255,255,0.08)' }}>
            <div style={{ display: 'flex', 'justify-content': 'space-between', 'align-items': 'baseline', 'margin-bottom': '10px', 'flex-wrap': 'wrap', gap: '6px' }}>
              <span><strong>{rule().symbol || 'NIFTY'}</strong> <span style={S.muted}>{mkt()?.expiry || rule().expiry || nearest() || ''} · ATM straddle (live)</span></span>
              <span style={S.muted}>{qErr() ? <span class="negative">{qErr()}</span> : `spot ${fmt(mkt()?.spot)} · ${mkt()?.updated || mkt()?.time || ''}`}</span>
            </div>
            <div style={{ display: 'flex', 'flex-wrap': 'wrap', 'row-gap': '10px' }}>
              <Kpi first big k={`ATM ${fmt(mkt()?.atm, 0)} · LTP straddle`} v={fmt(mkt()?.ltp_straddle)} />
              <Kpi big k="Bid straddle (sell gets)" v={fmt(mkt()?.bid_straddle)} cls={target() ? (above() ? 'positive' : 'negative') : ''} />
              <Kpi k="vs target" v={target() ? sgn(gap()) : 'set a target'} cls={target() ? (above() ? 'positive' : 'negative') : ''} sub={target() ? (above() ? 'above — would sell' : 'below — waits') : ''} />
              <Kpi k="CE bid / ask" v={`${fmt(mkt()?.ce_bid)} / ${fmt(mkt()?.ce_ask)}`} sub={`LTP ${fmt(mkt()?.ce_ltp)}`} />
              <Kpi k="PE bid / ask" v={`${fmt(mkt()?.pe_bid)} / ${fmt(mkt()?.pe_ask)}`} sub={`LTP ${fmt(mkt()?.pe_ltp)}`} />
              <Kpi k="VWAP · 1 lot" v={mkt()?.vwap_one_lot ? fmt(mkt().vwap_one_lot) : '—'} sub={mkt()?.ce_depth_levels ? `depth ${mkt().ce_depth_levels}/${mkt().pe_depth_levels} lvls · ${mkt().ce_depth_age_ms}ms` : 'no depth'} />
            </div>
            <Show when={st()?.live?.preview && mkt() === st()?.live}>
              <div style={{ 'margin-top': '10px', 'font-size': '13px' }}>
                <span style={S.label}>Next tranche now </span>
                <strong class={st().live.preview.authorized ? 'positive' : 'negative'}>
                  {st().live.preview.authorized ? `sell CE ${st().live.preview.ce_qty} + PE ${st().live.preview.pe_qty} @ ${fmt(st().live.preview.weighted_straddle)}` : st().live.preview.reason}
                </strong>
                <Show when={st().live.shared_with > 1}><span style={S.muted}> · book shared by {st().live.shared_with} rules ({fmt(st().live.participation * 100, 0)}%)</span></Show>
              </div>
            </Show>
          </div>

          {/* Rule editor */}
          <div style={S.panel}>
            <div style={{ display: 'flex', 'justify-content': 'space-between', 'align-items': 'center', 'margin-bottom': '12px', 'flex-wrap': 'wrap', gap: '8px' }}>
              <span style={{ display: 'flex', gap: '8px', 'align-items': 'center' }}>
                <strong>{rule().id ? ruleLabel(st()?.config || rule()) : 'New rule'}</strong>
                <Show when={rule().id}><span style={S.muted}>{rule().id}</span></Show>
                <Show when={st()}><Show when={isLive(st())}><span style={modeChip(st())}>LIVE</span></Show><span style={phaseChip(st().phase)}>{st().phase}</span></Show>
                <Show when={dirty() && rule().id}><span style={{ ...S.muted, color: '#d9a441' }}>unsaved</span></Show>
              </span>
              <span style={S.muted}>{running() ? (isLive(st()) ? 'LIVE — real orders. Save changes applies edits to this running build.' : 'SHADOW — simulated, no orders. Use Switch to LIVE for real orders.') : ''}</span>
            </div>

            <div style={{ display: 'grid', 'grid-template-columns': 'repeat(2, minmax(0, 1fr))', gap: '18px 28px' }}>
              <Group title="Market">
                <label style={{ display: 'flex', 'flex-direction': 'column', gap: '4px' }}>
                  <span style={S.label}>Symbol</span>
                  <select class="symbol-select" style={S.input} value={rule().symbol || 'NIFTY'} disabled={running()} onChange={changeSymbol}>
                    <For each={SYMBOLS}>{(x) => <option value={x}>{x}</option>}</For>
                  </select>
                </label>
                <label style={{ display: 'flex', 'flex-direction': 'column', gap: '4px' }}>
                  <span style={S.label}>Expiry</span>
                  <select class="symbol-select" style={S.input} value={rule().expiry || ''} disabled={running()} onChange={setField('expiry')}>
                    <option value="">Nearest ({nearest() || '—'})</option>
                    <For each={expiries()}>{(e) => <option value={e}>{e}</option>}</For>
                  </select>
                </label>
                <div style={{ 'grid-column': '1 / -1' }}><In label="Name (optional)" k="name" type="text" ph={`${rule().symbol || 'NIFTY'} ${rule().target_straddle || ''}`} /></div>
              </Group>
              <Group title="Entry">
                <In label="Target straddle ₹ (sell above)" k="target_straddle" ph={mkt()?.bid_straddle ? fmt(mkt().bid_straddle) : ''} />
                <In label="Qty per leg (contracts)" k="straddles" ph={String(lotSize() || 65)} onBlur={roundQty} hint={`${lotsTxt()} · total ${totalQty()}`} />
              </Group>
              <Group title="Risk">
                <In label="SL (bps)" k="sl_bps" />
                <In label="TP (bps)" k="tp_bps" />
                <div style={{ 'grid-column': '1 / -1' }}><In label="Exit time" k="exit_time" type="text" ph="15:37:00" /></div>
              </Group>
              <Group title="Hedge" cols={3}>
                <In label="Straddle ÷" k="straddle_div" />
                <In label="Hedge ÷" k="hedge_div" />
                <In label="Min bps" k="hedge_min_bps" />
              </Group>
            </div>

            <div style={{ display: 'flex', 'justify-content': 'space-between', 'align-items': 'center', 'margin-top': '14px', 'padding-top': '12px', 'border-top': '1px solid rgba(255,255,255,0.06)', 'flex-wrap': 'wrap', gap: '8px' }}>
              <span style={{ display: 'flex', gap: '8px', 'flex-wrap': 'wrap' }}>
                <button class="tab-btn" onClick={saveRule} disabled={!dirty()} title={running() ? 'Saves the edits into the running build (does not change SHADOW / LIVE)' : ''}>{!rule().id ? 'Add rule' : 'Save changes'}</button>
                <Show when={!running()}><button class="tab-btn" onClick={() => start(dirty() ? '' : rule().id)}>Start shadow</button></Show>
                <Show when={st()?.phase === 'BUILDING'}><button class="tab-btn" onClick={() => pause(rule().id, true)}>Pause building</button></Show>
                <Show when={st()?.phase === 'PAUSED'}><button class="tab-btn" onClick={() => pause(rule().id, false)}>Resume building</button></Show>
                <Show when={running()}><button class="tab-btn" onClick={() => stop(rule().id)}>{isLive(st()) ? (st().phase === 'HALTED' ? 'Acknowledge halt' : 'Stop building') : 'Stop'}</button></Show>
                <Show when={st()?.phase !== 'HANDED_OFF' && st()?.phase !== 'EXITED' && (running() || (st()?.position && !st().position.flat))}><button class="tab-btn" style={S.btnExit} onClick={() => exitNow(rule().id)}>Exit now</button></Show>
                <Show when={st()?.phase === 'HANDED_OFF'}><span style={S.muted}>Position handed to trade {st()?.trade_uid} — manage / exit it from Portfolio.</span></Show>
                <Show when={rule().id && !running()}><button class="tab-btn" onClick={() => del(rule().id)}>Delete</button></Show>
              </span>
              <span style={{ display: 'flex', gap: '10px', 'align-items': 'center' }}>
                <span style={{ 'font-size': '12px' }}>{msg()}</span>
                <Show when={!running()}><button class="tab-btn" style={S.btnDanger} onClick={() => startLive(dirty() ? '' : rule().id)}>Start LIVE</button></Show>
                <Show when={running() && !isLive(st())}><button class="tab-btn" style={S.btnDanger} onClick={() => switchToLive(rule().id)}>Switch to LIVE</button></Show>
              </span>
            </div>
            <div style={{ ...S.muted, 'margin-top': '8px', 'font-size': '11px' }}>
              Sells the current ATM, one lot per order, sized off 50% of fillable L1–L5 depth (shared across rules on the same book); avg CE + avg PE sold always stays above the target; tail ≤ 2 lots trims net delta.
            </div>
          </div>

          {/* Run */}
          <Show when={st()?.started_at}>
            <div style={{ ...S.panel, padding: '12px 4px' }}>
              <div style={{ display: 'flex', gap: '10px', 'align-items': 'center', 'flex-wrap': 'wrap', padding: '0 14px 10px' }}>
                <Show when={isLive(st())}><span style={modeChip(st())}>LIVE</span></Show>
                <span style={phaseChip(st().phase)}>{st().phase}</span>
                <span style={S.muted}>started {st().started_at}{st().resumed_at ? ` · resumed ${st().resumed_at}` : ''}{st().trade_uid ? ` · ${st().trade_uid}` : ''} · ATM {fmt(st().atm, 0)} · lot {st().lot_size}</span>
                <Show when={st().busy}><span style={{ ...S.muted, color: '#d9a441' }}>orders in flight…</span></Show>
                <Show when={(st().strikes || []).length > 1}><span style={{ ...S.muted, color: '#4dd0e1' }}>strikes {st().strikes.map((k) => fmt(k, 0)).join(', ')}</span></Show>
              </div>
              <Show when={st().halt}>
                <div style={{ margin: '0 14px 10px', padding: '8px 10px', 'border-radius': '8px', background: 'rgba(198,40,40,0.18)', border: '1px solid #c62828', 'font-size': '13px' }}>
                  <strong>HALTED — nothing more will be sent.</strong> {st().halt}
                </div>
              </Show>
              <div style={{ padding: '0 14px 10px' }}>
                <div style={{ display: 'flex', 'justify-content': 'space-between', 'font-size': '12px', 'margin-bottom': '4px' }}>
                  <span>Sold <strong>{filled()}</strong> / {st().target_qty}</span><span style={S.muted}>{fmt(pct(), 1)}% · remaining {st().remaining}</span>
                </div>
                <div style={{ height: '6px', background: 'rgba(255,255,255,0.08)', 'border-radius': '3px' }}>
                  <div style={{ height: '6px', width: `${Math.min(100, pct())}%`, background: '#3987e5', 'border-radius': '3px', transition: 'width 0.3s' }} />
                </div>
              </div>
              <div style={{ display: 'flex', 'flex-wrap': 'wrap', 'row-gap': '10px' }}>
                <Kpi first big k="P&L" v={`₹${fmt(pos().pnl, 0)}`} cls={pnlCls(pos().pnl)} sub={`${sgn(pos().pnl_per_straddle)} per straddle`} />
                <Kpi k="Sold straddle" v={pos().build_straddle ? fmt(pos().build_straddle) : '—'} cls={pos().build_straddle ? (pos().build_straddle > (st().config?.target_straddle || 0) ? 'positive' : 'negative') : ''}
                  sub={pos().build_straddle ? `${fmt(pos().build_avg_ce)} + ${fmt(pos().build_avg_pe)} · ${sgn(pos().build_straddle - st().config.target_straddle)} vs target` : ''} />
                <Kpi k="CE / PE sold" v={`${pos().build_ce || 0} / ${pos().build_pe || 0}`} sub={`${st().tranches} tranche(s)`} />
                <Kpi k="Net delta" v={fmt(pos().net_delta, 1)} cls={Math.abs(pos().net_delta || 0) < (st().lot_size || 65) * 0.5 ? '' : 'negative'} sub={`gamma ${fmt(pos().net_gamma, 4)}`} />
                <Kpi k="Hedge check" v={(st().risk?.HEDGE || '—').split(':')[0]} sub={st().risk?.TIME ? `exit ${st().risk.TIME}` : ''} />
              </div>
            </div>
          </Show>

          <div style={S.panel}>
            <div style={{ 'margin-bottom': '10px' }}><Seg value={tab()} onChange={setTab} options={runTabs()} /></div>

            <Show when={tab() === 'depth'}>
              <Show when={(mkt()?.ce_bids || []).length || (mkt()?.pe_bids || []).length} fallback={<div style={S.muted}>No L1–L5 depth for this ATM yet.</div>}>
                <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(280px, 1fr))', gap: '14px' }}>
                  <Ladder title={`CE ${fmt(mkt()?.atm, 0)} · sell into bids`} bids={mkt()?.ce_bids} asks={mkt()?.ce_asks} />
                  <Ladder title={`PE ${fmt(mkt()?.atm, 0)} · sell into bids`} bids={mkt()?.pe_bids} asks={mkt()?.pe_asks} />
                </div>
              </Show>
            </Show>

            <Show when={tab() === 'position' && st()}>
              <table class="trade-position-table" style={S.table}>
                <thead><tr><th>Strike</th><th>Leg</th><th style={S.th}>Qty</th><th style={S.th}>Avg</th><th style={S.th}>Mark</th><th style={S.th}>Delta</th><th style={S.th}>Realized</th><th style={S.th}>P&L</th></tr></thead>
                <tbody>
                  <Show when={(pos().legs || []).length} fallback={<tr><td colSpan="8" style={S.muted}>No position yet</td></tr>}>
                    <For each={pos().legs || []}>{(l) => (
                      <tr>
                        <td>{fmt(l.strike, 0)}{l.strike === st().atm ? <span style={S.muted}> ATM</span> : ''}</td><td>{l.option_type}</td>
                        <td style={S.th} class={l.qty < 0 ? 'negative' : 'positive'}>{l.qty}</td><td style={S.th}>{fmt(l.avg_price)}</td><td style={S.th}>{fmt(l.mark)}</td>
                        <td style={S.th}>{fmt(l.delta, 3)}</td><td style={S.th}>₹{fmt(l.realized, 0)}</td><td style={S.th} class={pnlCls(l.pnl)}>₹{fmt(l.pnl, 0)}</td>
                      </tr>
                    )}</For>
                  </Show>
                </tbody>
              </table>
            </Show>

            <Show when={tab() === 'risk' && st()}>
              <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(320px, 1fr))', gap: '14px' }}>
                <div>
                  <div style={{ ...S.label, 'margin-bottom': '6px' }}>Minute-end risk</div>
                  <Show when={Object.keys(st().risk || {}).length} fallback={<div style={S.muted}>No check yet.</div>}>
                    <For each={Object.entries(st().risk || {})}>{([k, v]) => (
                      <div style={{ display: 'flex', gap: '10px', padding: '4px 0', 'border-bottom': '1px solid rgba(255,255,255,0.04)', 'font-size': '13px' }}>
                        <span style={{ ...S.muted, width: '56px' }}>{k}</span><span>{v}</span>
                      </div>
                    )}</For>
                  </Show>
                </div>
                <div>
                  <div style={{ ...S.label, 'margin-bottom': '6px' }}>Latest tranche decision <Show when={st().last_plan}><span class={st().last_plan.authorized ? 'positive' : 'negative'}>· {st().last_plan.authorized ? 'AUTHORIZED' : 'NO TRANCHE'}</span></Show></div>
                  <Show when={st().last_plan} fallback={<div style={S.muted}>None yet.</div>}>
                    <pre style={{ 'white-space': 'pre-wrap', 'font-size': '11.5px', margin: 0, color: 'rgba(255,255,255,0.8)' }}>{(st().last_plan.steps || []).join('\n')}</pre>
                  </Show>
                </div>
              </div>
            </Show>

            <Show when={tab() === 'trail' && st()}>
              <div style={{ 'max-height': '420px', overflow: 'auto' }}>
                <For each={[...(st().events || [])].reverse()}>{(e) => (
                  <div style={{ display: 'grid', 'grid-template-columns': '92px 70px 1fr', gap: '8px', padding: '4px 0', 'border-bottom': '1px solid rgba(255,255,255,0.04)', 'font-size': '12px' }}>
                    <span style={S.muted}>{e.time}</span>
                    <span style={{ color: kindColor[e.kind] || '#fff', 'font-weight': 700 }}>{e.kind}</span>
                    <span style={{ 'white-space': 'pre-wrap' }}>{e.text}</span>
                  </div>
                )}</For>
              </div>
            </Show>

            <Show when={tab() === 'fills' && st()}>
              <div style={{ 'max-height': '420px', overflow: 'auto' }}>
                <table class="trade-position-table" style={S.table}>
                  <thead><tr><th>Time</th><th>Role</th><th style={S.th}>#</th><th style={S.th}>Strike</th><th>Leg</th><th>Side</th><th style={S.th}>Qty</th><th style={S.th}>Price</th></tr></thead>
                  <tbody><For each={[...(st().fills || [])].reverse()}>{(f) => (
                    <tr><td>{f.time}</td><td>{f.role}</td><td style={S.th}>{f.tranche || '—'}</td><td style={S.th}>{fmt(f.strike, 0)}</td><td>{f.option_type}</td>
                      <td class={f.side === 'SELL' ? 'negative' : 'positive'}>{f.side}</td><td style={S.th}>{f.qty}</td><td style={S.th}>{fmt(f.price)}</td></tr>
                  )}</For></tbody>
                </table>
              </div>
            </Show>
          </div>
        </div>
      </div>
    </section>
  );
}
