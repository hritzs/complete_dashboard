import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';
import LutActualBuild from './LutActualBuild.jsx';

// LUT Entry Check, always on. The gateway re-evaluates on every chain update
// (100ms) and pushes a "lut_update" over the UI websocket; this tab renders
// it. The check itself is paper; the separate "Actual build" panel
// (LutActualBuild.jsx), when ARMED, turns the day's first YES into a real
// build.

const fmt = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? n.toFixed(d) : '—';
};
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

const box = { border: '1px solid rgba(255,255,255,0.10)', 'border-radius': '8px', padding: '10px 12px', margin: '10px 0' };
const grid = { display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(230px, 1fr))', gap: '6px 16px' };
const muted = { opacity: 0.65, 'font-size': '12px' };

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
  const [lastGrid0920, setLastGrid0920] = createSignal(null);
  const [minutes, setMinutes] = createSignal([]);
  const [params, setParams] = createSignal(null);
  const [lots, setLots] = createSignal('1');
  const [expiry, setExpiry] = createSignal('');
  const [og0916, setOg0916] = createSignal('');
  const [msg, setMsg] = createSignal('');
  const [man, setMan] = createSignal({});
  const [lastSell, setLastSell] = createSignal(null);
  const [lastSell0920, setLastSell0920] = createSignal(null);
  let seenMinutes = -1;
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
      setParams(st.params || null);
      setMinutes(st.minutes || []);
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

  const onUpdate = (e) => {
    const d = e.detail || {};
    setLive(d);
    if (d.grid) setLastGrid(d.grid);
    if (d.grid_0920) setLastGrid0920(d.grid_0920); else if (d.grid) setLastGrid0920(null);
    if (d.sell_range) setLastSell(d.sell_range);
    if (d.sell_range_0920) setLastSell0920(d.sell_range_0920); else if (d.sell_range) setLastSell0920(null);
    if (!d.eval) { setLastSell(null); setLastSell0920(null); }
    if (d.minutes_count !== seenMinutes) {
      seenMinutes = d.minutes_count;
      loadState();
    }
  };

  const saveConfig = async (manualOverride) => {
    setMsg('');
    try {
      const m = manualOverride || man();
      const res = await fetch('/api/lut/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ lots: Number(lots()) || 1, expiry: expiry().trim(), underlying_0916: Number(og0916()) || 0, manual: manualBody(m) })
      });
      const data = await res.json();
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
          if (cur >= g.straddle_from - r().step / 2 && cur <= g.straddle_to + r().step / 2) return ['IN ZONE — would sell', true];
          const target = cur < g.straddle_from ? g.straddle_from : g.straddle_to;
          return [`needs ${target - cur >= 0 ? '+' : ''}${fmt(target - cur)} (${sgn(((target - cur) / cur) * 100)})`, false];
        };
        return (
          <div style={{ ...box, border: `1px solid ${r().in_zone ? 'rgba(46,125,50,0.8)' : 'rgba(255,255,255,0.10)'}` }}>
            <div class="section-title">Sell zone — {r().table} table ({r().time}){live()?.manual ? ' · what-if' : ''}</div>
            <div style={muted}>
              Future {fmt(r().future)} · ATM {fmt(r().strike, 0)} · DTE {fmt(r().raw_dte, 3)} · gap / IDV / IV change fixed — ATM straddle scanned ₹{fmt(r().from, 0)}–₹{fmt(r().to, 0)} in steps of {fmt(r().step)}.
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
      <Show when={live()?.inputs_error}><div class="negative" style={{ 'font-size': '13px' }}>Daily inputs: {live().inputs_error}</div></Show>
      <Show when={live()?.tables_error}><div class="negative" style={{ 'font-size': '13px' }}>Tables: {live().tables_error}</div></Show>
      <Show when={live()?.chain_error}><div class="negative" style={{ 'font-size': '13px' }}>Chain: {live().chain_error}</div></Show>
      <Show when={live()?.last_close}>{(mc) => (
        <div style={{ 'font-size': '13px', margin: '6px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.10)', display: 'flex', gap: '16px', 'flex-wrap': 'wrap' }}
          title="Every minute-end decision (LUT, hedge / SL / TP, Paper Sim) uses these candle closes: each leg's last trade before HH:MM:00 by exchange trade time">
          <strong>Minute close {mc().time}</strong>
          <span>ATM <strong>{fmt(mc().atm, 0)}</strong></span>
          <span>CE close <strong>{fmt(mc().ce)}</strong> <span style={muted}>trade {mc().ce_trade || '—'}</span></span>
          <span>PE close <strong>{fmt(mc().pe)}</strong> <span style={muted}>trade {mc().pe_trade || '—'}</span></span>
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
          <div>{d().lots} lot(s) × {d().lot_size} · future {fmt(d().underlying)} · build IV {fmt(d().evaluation?.build_iv, 4)} · adjusted build IV {fmt(d().evaluation?.adj_build_iv, 4)}</div>
          <div>Exit: SL {fmt(d().sl_bps, 0)} bps = {fmt(d().sl_points)} pts (₹{fmt(d().sl_rupees, 0)}) · TP {fmt(d().tp_bps, 0)} bps = {fmt(d().tp_points)} pts (₹{fmt(d().tp_rupees, 0)})</div>
        </div>
      </Show>
    );
  };

  const Inputs = () => (
    <div style={box}>
      <div class="section-title">All inputs right now</div>
      <div style={{ ...muted, margin: '4px 0' }}>Fixed for today{live()?.manual ? ' (manual values applied where set)' : ''}</div>
      <div style={grid}>
        <KV k="Date" v={live()?.day} />
        <KV k="Expiry" v={live()?.expiry || '—'} />
        <KV k="Lot size" v={live()?.lot_size || '—'} />
        <KV k="Size" v={`${lots()} lot(s) = ${qty()} qty`} />
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
    </div>
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

  const Timeline = () => (
    <div style={box}>
      <div class="section-title">Recorded minutes today (first tick of each minute, 09:16–13:30)</div>
      <Show when={minutes().length} fallback={<div style={muted}>Nothing recorded yet.</div>}>
        <div class="position-table-wrap">
          <table class="trade-position-table">
            <thead>
              <tr>
                <th>Time</th><th>Table</th><th>Answer</th><th title="synthetic future from the minute's candle closes">Future (close)</th><th>ATM</th><th title="last trade before the minute boundary">CE / PE close</th><th>Build IV</th><th>Adj build IV</th>
                <th>IV ratio</th><th>Straddle (close)</th><th>Str ratio</th><th>DTE / OG / Adj</th><th>Coord</th><th>TP bps</th>
              </tr>
            </thead>
            <tbody>
              <For each={[...minutes()].reverse()}>
                {(e) => (
                  <tr>
                    <td>{e.time}</td>
                    <td>{e.stage}</td>
                    <Show when={!e.skip} fallback={<td colSpan="12"style={{ opacity: 0.7 }}>no check: {e.skip}</td>}>
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
    </div>
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
          <span style={{ 'font-size': '12px', padding: '2px 8px', 'border-radius': '10px', background: '#5d4037', 'margin-left': '8px' }}>YES / NO check — real orders only via the armed Actual build below</span>
        </div>
        <div class="panel-subtitle">
          Always on: re-evaluated on every chain tick and shown live. The first tick of each minute 09:16–13:30 is recorded; the first YES of the day is the paper entry.
        </div>
      </div>

      <div style={{ display: 'flex', gap: '12px', 'flex-wrap': 'wrap', 'align-items': 'flex-end' }}>
        <label class="control-block">
          <span class="control-label">Size (lots)</span>
          <input class="symbol-select" type="text" inputmode="decimal" min="1" value={lots()} onInput={(e) => setLots(e.currentTarget.value)} />
        </label>
        <label class="control-block">
          <span class="control-label">Expiry (blank = nearest)</span>
          <input class="symbol-select" type="text" placeholder={live()?.expiry ? `nearest: ${live().expiry}` : 'nearest'} value={expiry()} onInput={(e) => setExpiry(e.currentTarget.value)} />
        </label>
        <label class="control-block">
          <span class="control-label">09:16 future override (blank = captured live)</span>
          <input class="symbol-select" type="text" inputmode="decimal" step="0.05" value={og0916()} onInput={(e) => setOg0916(e.currentTarget.value)} />
        </label>
        <button class="tab-btn" onClick={saveConfig}>Save settings</button>
        <button class="tab-btn" onClick={startNow} title="Record a paper entry at this tick (not executed)">Start now</button>
        <span style={muted}>{msg()}</span>
      </div>

      <Manual />
      <Verdict />
      <SellZone r={lastSell()} />
      <Show when={lastSell0920() && lastSell()?.table !== '09:20+'}><SellZone r={lastSell0920()} /></Show>
      <Entry />
      <LutActualBuild />
      <Inputs />
      <Grid g={lastGrid()} />
      <Show when={ev() && !ev().skip && !ev().allowed}>
        <Nearest title={`${ev().stage} table`} rows={ev().nearest_yes} />
        <Show when={ev().stage !== '09:20+'}>
          <Nearest title="09:20+ table" rows={ev().nearest_yes_0920} />
        </Show>
      </Show>
      <Grid g={lastGrid0920()} />
      <Timeline />
      <Params />
    </section>
  );
}
