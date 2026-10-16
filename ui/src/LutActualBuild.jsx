import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// LUT ACTUAL BUILD -- REAL ORDERS. When ARMED, the LUT's first YES of the
// day fires the same build as an Automation entry (delta-neutral, full size,
// SL / TP / exit / hedge attached before any order), at the exact strike and
// expiry the LUT evaluated. Once per day; arming asks one OK/Cancel question.
// Backend: services/execution-gateway/internal/trading/lut_build.go

const box = { border: '1px solid rgba(255,255,255,0.10)', 'border-radius': '8px', padding: '10px 12px', margin: '10px 0' };
const muted = { opacity: 0.65, 'font-size': '12px' };

const FIELDS = [
  ['lots', 'Size (qty)', 'number'],
  ['order_lots_per_call', 'Qty per order (0 = auto)', 'number'],
  ['exit_time', 'Exit time', 'text'],
  ['sl_bps', 'SL bps (0 = LUT 14)', 'number'],
  ['tp_bps', 'TP bps (0 = LUT own)', 'number'],
  ['wing_pct', 'Wings % OTM (0 = no wings)', 'number'],
  ['hedge_div', 'Hedge div', 'number'],
  ['straddle_div', 'Straddle div', 'number'],
  ['buy_buffer', 'Buy buffer', 'number'],
  ['sell_buffer', 'Sell buffer', 'number'],
  ['broker_name', 'Broker', 'text'],
  ['account_id', 'Account', 'text'],
  ['user_id', 'User', 'text'],
  ['exchange_segment', 'Segment', 'text'],
  ['product_type', 'Product', 'text'],
];
const NUM = new Set(FIELDS.filter((f) => f[2] === 'number').map((f) => f[0]));
const FIELD = Object.fromEntries(FIELDS.map((f) => [f[0], f]));
// The form, grouped; the account fields are rarely touched and sit folded.
const GROUPS = [
  ['Size', ['lots', 'order_lots_per_call']],
  ['Risk', ['exit_time', 'sl_bps', 'tp_bps']],
  ['Hedge & wings', ['hedge_div', 'straddle_div', 'wing_pct']],
  ['Order pricing', ['buy_buffer', 'sell_buffer']],
];
const ACCOUNT = ['broker_name', 'account_id', 'user_id', 'exchange_segment', 'product_type'];
const groupBox = { border: '1px solid rgba(255,255,255,0.08)', 'border-radius': '8px', padding: '8px 10px', background: 'rgba(255,255,255,0.02)' };
const groupTitle = { 'font-size': '11px', 'text-transform': 'uppercase', 'letter-spacing': '0.06em', opacity: 0.6, 'margin-bottom': '6px' };
// Shown and typed as QUANTITY on this tab; the backend keeps lots.
const QTY = new Set(['lots', 'order_lots_per_call']);

const STATUS_BG = { BUILT: '#2e7d32', FIRING: '#c98500', FAILED: '#b23b3b', REFUSED: '#b23b3b', UNKNOWN: '#b23b3b' };

export default function LutActualBuild() {
  const [st, setSt] = createSignal(null);
  const [form, setForm] = createSignal(null);
  const [dirty, setDirty] = createSignal(false);
  const [msg, setMsg] = createSignal('');

  const load = async () => {
    try {
      const d = await (await fetch('/api/lut/build')).json();
      setSt(d);
      if (!dirty() && d.config) setForm(toQty(d.config, d.lot_size));
    } catch (e) { setMsg(String(e)); }
  };
  const lot = () => Number(st()?.lot_size) || 65;
  // config (lots) -> form (quantity)
  const toQty = (cfg, lotSize) => {
    const l = Number(lotSize) || 65;
    const f = { ...cfg };
    for (const k of QTY) f[k] = (Number(cfg[k]) || 0) * l;
    return f;
  };
  // form (quantity) -> request (lots); null + message when a quantity is not whole lots
  const body = () => {
    const f = { ...form() };
    for (const k of NUM) f[k] = Number(f[k]) || 0;
    for (const k of QTY) {
      const q = f[k];
      if (q < 0 || q % lot() !== 0) { setMsg(`${k === 'lots' ? 'Size' : 'Qty per order'} ${q} is not a multiple of the lot size ${lot()} (e.g. ${Math.max(1, Math.round(q / lot())) * lot()})`); return null; }
      f[k] = q / lot();
    }
    if (f.lots < 1) { setMsg(`Size must be at least ${lot()} (1 lot)`); return null; }
    return f;
  };
  const post = async (extra) => {
    setMsg('');
    const b = body();
    if (b == null) return false;
    try {
      const d = await (await fetch('/api/lut/build', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...b, ...extra }) })).json();
      if (!d.success) { setMsg(d.error || 'failed'); return false; }
      setDirty(false);
      setSt(d);
      setForm(toQty(d.config, d.lot_size));
      return true;
    } catch (e) { setMsg(String(e)); return false; }
  };
  // One OK/Cancel question; OK sends the backend's confirmation text itself.
  const confirmText = () => st()?.confirm_text || 'SELL LIVE';
  const armedConfirm = () =>
    window.confirm(`REAL ORDERS: the LUT's first YES will SELL ${form()?.lots} qty per leg (${(Number(form()?.lots) || 0) / lot()} lot(s) of ${lot()}) on ${form()?.broker_name}/${form()?.account_id}.\n\nSell?`) ? confirmText() : null;
  const save = async () => {
    if (st()?.config?.armed) {
      const c = armedConfirm();
      if (c == null) return;
      if (await post({ confirm: c })) setMsg('saved (still armed)');
      return;
    }
    if (await post({})) setMsg('saved');
  };
  const arm = async () => {
    const c = armedConfirm();
    if (c == null) return;
    if (await post({ arm: true, confirm: c })) setMsg('ARMED -- the next first LUT YES places real orders, then the LUT disarms itself');
  };
  const disarm = async () => { if (await post({ arm: false })) setMsg('disarmed -- the LUT stays paper only'); };
  // Test: one REAL build now through exactly the LUT's path (as if the LUT
  // said YES this minute). Never uses up the day's real entry.
  const testFire = async () => {
    if (dirty()) { setMsg('save the settings first'); return; }
    if (!window.confirm(`TEST FIRE -- REAL ORDERS NOW: a delta-neutral NIFTY straddle of ${form()?.lots} qty per leg (${(Number(form()?.lots) || 0) / lot()} lot(s)) at the LUT's current strike on ${form()?.broker_name}/${form()?.account_id}, exactly as a LUT YES would.\n\nSell now?`)) return;
    setMsg('');
    try {
      const d = await (await fetch('/api/lut/build/test', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ confirm: confirmText() }) })).json();
      setMsg(d.success ? `TEST fired at ${d.test?.fired_at}: ${d.test?.expiry} ${d.test?.strike} ${(d.test?.lots || 0) * lot()} qty -- watch the status below / Portfolio` : (d.error || 'failed'));
      load();
    } catch (e) { setMsg(String(e)); }
  };

  let timer;
  onMount(() => { load(); timer = setInterval(() => { if (!document.hidden) load(); }, 5000); });
  onCleanup(() => clearInterval(timer));

  const armed = () => !!st()?.config?.armed;
  const today = () => st()?.today;

  return (
    <div style={{ ...box, margin: '22px 0', padding: '0 12px 12px', border: `2px solid ${armed() ? '#e53935' : '#78909c'}`, 'border-radius': '10px', background: armed() ? 'rgba(229,57,53,0.10)' : 'rgba(120,144,156,0.08)', 'box-shadow': armed() ? '0 0 0 3px rgba(229,57,53,0.18)' : 'none', overflow: 'hidden' }}>
      <div style={{ background: armed() ? '#c62828' : '#455a64', color: '#fff', padding: '8px 14px', margin: '0 -12px', display: 'flex', 'align-items': 'center', gap: '12px', 'flex-wrap': 'wrap' }}>
        <strong style={{ 'font-size': '16px', 'letter-spacing': '0.04em' }}>⚠ ACTUAL BUILD — REAL ORDERS</strong>
        <strong style={{ 'font-size': '14px' }}>{armed() ? `ARMED since ${st()?.config?.armed_at}` : 'DISARMED — paper only'}</strong>
        <span style={{ 'font-size': '12px', opacity: 0.9 }}>the LUT check above is paper; this panel is the one that sells</span>
      </div>
      <div style={{ ...muted, 'padding-top': '8px' }}
        title="FIRST minute-end YES while ARMED -> delta-neutral full-size build at the LUT's strike / expiry, that YES only; then it DISARMS ITSELF and only monitors (arm again, or use another build, for a new entry)">
        Armed: the first minute-end LUT YES sells once at the LUT's strike, then it disarms itself.
      </div>

      <Show when={today()}>
        <div style={{ margin: '8px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(255,255,255,0.05)' }}>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: STATUS_BG[today().status] || '#455a64', 'margin-right': '8px' }}>{today().status}</span>
          today: LUT YES {today().minute} → fired {today().fired_at}{today().finished_at ? `, done ${today().finished_at}` : ''} · {today().expiry} {today().strike} · {today().lots * lot()} qty ({today().lots} lot(s)) · SL {Number(today().sl_bps).toFixed(2)} / TP {Number(today().tp_bps).toFixed(2)} bps · exit {today().exit_time} · {today().account}
          {today().trade_uid ? ` · trade ${today().trade_uid}` : ''}
          <Show when={today().error}><div style={{ color: '#e66767', 'margin-top': '4px' }}>{today().error}</div></Show>
          <Show when={today().message}><div style={muted}>{today().message}</div></Show>
        </div>
      </Show>
      <For each={st()?.tests || []}>{(tr) => (
        <div style={{ margin: '6px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(201,133,0,0.08)', 'font-size': '13px' }}>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: STATUS_BG[tr.status] || '#455a64', 'margin-right': '8px' }}>TEST {tr.status}</span>
          fired {tr.fired_at}{tr.finished_at ? `, done ${tr.finished_at}` : ''} · {tr.expiry} {tr.strike} · {tr.lots * lot()} qty ({tr.lots} lot(s)) · SL {Number(tr.sl_bps).toFixed(2)} / TP {Number(tr.tp_bps).toFixed(2)} bps · exit {tr.exit_time} · {tr.account}{tr.trade_uid ? ` · trade ${tr.trade_uid}` : ''}
          <Show when={tr.error}><div style={{ color: '#e66767' }}>{tr.error}</div></Show>
        </div>
      )}</For>
      <Show when={!today() && st()?.lut_yes_taken_today}>
        <div style={{ ...muted, margin: '6px 0' }}>A paper entry exists today — it does not block the real build: while ARMED, the first minute-end YES still sells (once).</div>
      </Show>

      <Show when={form()}>
        {(() => {
          const Field = (fp) => {
            const [k, label, type] = FIELD[fp.k];
            return (
              <label class="control-block" style={{ 'min-width': '0' }}>
                <span class="control-label" style={{ 'white-space': 'nowrap' }}>{label}</span>
                <input class="symbol-select" style={{ width: '100%', 'box-sizing': 'border-box' }} type={type === 'number' ? 'text' : type} inputmode={type === 'number' ? 'decimal' : undefined} step="any" value={form()[k] ?? ''}
                  onInput={(e) => { setForm({ ...form(), [k]: e.currentTarget.value }); setDirty(true); }} />
                <Show when={QTY.has(k)}>
                  <span style={{ ...muted, 'font-size': '11px' }}>
                    {k === 'order_lots_per_call' && !(Number(form()[k]) > 0) ? 'automatic' : `${(Number(form()[k]) || 0) / lot()} lot(s) × ${lot()}`}
                  </span>
                </Show>
              </label>
            );
          };
          return (
            <>
              <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(230px, 1fr))', gap: '10px', 'margin-top': '10px' }}>
                <For each={GROUPS}>{([title, keys]) => (
                  <div style={groupBox}>
                    <div style={groupTitle}>{title}</div>
                    <div style={{ display: 'grid', 'grid-template-columns': `repeat(${keys.length > 2 ? 3 : 2}, minmax(0, 1fr))`, gap: '8px' }}>
                      <For each={keys}>{(k) => <Field k={k} />}</For>
                    </div>
                  </div>
                )}</For>
              </div>
              <details style={{ ...groupBox, 'margin-top': '10px' }}>
                <summary style={{ cursor: 'pointer', ...groupTitle, 'margin-bottom': 0 }}>
                  Account · {form().broker_name}/{form().account_id} · {form().user_id} · {form().exchange_segment} · {form().product_type}
                </summary>
                <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(140px, 1fr))', gap: '8px', 'margin-top': '8px' }}>
                  <For each={ACCOUNT}>{(k) => <Field k={k} />}</For>
                </div>
              </details>
            </>
          );
        })()}
      </Show>

      <div style={{ display: 'flex', gap: '8px', 'align-items': 'center', 'margin-top': '12px', 'padding-top': '10px', 'border-top': '1px solid rgba(255,255,255,0.06)', 'flex-wrap': 'wrap' }}>
        <button class="tab-btn" onClick={save} disabled={!dirty()}>Save</button>
        <Show when={!armed()} fallback={<button class="tab-btn" onClick={disarm}>Disarm</button>}>
          <button class="tab-btn" style={{ background: '#b23b3b' }} onClick={arm}>Arm (REAL orders)</button>
        </Show>
        <button class="tab-btn" style={{ border: '1px solid #c98500' }} onClick={testFire}
          title="Sends ONE real build now through the exact LUT path (simulated YES at the current minute). Does not use up the day's real entry.">Test fire now (simulated YES)</button>
        <span style={muted}>{msg()}</span>
      </div>
      <Show when={(st()?.not_ready || []).filter((r) => r !== 'not armed').length}>
        <div style={{ color: '#e6a067', 'font-size': '12px', 'margin-top': '6px' }}>
          Would NOT fire right now: {(st()?.not_ready || []).filter((r) => r !== 'not armed').join(' · ')}
        </div>
      </Show>
      <Show when={(st()?.warnings || []).length}>
        <div style={{ color: '#d9a441', 'font-size': '12px', 'margin-top': '4px' }}>
          Note (does not stop the build): {(st()?.warnings || []).join(' · ')}
        </div>
      </Show>
    </div>
  );
}
