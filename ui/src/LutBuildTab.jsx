import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';
import { postJSON } from './api.js';
import { box, muted } from './styles.js';
import LutActualBuild from './LutActualBuild.jsx';
import { fmt } from './format.js';

// LUT Entry Check, always on. The gateway re-evaluates on every chain update
// (100ms) and pushes a "lut_update" over the UI websocket; this tab renders
// it. The check itself is paper; the separate "Actual build" panel
// (LutActualBuild.jsx), when ARMED, turns the day's first YES into a real
// build.

const sgn = (v, d = 1) => {
  const n = Number(v);
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) < 1e-9) return 'now';
  return `${n > 0 ? '+' : ''}${n.toFixed(d)}%`;
};
const range = (from, to, d) =>
  `${Number(from) > 0 ? fmt(from, d) : '…'} – ${Number(to) > 0 ? fmt(to, d) : '…'}`;
const movePct = (cur, from, to) => {
  const c = Number(cur);
  if (!(c > 0)) return NaN;
  if (Number(from) > 0 && c < from) return ((from - c) / c) * 100;
  if (Number(to) > 0 && c >= to) return ((to - c) / c) * 100;
  return 0;
};

const MANUAL_GROUPS = [
  { id: 'mkt', title: 'Market (this minute)' },
  { id: 'day', title: 'Daily inputs (normally from the daily CSV)' },
];
const MANUAL_FIELDS = [
  { k: 'time', g: 'mkt', label: 'Minute (HH:MM, 09:16–13:30)', ph: '09:16' },
  { k: 'future', g: 'mkt', label: 'Future' },
  { k: 'ce_ltp', g: 'mkt', label: 'ATM CE LTP' },
  { k: 'pe_ltp', g: 'mkt', label: 'ATM PE LTP' },
  { k: 'straddle', g: 'mkt', label: 'ATM straddle' },
  { k: 'build_iv', g: 'mkt', label: 'Build IV raw (0.17 = 17%)' },
  { k: 'dte', g: 'mkt', label: 'DTE raw (days)' },
  { k: 'prev_straddle', g: 'day', label: 'Prev straddle' },
  { k: 'prev_future', g: 'day', label: 'Prev future' },
  { k: 'yesterday_iv', g: 'day', label: 'Yesterday adj IV' },
  { k: 'idv_pure', g: 'day', label: 'IDV pure' },
  { k: 'idv_with_prev', g: 'day', label: 'IDV with prev' },
  { k: 'norm_adj_chg', g: 'day', label: 'Norm adj IV change' },
];

const grid = { display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(230px, 1fr))', gap: '6px 16px' };

function KV(props) {
  return (
    <div>
      <span style={muted}>{props.k}</span>{' '}
      <strong>{props.v}</strong>
      <Show when={props.tag}> <span style={{ ...muted, 'margin-left': '4px' }}>[{props.tag}]</span></Show>
    </div>
  );
}

export default function LutBuildTab() {
  const [live, setLive] = createSignal(null);
  const [lastGrid, setLastGrid] = createSignal(null);
  const [minutes, setMinutes] = createSignal([]);
  const [params, setParams] = createSignal(null);
  const [lots, setLots] = createSignal('1');
  const [expiry, setExpiry] = createSignal('');
  const [og0916, setOg0916] = createSignal('');
  const [msg, setMsg] = createSignal('');
  const [man, setMan] = createSignal({});
  const [lastSell, setLastSell] = createSignal(null);
  const [levels, setLevels] = createSignal(null); // 09:16 .. 09:20 sell levels (sent until 09:30, then kept as last seen)
  // Two table sets run side by side: 'main' = the set driving today (its
  // first YES fires the armed build), 'alt' = the other one, shown and
  // recorded beside, never executed. The switch picks which one the whole
  // tab shows.
  const [tset, setTset] = createSignal('main');
  const [raw, setRaw] = createSignal(null); // last pushed payload, both sets
  const [stateRaw, setStateRaw] = createSignal(null);
  const [showOld, setShowOld] = createSignal(false); // "Show old algorithm": the old set runs only while on
  const loadShowOld = async () => {
    try { const d = await (await fetch('/api/lut/logic')).json(); setShowOld(!!d.show_old); } catch (_) { /* restarting */ }
  };
  const toggleShowOld = async (on) => {
    try {
      const d = await postJSON('/api/lut/logic', { show_old: on });
      setShowOld(!!d.show_old);
      if (!d.show_old && tset() === 'alt') switchSet('main');
    } catch (_) { /* restarting */ }
  };
  let seenMinutes = -1;
  let seenAlt = -1;
  const setManField = (k) => (e) => setMan((m) => ({ ...m, [k]: e.currentTarget.value }));
  const manualBody = (m) => {
    const out = {};
    for (const f of MANUAL_FIELDS) {
      const v = String(m[f.k] ?? '').trim();
      if (v === '') continue;
      out[f.k] = f.k === 'time' ? v : Number(v);
    }
    return out;
  };

  const loadState = async () => {
    try {
      const res = await fetch('/api/lut/state');
      const data = await res.json();
      const st = data.state || {};
      setStateRaw(st);
      applyState(st);
      if (!live()) setLive(st);
      if (st.config) {
        setLots(String(st.config.lots || 1));
        setExpiry(st.config.expiry || '');
        setOg0916(st.config.underlying_0916 ? String(st.config.underlying_0916) : '');
        const m = {};
        for (const f of MANUAL_FIELDS) m[f.k] = st.config.manual?.[f.k] != null ? String(st.config.manual[f.k]) : '';
        setMan(m);
      }
    } catch (_) { /* gateway restarting */ }
  };

  const applyState = (st) => {
    const alt = tset() === 'alt';
    setParams((alt ? st.alt_params : st.params) || null);
    setMinutes((alt ? st.alt_minutes : st.minutes) || []);
  };
  // The payload as the chosen set sees it: the other set's fields laid over.
  const view = (d) => {
    if (tset() !== 'alt' || !d.alt) return d;
    const a = d.alt;
    return {
      ...d, eval: a.eval, grid: a.grid, grid_0920: a.grid_0920, sell_range: a.sell_range, sell_range_0920: a.sell_range_0920,
      sell_levels: a.sell_levels, sell_levels_next: undefined, entry: a.entry, table_yes: a.table_yes, tables_error: a.tables_error,
      tables: a.tables, tables_name: a.name, alt: { ...a, tables: d.tables, name: d.tables_name, eval: d.eval }
    };
  };
  const switchSet = (v) => {
    setTset(v);
    setLastGrid(null); setLastSell(null); setLevels(null);
    if (stateRaw()) applyState(stateRaw());
    if (raw()) onUpdate({ detail: raw() });
  };
  const onUpdate = (e) => {
    setRaw(e.detail || {});
    const d = view(e.detail || {});
    setLive(d);
    if (d.grid) setLastGrid(d.grid);
    if (d.sell_range) setLastSell(d.sell_range);
    if (!d.eval) setLastSell(null);
    if (d.sell_levels) setLevels({ day: d.day, rows: d.sell_levels }); else if (levels() && levels().day !== d.day) setLevels(null);
    const altCount = raw()?.alt?.minutes_count ?? -1;
    if (raw()?.minutes_count !== seenMinutes || altCount !== seenAlt) {
      seenMinutes = raw()?.minutes_count;
      seenAlt = altCount;
      loadState();
    }
  };

  const saveConfig = async (manualOverride) => {
    setMsg('');
    try {
      const m = manualOverride || man();
      const data = await postJSON('/api/lut/config', { lots: Number(lots()) || 1, expiry: expiry().trim(), underlying_0916: Number(og0916()) || 0, manual: manualBody(m) });
      setMsg(data.success ? (Object.keys(manualBody(m)).length ? 'Saved — manual what-if shown (nothing recorded from it)' : 'Saved — live values') : (data.error || 'failed'));
      return data.success;
    } catch (err) { setMsg(String(err)); return false; }
  };
  const clearManual = async () => {
    const empty = {};
    for (const f of MANUAL_FIELDS) empty[f.k] = '';
    setMan(empty);
    setOg0916('');
    await saveConfig(empty);
  };

  const startNow = async () => {
    setMsg('');
    try {
      if (!(await saveConfig())) return;
      const res = await fetch('/api/lut/start-now', { method: 'POST' });
      const data = await res.json();
      setMsg(data.message || data.error || '');
    } catch (err) { setMsg(String(err)); }
  };

  onMount(() => {
    loadState();
    loadShowOld();
    window.addEventListener('lut_update', onUpdate);
  });
  onCleanup(() => window.removeEventListener('lut_update', onUpdate));

  const ev = () => live()?.eval;
  const inp = () => live()?.inputs || {};
  const qty = () => (Number(lots()) || 0) * (live()?.lot_size || 0);

  // ---------------------------------------------------------------- pieces

  // Live value of each manual field (shown as the box's placeholder).
  const liveOf = (k) => {
    const e = ev() || {};
    const i = inp();
    switch (k) {
      case 'future': return e.underlying ? fmt(e.underlying) : '';
      case 'ce_ltp': return e.ce_ltp ? fmt(e.ce_ltp) : '';
      case 'pe_ltp': return e.pe_ltp ? fmt(e.pe_ltp) : '';
      case 'straddle': return e.ce_ltp ? fmt((e.ce_ltp || 0) + (e.pe_ltp || 0)) : '';
      case 'build_iv': return e.build_iv ? fmt(e.build_iv, 4) : '';
      case 'dte': return e.raw_dte ? fmt(e.raw_dte, 3) : '';
      case 'prev_straddle': return i.PrevStraddle ? fmt(i.PrevStraddle) : '';
      case 'prev_future': return i.PrevFuture ? fmt(i.PrevFuture) : '';
      case 'yesterday_iv': return i.YesterdayIV ? fmt(i.YesterdayIV, 4) : '';
      case 'idv_pure': return i.IDVPure ? fmt(i.IDVPure, 4) : '';
      case 'idv_with_prev': return i.IDVWithPrev ? fmt(i.IDVWithPrev, 4) : '';
      case 'norm_adj_chg': return i.PrevStraddle ? fmt(i.NormalizedAdjChg, 5) : '';
      default: return '';
    }
  };
  const manualCount = () => Object.keys(manualBody(man())).length;

  const Manual = () => (
    <details style={{ ...box, border: live()?.manual ? '1px solid #ffb74d' : box.border }} open={live()?.manual || undefined}>
      <summary>
        <strong>Manual what-if inputs</strong>{' '}
        <span style={muted}>— check before the open / any scenario. Blank = live value (shown greyed). Never recorded, never an entry.</span>
        <Show when={live()?.manual}> <span style={{ color: '#ffb74d', 'font-weight': 700 }}>· ACTIVE: {(live()?.manual_fields || []).join(', ')}</span></Show>
      </summary>
      <For each={MANUAL_GROUPS}>
        {(g) => (
          <div style={{ 'margin-top': '10px' }}>
            <div style={muted}>{g.title}</div>
            <div style={{ display: 'flex', gap: '10px', 'flex-wrap': 'wrap', 'margin-top': '4px' }}>
              <For each={MANUAL_FIELDS.filter((f) => f.g === g.id)}>
                {(f) => (
                  <label class="control-block" style={{ 'min-width': '150px' }}>
                    <span class="control-label">{f.label}</span>
                    <input class="symbol-select" type={f.k === 'time' ? 'text' : 'number'} step="any"
                      placeholder={liveOf(f.k) || f.ph || ''} value={man()[f.k] ?? ''} onInput={setManField(f.k)}
                      style={{ 'border-color': String(man()[f.k] ?? '').trim() ? '#ffb74d' : undefined }} />
                  </label>
                )}
              </For>
            </div>
          </div>
        )}
      </For>
      <div style={{ ...muted, 'margin-top': '6px' }}>
        Prices: CE/PE LTP win over ATM straddle, which wins over build IV. ATM = future rounded to 50. Minute picks the table (09:16 … 09:19, 09:20+) and the DTE at that minute.
        The opening gap uses the 09:16 future override above; with a manual future and no 09:16 captured yet, the manual future is used.
      </div>
      <div style={{ display: 'flex', gap: '10px', 'margin-top': '8px', 'align-items': 'center', 'flex-wrap': 'wrap' }}>
        <button class="tab-btn" onClick={() => saveConfig()}>Apply manual values ({manualCount()})</button>
        <button class="tab-btn" onClick={clearManual} disabled={!live()?.manual && !manualCount()}>Clear manual — use live values</button>
        <span style={muted}>{msg()}</span>
      </div>
    </details>
  );

  const SellZone = (props) => (
    <Show when={props.r}>
      {(() => {
        const r = () => props.r;
        const rowGap = (g) => {
          const cur = r().straddle;
          if (cur >= g.straddle_from - 0.03 && cur <= g.straddle_to + 0.03) return ['IN ZONE — would sell', true]; // band ends are exact to the 0.05 tick
          const target = cur < g.straddle_from ? g.straddle_from : g.straddle_to;
          return [`needs ${target - cur >= 0 ? '+' : ''}${fmt(target - cur)} (${sgn(((target - cur) / cur) * 100)})`, false];
        };
        return (
          <div style={{ ...box, border: `1px solid ${r().in_zone ? 'rgba(46,125,50,0.8)' : 'rgba(255,255,255,0.10)'}` }}>
            <div class="section-title">Sell zone — {r().table} table ({r().time}){live()?.manual ? ' · what-if' : ''}</div>
            <div style={muted} title={`Gap / IDV / IV change fixed; ATM straddle scanned ₹${fmt(r().from, 0)}–₹${fmt(r().to, 0)} in steps of ${fmt(r().step)}`}>
              Future {fmt(r().future)} · ATM {fmt(r().strike, 0)} · DTE {fmt(r().raw_dte, 3)} · straddle now ₹{fmt(r().straddle)}
            </div>
            <Show when={(r().ranges || []).length} fallback={<div class="negative" style={{ 'margin-top': '6px' }}>No ATM straddle in that range gives a YES{r().skip ? ` (${r().skip})` : ''}.</div>}>
              <table class="trade-position-table" style={{ 'margin-top': '6px' }}>
                <thead><tr><th>Sell when ATM straddle is</th><th>Build IV (raw)</th><th>Adj build IV</th><th>TP bps</th><th>vs now ({fmt(r().straddle)})</th></tr></thead>
                <tbody>
                  <For each={r().ranges}>
                    {(g) => (
                      <tr style={{ background: rowGap(g)[1] ? 'rgba(46,125,50,0.25)' : '' }}>
                        <td><strong>₹{fmt(g.straddle_from)} – ₹{fmt(g.straddle_to)}</strong></td>
                        <td>{fmt(g.build_iv_from, 4)} – {fmt(g.build_iv_to, 4)}</td>
                        <td>{fmt(g.adj_iv_from, 4)} – {fmt(g.adj_iv_to, 4)}</td>
                        <td>{fmt(g.tp_bps_from, 2)} – {fmt(g.tp_bps_to, 2)}</td>
                        <td class={rowGap(g)[1] ? 'positive' : ''}>{rowGap(g)[0]}</td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </Show>
          </div>
        );
      })()}
    </Show>
  );

  // Opening sell levels: for each of the five opening tables, the ATM
  // straddle at / above which the LUT sells. Before the open from
  // yesterday's close, then from live prices; a minute already checked
  // shows the level as it was at that minute and what the LUT answered.
  const OpeningLevels = () => (
    <Show when={levels()?.rows?.length}>
      {(() => {
        const rows = () => levels().rows;
        const first = () => rows().find((l) => !l.done) || rows()[0];
        const band = (l) => {
          if (!(l.ranges || []).length) return <span class="negative">no YES level{l.skip ? ` (${l.skip})` : ''}</span>;
          return (
            <span>
              <strong>{l.above ? `₹${fmt(l.sell_from)} and above` : `₹${fmt(l.sell_from)} – ₹${fmt(l.sell_to)}`}</strong>
              <Show when={l.ranges.length > 1}>
                <span style={muted}> · also {l.ranges.slice(1).map((g) => `₹${fmt(g.straddle_from)}–₹${fmt(g.straddle_to)}`).join(', ')}</span>
              </Show>
            </span>
          );
        };
        const status = (l) => {
          if (l.done) {
            if (l.note) return [`checked: no check (${l.note})`, ''];
            return [l.allowed ? 'checked: YES — sold here' : 'checked: NO', l.allowed ? 'positive' : 'negative'];
          }
          if (!(l.ranges || []).length) return ['—', ''];
          if (l.in_zone) return ['IN ZONE — would sell', 'positive'];
          return [`needs ${l.need >= 0 ? '+' : ''}${fmt(l.need)} (${sgn((l.need / l.straddle) * 100)})`, ''];
        };
        return (
          <details style={{ ...box, border: '1px solid rgba(77,208,225,0.45)' }}>
            <summary style={{ cursor: 'pointer' }}>
              <strong>Opening sell levels — 09:16 to 09:20</strong>{live()?.manual ? ' · what-if' : ''}
              <span style={muted}>
                {' '}· {rows().map((l) => `${l.time} ${(l.ranges || []).length ? `₹${fmt(l.sell_from)}${l.above ? '+' : `–${fmt(l.sell_to)}`}` : '—'}${l.done ? (l.allowed ? ' ✓' : ' ✗') : (l.in_zone ? ' ●' : '')}`).join(' · ')} · click for details
              </span>
            </summary>
            <div style={{ ...muted, 'margin-top': '8px' }}>
              Basis: <strong>{first().basis}</strong> · future {fmt(first().future)} · ATM {fmt(first().strike, 0)} · ATM straddle {first().done ? 'then' : 'now'} ₹{fmt(first().straddle)}.
              Each minute has its own table, so the level differs minute to minute. Before the open these come from yesterday's close and move with the opening gap;
              from 09:15 they follow the live future, and from 09:16 the gap is fixed.
            </div>
            <table class="trade-position-table" style={{ 'margin-top': '6px' }}>
              <thead><tr><th>Minute</th><th>Sell when ATM straddle is</th><th>Build IV at that level</th><th>TP bps there</th><th>Future · ATM</th><th>Straddle</th><th>Status</th></tr></thead>
              <tbody>
                <For each={rows()}>
                  {(l) => (
                    <tr style={{ background: (l.done ? l.allowed : l.in_zone) ? 'rgba(46,125,50,0.25)' : '', opacity: l.done ? 0.8 : 1 }}>
                      <td><strong>{l.time}</strong></td>
                      <td>{band(l)}</td>
                      <td>{(l.ranges || []).length ? fmt(l.ranges[0].build_iv_from, 4) : '—'}</td>
                      <td>{(l.ranges || []).length ? fmt(l.ranges[0].tp_bps_from, 2) : '—'}</td>
                      <td>{fmt(l.future)} · {fmt(l.strike, 0)}</td>
                      <td>₹{fmt(l.straddle)}{l.done ? ' (at that minute)' : ''}</td>
                      <td class={status(l)[1]}>{status(l)[0]}</td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
            <Show when={live()?.sell_levels_next?.length}>
              <div style={{ 'margin-top': '12px' }}>
                <strong>Next week ({live().next_expiry}) — where the same tables would sell</strong>
                <div style={muted}>{live().next_basis}. Display only: next week's time to expiry, today's daily inputs.</div>
              </div>
              <table class="trade-position-table" style={{ 'margin-top': '6px' }}>
                <thead><tr><th>Minute</th><th>Sell when ATM straddle is</th><th>Build IV at that level</th><th>TP bps there</th><th>Future · ATM</th><th>Straddle</th><th>Status</th></tr></thead>
                <tbody>
                  <For each={live().sell_levels_next}>
                    {(l) => (
                      <tr style={{ background: l.in_zone ? 'rgba(46,125,50,0.25)' : '' }}>
                        <td><strong>{l.time}</strong></td>
                        <td>{band(l)}</td>
                        <td>{(l.ranges || []).length ? fmt(l.ranges[0].build_iv_from, 4) : '—'}</td>
                        <td>{(l.ranges || []).length ? fmt(l.ranges[0].tp_bps_from, 2) : '—'}</td>
                        <td>{fmt(l.future)} · {fmt(l.strike, 0)}</td>
                        <td>₹{fmt(l.straddle)}</td>
                        <td class={status({ ...l, done: false })[1]}>{status({ ...l, done: false })[0]}</td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </Show>
          </details>
        );
      })()}
    </Show>
  );

  const Verdict = () => (
    <div style={{ ...box, 'font-size': '16px', background: !ev() ? 'rgba(120,120,120,0.12)' : (ev().allowed ? 'rgba(46,125,50,0.18)' : (ev().skip ? 'rgba(120,120,120,0.12)' : 'rgba(198,40,40,0.14)')) }}>
      <strong>{live()?.now || '—'}</strong> · {live()?.phase || 'waiting for data'}
      <Show when={ev()}>
        {' '}· <Show when={live()?.next_check}>next check <strong>{live().next_check}:00</strong> · </Show>table <strong>{ev().stage}</strong> ·{' '}
        <Show when={!ev().skip} fallback={<span>no check: {ev().skip}</span>}>
          <strong class={ev().allowed ? 'positive' : 'negative'}>{ev().allowed ? 'YES — would sell' : 'NO — no entry'}</strong>
          <Show when={ev().preview}> <span style={muted}>(preview)</span></Show>
          <span style={muted}> · coord {ev().coord_text} · {ev().table_yes_cells_today} YES cell(s) reachable today in this table</span>
        </Show>
      </Show>
      <Show when={live()?.alt}>
        {(() => {
          const ans = (e) => !e ? '—' : (e.skip ? `no check (${e.skip})` : (e.allowed ? 'YES' : 'NO'));
          const cls = (e) => !e || e.skip ? '' : (e.allowed ? 'positive' : 'negative');
          return (
            <div style={{ 'font-size': '13px', 'margin-top': '4px' }}>
              <span style={muted}>Same prices, both table sets: </span>
              <strong>{live().tables_name}</strong> <strong class={cls(ev())}>{ans(ev())}</strong>
              <span style={muted}> · </span>
              <strong>{live().alt.name}</strong> <strong class={cls(live().alt.eval)}>{ans(live().alt.eval)}</strong>
              <Show when={live().alt.tables_error}><span class="negative"> ({live().alt.tables_error})</span></Show>
            </div>
          );
        })()}
      </Show>
      <Show when={live()?.inputs_error}><div class="negative" style={{ 'font-size': '13px' }}>Daily inputs: {live().inputs_error}</div></Show>
      <Show when={live()?.tables_error}><div class="negative" style={{ 'font-size': '13px' }}>Tables: {live().tables_error}</div></Show>
      <Show when={live()?.chain_error}><div class="negative" style={{ 'font-size': '13px' }}>Chain: {live().chain_error}</div></Show>
      <Show when={live()?.last_close}>{(mc) => (
        <div style={{ 'font-size': '13px', margin: '6px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.10)', display: 'flex', gap: '16px', 'flex-wrap': 'wrap' }}
          title="Every minute decision (LUT, hedge / SL / TP, Paper Sim) uses these prices: what minute HH:MM opens at, i.e. each leg's last trade before HH:MM:00 by exchange trade time (the close of the candle that just ended)">
          <strong>Minute open {mc().time}</strong>
          <span>ATM <strong>{fmt(mc().atm, 0)}</strong></span>
          <span>CE open <strong>{fmt(mc().ce)}</strong> <span style={muted}>trade {mc().ce_trade || '—'}</span></span>
          <span>PE open <strong>{fmt(mc().pe)}</strong> <span style={muted}>trade {mc().pe_trade || '—'}</span></span>
          <span>Straddle <strong>{fmt(mc().straddle)}</strong></span>
          <span>Syn fut <strong>{fmt(mc().syn_fut)}</strong> <span style={muted}>(ATM + CE − PE)</span></span>
          <span style={muted}>live LTP CE {fmt(mc().live_ce)} · PE {fmt(mc().live_pe)} · fut {fmt(mc().live_fut)}</span>
          <span style={muted}>taken +{mc().ready_ms} ms</span>
        </div>
      )}</Show>
      <Show when={live()?.preopen}>
        <div style={{ 'font-size': '13px', margin: '6px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(77,208,225,0.10)', border: '1px solid rgba(77,208,225,0.5)' }}>
          <strong style={{ color: '#4dd0e1' }}>Pre-open estimate</strong> — {live().preopen}. Shown only, never recorded or entered. Type a manual future (e.g. from GIFT Nifty) under "Manual what-if inputs" to move the strike, gap and sell zone.
        </div>
      </Show>
      <Show when={live()?.eval_error}><div class="negative" style={{ 'font-size': '13px' }}>{live().eval_error}</div></Show>
      <Show when={live()?.manual}><div style={{ 'font-size': '13px', color: '#ffb74d' }}>MANUAL WHAT-IF — overriding {(live().manual_fields || []).join(', ')}. Live minutes are still recorded from the feed.</div></Show>
    </div>
  );

  const Entry = () => {
    const d = () => live()?.entry;
    return (
      <Show when={d()} fallback={<div style={{ ...box, ...muted }}>No YES recorded yet today (first YES between 09:16 and 13:30 becomes the paper entry).</div>}>
        <div style={{ ...box, border: '1px solid #2e7d32', background: 'rgba(46,125,50,0.08)' }}>
          <div><strong>{d().source === 'started manually' ? 'Started manually' : 'First YES today'} — PAPER ENTRY (not executed)</strong> at {d().time} · table {d().stage}{d().source === 'started manually' ? ` · LUT said ${d().evaluation?.allowed ? 'YES' : 'NO'}` : ''}</div>
          <div style={{ 'margin-top': '6px' }}>
            NIFTY {d().expiry} <strong>{fmt(d().strike, 0)}</strong> — SELL CE {d().qty} (bid {fmt(d().ce_bid)} / ask {fmt(d().ce_ask)}) + SELL PE {d().qty} (bid {fmt(d().pe_bid)} / ask {fmt(d().pe_ask)})
          </div>
          <div>{d().lots * d().lot_size} qty ({d().lots} lot(s) × {d().lot_size}) · future {fmt(d().underlying)} · build IV {fmt(d().evaluation?.build_iv, 4)} · adjusted build IV {fmt(d().evaluation?.adj_build_iv, 4)}</div>
          <div>Exit: SL {fmt(d().sl_bps, 0)} bps = {fmt(d().sl_points)} pts (₹{fmt(d().sl_rupees, 0)}) · TP {fmt(d().tp_bps, 0)} bps = {fmt(d().tp_points)} pts (₹{fmt(d().tp_rupees, 0)})</div>
        </div>
      </Show>
    );
  };

  const Inputs = () => (
    <details style={box}>
      <summary style={{ cursor: 'pointer' }}>
        <strong>All inputs right now</strong>
        <span style={muted}> · weighted IDV {fmt(inp().WeightedIDV, 4)} · opening gap {fmt(live()?.norm_og, 4)} · adj IV change {fmt(inp().NormalizedAdjChg, 5)} · business days to expiry {live()?.bus_days ?? '—'}</span>
      </summary>
      <div style={{ ...muted, margin: '8px 0 4px' }}>Fixed for today{live()?.manual ? ' (manual values applied where set)' : ''}</div>
      <div style={grid}>
        <KV k="Date" v={live()?.day} />
        <KV k="Expiry" v={live()?.expiry || '—'} />
        <KV k="Lot size" v={live()?.lot_size || '—'} />
        <KV k="Size" v={`${qty()} qty (${lots()} lot(s))`} />
        <KV k="Prev straddle" v={fmt(inp().PrevStraddle)} />
        <KV k="Prev future" v={fmt(inp().PrevFuture)} />
        <KV k="Yesterday Adj IV" v={fmt(inp().YesterdayIV, 4)} />
        <KV k="IDV pure" v={fmt(inp().IDVPure, 4)} />
        <KV k="IDV with prev" v={fmt(inp().IDVWithPrev, 4)} />
        <KV k="Weighted IDV (0.2/0.5/0.3)" v={fmt(inp().WeightedIDV, 4)} />
        <KV k="Norm adj IV change" v={fmt(inp().NormalizedAdjChg, 5)} tag={ev()?.adj_label} />
        <KV k="09:16 future" v={fmt(live()?.underlying_0916)} tag={live()?.og_source} />
        <KV k="Norm opening gap" v={fmt(live()?.norm_og, 4)} tag={ev()?.og_label} />
        <KV k="Business days to expiry" v={live()?.bus_days ?? '—'} />
      </div>
      <Show when={ev() && !ev().skip}>
        <div style={{ ...muted, margin: '10px 0 4px' }}>{live()?.manual ? 'This what-if' : 'Live this tick'}</div>
        <div style={grid}>
          <KV k="Future" v={fmt(ev().underlying)} />
          <KV k="ATM strike (round to 50)" v={fmt(ev().strike, 0)} />
          <KV k="CE LTP @ATM" v={fmt(ev().ce_ltp)} />
          <KV k="PE LTP @ATM" v={fmt(ev().pe_ltp)} />
          <KV k="OTM leg used for IV" v={`${ev().otm_leg} @ ${fmt(ev().otm_price)}`} />
          <KV k="Time to expiry (years)" v={fmt(ev().t_years, 5)} />
          <KV k="DTE raw / trading" v={`${fmt(ev().raw_dte, 3)} / ${fmt(ev().trading_dte, 3)}`} tag={`DTE ${ev().dte_label}`} />
          <KV k="Weekend adj factor √(raw/trading)" v={fmt(ev().adj_factor, 4)} />
          <KV k="Build IV (raw, BS from OTM)" v={fmt(ev().build_iv, 4)} />
          <KV k="Build IV adjusted (entry + TP)" v={fmt(ev().adj_build_iv, 4)} tag={ev().bld_label} />
          <KV k="IV ratio = adj IV / IDV" v={fmt(ev().iv_ratio, 4)} tag={ev().iv_label} />
          <KV k="ATM straddle (CE LTP + PE LTP)" v={fmt(ev().straddle)} />
          <KV k="Straddle ratio = prev / now" v={fmt(ev().str_ratio, 4)} tag={ev().str_label} />
          <KV k="TP if entered now" v={`${fmt(ev().tp_bps, 2)} bps = ${fmt(ev().underlying * ev().tp_bps / 10000)} pts`} />
          <KV k="SL if entered now" v={`${fmt(params()?.sl_bps, 0)} bps = ${fmt(ev().underlying * (params()?.sl_bps || 0) / 10000)} pts`} />
          <KV k="Coordinate (DTE,IV,STR,BLD,OG,ADJ)" v={ev().coord_text} />
        </div>
      </Show>
    </details>
  );

  // Grid: rows IV-ratio buckets, columns straddle-ratio buckets.
  const Grid = (props) => (
    <Show when={props.g && props.g.cells}>
      <div style={box}>
        <div class="section-title">{props.g.table} table today — {props.g.yes_count} YES cell(s)</div>
        <div style={muted}>
          Rows: IV ratio (adj build IV / IDV) · Columns: straddle ratio (prev / now). DTE, opening gap and IV change are fixed for today.
          Green = would sell; outlined = where the market is now. In each YES cell: the % move in build IV / ATM straddle needed to get there (hover for exact ranges).
        </div>
        <div class="position-table-wrap" style={{ 'margin-top': '6px' }}>
          <table class="trade-position-table" style={{ 'font-size': '11px' }}>
            <thead>
              <tr>
                <th>IV ratio ↓ · Str ratio →</th>
                <For each={props.g.str_labels}>{(l) => <th>{l}</th>}</For>
              </tr>
            </thead>
            <tbody>
              <For each={props.g.cells}>
                {(row, i) => (
                  <tr>
                    <td><strong>{props.g.iv_labels[i()]}</strong></td>
                    <For each={row}>
                      {(c, j) => (
                        <td
                          title={c.reachable
                            ? `${props.g.iv_labels[i()]} / ${props.g.str_labels[j()]}${c.allowed ? ' — YES (build buckets ' + (c.yes_buckets || []).join(', ') + ')' : ' — NO'}\nbuild IV (raw) ${range(c.build_iv_from, c.build_iv_to, 4)}\nATM straddle ${range(c.straddle_from, c.straddle_to, 2)}\nmove: IV ${sgn(c.iv_move_pct)} · straddle ${sgn(c.straddle_move_pct)}`
                            : 'unreachable with today\'s IDV'}
                          style={{
                            background: !c.reachable ? 'rgba(0,0,0,0.25)' : (c.allowed ? 'rgba(46,125,50,0.35)' : 'rgba(198,40,40,0.10)'),
                            outline: c.current ? '2px solid #ffd54f' : 'none',
                            'text-align': 'center', 'white-space': 'nowrap', padding: '3px 4px'
                          }}
                        >
                          <Show when={c.reachable} fallback={<span style={{ opacity: 0.3 }}>·</span>}>
                            <Show when={c.allowed} fallback={<span style={{ opacity: 0.5 }}>{c.current ? 'NO ◀' : 'no'}</span>}>
                              <div><strong>YES{c.current ? ' ◀' : ''}</strong></div>
                              <div style={{ opacity: 0.8 }}>IV {sgn(c.iv_move_pct)}</div>
                              <div style={{ opacity: 0.8 }}>Str {sgn(c.straddle_move_pct)}</div>
                            </Show>
                          </Show>
                        </td>
                      )}
                    </For>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </div>
    </Show>
  );

  const Nearest = (props) => (
    <Show when={(props.rows || []).length}>
      <div style={box}>
        <div class="section-title">{props.title}: nearest YES — sells when BOTH hold at a minute's first tick</div>
        <div class="position-table-wrap">
          <table class="trade-position-table">
            <thead>
              <tr><th>Build IV (raw) needed</th><th>IV move</th><th>ATM straddle needed</th><th>Straddle move</th><th>Buckets (IV ratio / str / build)</th><th>Coord</th></tr>
            </thead>
            <tbody>
              <For each={props.rows}>
                {(c) => (
                  <tr>
                    <td>{range(c.build_iv_from, c.build_iv_to, 4)}</td>
                    <td class={c.iv_ok ? 'positive' : ''}>{sgn(movePct(ev()?.build_iv, c.build_iv_from, c.build_iv_to))}</td>
                    <td>{range(c.straddle_from, c.straddle_to, 2)}</td>
                    <td class={c.straddle_ok ? 'positive' : ''}>{sgn(movePct(ev()?.straddle, c.straddle_from, c.straddle_to))}</td>
                    <td>{c.iv_bucket} / {c.str_bucket} / {c.bld_bucket}</td>
                    <td>{c.coord}</td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </div>
    </Show>
  );

  const otherMins = () => {
    const st = stateRaw() || {};
    const list = (tset() === 'alt' ? st.minutes : st.alt_minutes) || [];
    const m = {};
    for (const e of list) m[e.time] = e;
    return m;
  };
  const otherAt = (t) => otherMins()[t];
  const otherName = () => `${(tset() === 'alt' ? raw()?.tables : raw()?.alt?.tables) || 'other'} tables`;
  const Timeline = () => (
    <details style={box}>
      <summary style={{ cursor: 'pointer' }}>
        <strong>Recorded minutes today</strong>
        <span style={muted}>{' '}· {minutes().length} minute(s) · {minutes().filter((e) => !e.skip && e.allowed).length} YES
          {' '}· row HH:MM = prices that minute opens at (last trade before HH:MM:00)</span>
      </summary>
      <Show when={minutes().length} fallback={<div style={{ ...muted, 'margin-top': '8px' }}>Nothing recorded yet.</div>}>
        <div class="position-table-wrap">
          <table class="trade-position-table">
            <thead>
              <tr>
                <th>Time</th><th>Table</th><th title="the other table set's answer on the same prices">{otherName()}</th><th>Answer</th><th title="synthetic future at the minute's open: the price minute HH:MM opens at: the last trade before HH:MM:00 (GreekSoft close of the candle that ended there)">Future (open)</th><th>ATM</th><th title="the price minute HH:MM opens at: the last trade before HH:MM:00 (GreekSoft close of the candle that ended there)">CE / PE open</th><th>Build IV</th><th>Adj build IV</th>
                <th>IV ratio</th><th title="the price minute HH:MM opens at: the last trade before HH:MM:00 (GreekSoft close of the candle that ended there)">Straddle (open)</th><th>Str ratio</th><th>DTE / OG / Adj</th><th>Coord</th><th>TP bps</th>
              </tr>
            </thead>
            <tbody>
              <For each={[...minutes()].reverse()}>
                {(e) => (
                  <tr>
                    <td>{e.time}</td>
                    <td>{e.stage}</td>
                    <td>{(() => { const o = otherAt(e.time); return !o ? '—' : (o.skip ? <span style={muted}>skip</span> : <strong class={o.allowed ? 'positive' : 'negative'}>{o.allowed ? 'YES' : 'NO'}</strong>); })()}</td>
                    <Show when={!e.skip} fallback={<td colSpan="12" style={{ opacity: 0.7 }}>no check: {e.skip}</td>}>
                      <td class={e.allowed ? 'positive' : 'negative'}><strong>{e.allowed ? 'YES' : 'NO'}</strong></td>
                      <td>{fmt(e.underlying)}</td>
                      <td>{fmt(e.strike, 0)}</td>
                      <td>{fmt(e.ce_ltp)} / {fmt(e.pe_ltp)}</td>
                      <td>{fmt(e.build_iv, 4)}</td>
                      <td>{fmt(e.adj_build_iv, 4)} <span style={muted}>{e.bld_label}</span></td>
                      <td>{fmt(e.iv_ratio, 4)} <span style={muted}>{e.iv_label}</span></td>
                      <td>{fmt(e.straddle)}</td>
                      <td>{fmt(e.str_ratio, 4)} <span style={muted}>{e.str_label}</span></td>
                      <td>{e.dte_label} / {e.og_label} / {e.adj_label}</td>
                      <td>{e.coord_text}</td>
                      <td>{fmt(e.tp_bps, 2)}</td>
                    </Show>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
    </details>
  );

  const Params = () => {
    const p = () => params() || {};
    const rows = (labels) => (labels || []).map((l, i) => `${i}: ${l}`).join('   ');
    return (
      <details style={box}>
        <summary><strong>All parameters</strong></summary>
        <div style={{ ...grid, 'margin-top': '8px' }}>
          <KV k="IDV weights (pure / with prev / yesterday IV)" v={(p().weights || []).join(' / ')} />
          <KV k="SL" v={`${p().sl_bps} bps of the future`} />
          <KV k="Weekend adjustment" v={`${p().weekend_adj_days} day`} />
          <KV k="Strike step" v={p().strike_step} />
          <KV k="Scan window" v={`${p().scan_from} – ${p().scan_to}`} />
          <KV k="Tables" v={(p().tables || []).join(', ')} />
          <KV k="Table YES cells" v={(live()?.table_yes || []).join(' / ')} />
        </div>
        <div style={{ 'margin-top': '8px' }}>
          <div style={muted}>IV ratio buckets</div><div>{rows(p().iv_labels)}</div>
          <div style={muted}>Straddle ratio buckets</div><div>{rows(p().str_labels)}</div>
          <div style={muted}>Build IV buckets (adjusted IV)</div><div>{rows(p().bld_labels)}</div>
          <div style={muted}>Opening gap buckets</div><div>{rows(p().og_labels)}</div>
          <div style={muted}>Adj IV change buckets</div><div>{rows(p().adj_labels)}</div>
        </div>
        <div style={{ 'margin-top': '8px' }}>
          <div style={muted}>TP bps by DTE (low at adjusted build IV 0.08 → high at 0.20)</div>
          <table class="trade-position-table">
            <thead><tr><th>DTE</th><For each={[1, 2, 3, 4, 5, 6, 7]}>{(d) => <th>{d}</th>}</For></tr></thead>
            <tbody>
              <tr><td>low</td><For each={p().tp_low_bps || []}>{(v) => <td>{v}</td>}</For></tr>
              <tr><td>high</td><For each={p().tp_high_bps || []}>{(v) => <td>{v}</td>}</For></tr>
            </tbody>
          </table>
        </div>
        <ul style={{ 'margin-top': '8px' }}>
          <For each={p().rules || []}>{(r) => <li>{r}</li>}</For>
        </ul>
      </details>
    );
  };

  return (
    <section class="tab-panel">
      <div class="panel-header">
        <div class="panel-title">
          LUT Entry Check
          <span style={{ 'font-size': '11px', padding: '2px 8px', 'border-radius': '10px', background: '#5d4037', 'margin-left': '8px', 'vertical-align': 'middle' }}
            title="Always on: re-evaluated on every chain tick. The first tick of each minute 09:16-13:30 is recorded; the first YES of the day is the paper entry.">
            paper check · real orders only via the armed Actual build
          </span>
        </div>
      </div>

      <div style={{ ...box, display: 'flex', gap: '14px 20px', 'align-items': 'flex-end', 'flex-wrap': 'wrap' }}>
        <div class="control-block">
          <span class="control-label">LUT logic</span>
          <div style={{ display: 'flex', gap: '6px', 'flex-wrap': 'wrap' }}>
            <button class={`tab-btn ${tset() === 'main' ? 'active' : ''}`} onClick={() => switchSet('main')}
              title="drives today: its first YES is the paper entry and fires the armed build">
              {raw()?.tables === 'new' ? 'New tables' : 'Old tables'} · drives today
            </button>
            <Show when={raw()?.alt}>
              <button class={`tab-btn ${tset() === 'alt' ? 'active' : ''}`} onClick={() => switchSet('alt')}
                title="shown and recorded beside on the same prices, never executed">
                {raw().alt.tables === 'new' ? 'New tables' : 'Old tables'} · beside, never executed
              </button>
            </Show>
          </div>
        </div>
        <div class="control-block" title="Off = the old tables are not loaded, evaluated, recorded or simulated">
          <span class="control-label">Old algorithm</span>
          <div style={{ display: 'flex', gap: '10px', height: '30px', 'align-items': 'center' }}>
            <label style={{ cursor: 'pointer' }}><input type="radio" name="lut-show-old" checked={!showOld()} onChange={() => toggleShowOld(false)} /> Off</label>
            <label style={{ cursor: 'pointer' }}><input type="radio" name="lut-show-old" checked={showOld()} onChange={() => toggleShowOld(true)} /> On</label>
          </div>
        </div>
        <span style={{ width: '1px', 'align-self': 'stretch', background: 'rgba(255,255,255,0.08)' }} />
        <label class="control-block">
          <span class="control-label">Paper size (qty)</span>
          <input class="symbol-select" type="text" inputmode="numeric" style={{ width: '90px' }}
            title={`Quantity per leg for the paper entry; whole lots of ${live()?.lot_size || 65}.`}
            value={(Number(lots()) || 0) * (live()?.lot_size || 65)}
            onChange={(e) => {
              const ls = live()?.lot_size || 65;
              const q = Number(e.currentTarget.value) || 0;
              const l = Math.max(1, Math.round(q / ls)); // nearest whole lot
              setLots(String(l));
              e.currentTarget.value = String(l * ls);
            }} />
        </label>
        <label class="control-block">
          <span class="control-label">Expiry</span>
          <input class="symbol-select" type="text" style={{ width: '130px' }} placeholder={live()?.expiry ? `nearest: ${live().expiry}` : 'nearest'} value={expiry()} onInput={(e) => setExpiry(e.currentTarget.value)} />
        </label>
        <label class="control-block" title="Blank = the 09:16 future captured live">
          <span class="control-label">09:16 future override</span>
          <input class="symbol-select" type="text" inputmode="decimal" step="0.05" style={{ width: '120px' }} placeholder="captured live" value={og0916()} onInput={(e) => setOg0916(e.currentTarget.value)} />
        </label>
        <div style={{ display: 'flex', gap: '6px' }}>
          <button class="tab-btn" onClick={saveConfig}>Save</button>
          <button class="tab-btn" onClick={startNow} title="Record a paper entry at this tick (not executed)">Paper entry now</button>
        </div>
        <Show when={tset() === 'alt'}><span style={{ color: '#ffb74d', 'font-size': '12px' }}>Showing the set that is NOT driving today</span></Show>
        <span style={muted}>{msg()}</span>
      </div>

      <Verdict />
      <LutActualBuild />
      <Entry />
      <SellZone r={lastSell()} />
      {/* ONE table: the live LUT table of the minute checked next (09:15-09:16
          shows 09:16, ... 09:20 onward 09:20+), kept after 13:30 with the
          market's cell still outlined -- no YES / NO is given after 13:30. */}
      <Grid g={lastGrid()} />
      <Show when={ev() && !ev().skip && !ev().allowed}>
        <Nearest title={`${ev().stage} table`} rows={ev().nearest_yes} />
      </Show>
      <OpeningLevels />
      <Inputs />
      <Manual />
      <Timeline />
      <Params />
    </section>
  );
}
