import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// LUT ACTUAL BUILD -- REAL ORDERS. When ARMED, the LUT's first YES of the
// day fires the same build as an Automation entry (delta-neutral, full size,
// SL / TP / exit / hedge attached before any order), at the exact strike and
// expiry the LUT evaluated. Once per day; arming asks one OK/Cancel question.
// Backend: services/execution-gateway/internal/trading/lut_build.go

const box = { border: '1px solid rgba(255,255,255,0.10)', 'border-radius': '8px', padding: '10px 12px', margin: '10px 0' };
const muted = { opacity: 0.65, 'font-size': '12px' };

const FIELDS = [
  ['lots', 'Size (lots)', 'number'],
  ['order_lots_per_call', 'Lots per order', 'number'],
  ['exit_time', 'Exit time', 'text'],
  ['sl_bps', 'SL bps (0 = LUT 14)', 'number'],
  ['tp_bps', 'TP bps (0 = LUT own)', 'number'],
  ['wing_pct', 'Wing %', 'number'],
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
      if (!dirty() && d.config) setForm({ ...d.config });
    } catch (e) { setMsg(String(e)); }
  };
  const body = () => {
    const f = { ...form() };
    for (const k of NUM) f[k] = Number(f[k]) || 0;
    return f;
  };
  const post = async (extra) => {
    setMsg('');
    try {
      const d = await (await fetch('/api/lut/build', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...body(), ...extra }) })).json();
      if (!d.success) { setMsg(d.error || 'failed'); return false; }
      setDirty(false);
      setSt(d);
      setForm({ ...d.config });
      return true;
    } catch (e) { setMsg(String(e)); return false; }
  };
  // One OK/Cancel question; OK sends the backend's confirmation text itself.
  const confirmText = () => st()?.confirm_text || 'SELL LIVE';
  const armedConfirm = () =>
    window.confirm(`REAL ORDERS: the LUT's first YES will SELL ${form()?.lots} lot(s) on ${form()?.broker_name}/${form()?.account_id}.\n\nSell?`) ? confirmText() : null;
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
    if (await post({ arm: true, confirm: c })) setMsg('ARMED -- the next first LUT YES places real orders');
  };
  const disarm = async () => { if (await post({ arm: false })) setMsg('disarmed -- the LUT stays paper only'); };
  // Test: one REAL build now through exactly the LUT's path (as if the LUT
  // said YES this minute). Never uses up the day's real entry.
  const testFire = async () => {
    if (dirty()) { setMsg('save the settings first'); return; }
    if (!window.confirm(`TEST FIRE -- REAL ORDERS NOW: a delta-neutral ${form()?.lots}-lot NIFTY straddle at the LUT's current strike on ${form()?.broker_name}/${form()?.account_id}, exactly as a LUT YES would.\n\nSell now?`)) return;
    setMsg('');
    try {
      const d = await (await fetch('/api/lut/build/test', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ confirm: confirmText() }) })).json();
      setMsg(d.success ? `TEST fired at ${d.test?.fired_at}: ${d.test?.expiry} ${d.test?.strike} ${d.test?.lots} lot(s) -- watch the status below / Portfolio` : (d.error || 'failed'));
      load();
    } catch (e) { setMsg(String(e)); }
  };

  let timer;
  onMount(() => { load(); timer = setInterval(() => { if (!document.hidden) load(); }, 5000); });
  onCleanup(() => clearInterval(timer));

  const armed = () => !!st()?.config?.armed;
  const today = () => st()?.today;

  return (
    <div style={{ ...box, border: `1px solid ${armed() ? '#b23b3b' : 'rgba(255,255,255,0.18)'}`, background: armed() ? 'rgba(178,59,59,0.08)' : '' }}>
      <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', 'flex-wrap': 'wrap' }}>
        <strong>Actual build — REAL ORDERS</strong>
        <span style={{ 'font-size': '12px', padding: '2px 8px', 'border-radius': '10px', background: armed() ? '#b23b3b' : '#455a64' }}>
          {armed() ? `ARMED since ${st()?.config?.armed_at}` : 'DISARMED — paper only'}
        </span>
        <span style={muted}>FIRST minute-end YES while ARMED → delta-neutral full-size build at the LUT's strike/expiry · that YES only, once per day</span>
      </div>

      <Show when={today()}>
        <div style={{ margin: '8px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(255,255,255,0.05)' }}>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: STATUS_BG[today().status] || '#455a64', 'margin-right': '8px' }}>{today().status}</span>
          today: LUT YES {today().minute} → fired {today().fired_at}{today().finished_at ? `, done ${today().finished_at}` : ''} · {today().expiry} {today().strike} · {today().lots} lot(s) · SL {Number(today().sl_bps).toFixed(2)} / TP {Number(today().tp_bps).toFixed(2)} bps · exit {today().exit_time} · {today().account}
          {today().trade_uid ? ` · trade ${today().trade_uid}` : ''}
          <Show when={today().error}><div style={{ color: '#e66767', 'margin-top': '4px' }}>{today().error}</div></Show>
          <Show when={today().message}><div style={muted}>{today().message}</div></Show>
        </div>
      </Show>
      <For each={st()?.tests || []}>{(tr) => (
        <div style={{ margin: '6px 0', padding: '6px 10px', 'border-radius': '6px', background: 'rgba(201,133,0,0.08)', 'font-size': '13px' }}>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: STATUS_BG[tr.status] || '#455a64', 'margin-right': '8px' }}>TEST {tr.status}</span>
          fired {tr.fired_at}{tr.finished_at ? `, done ${tr.finished_at}` : ''} · {tr.expiry} {tr.strike} · {tr.lots} lot(s) · SL {Number(tr.sl_bps).toFixed(2)} / TP {Number(tr.tp_bps).toFixed(2)} bps · exit {tr.exit_time} · {tr.account}{tr.trade_uid ? ` · trade ${tr.trade_uid}` : ''}
          <Show when={tr.error}><div style={{ color: '#e66767' }}>{tr.error}</div></Show>
        </div>
      )}</For>
      <Show when={!today() && st()?.lut_yes_taken_today}>
        <div style={{ ...muted, margin: '6px 0' }}>A paper entry exists today — it does not block the real build: while ARMED, the first minute-end YES still sells (once).</div>
      </Show>

      <Show when={form()}>
        <div style={{ display: 'flex', gap: '10px', 'flex-wrap': 'wrap', 'margin-top': '8px' }}>
          <For each={FIELDS}>{([k, label, type]) => (
            <label class="control-block">
              <span class="control-label">{label}</span>
              <input class="symbol-select" style={{ width: type === 'number' ? '90px' : '110px' }} type={type} step="any" value={form()[k] ?? ''}
                onInput={(e) => { setForm({ ...form(), [k]: e.currentTarget.value }); setDirty(true); }} />
            </label>
          )}</For>
        </div>
      </Show>

      <div style={{ display: 'flex', gap: '8px', 'align-items': 'center', 'margin-top': '8px', 'flex-wrap': 'wrap' }}>
        <button class="tab-btn" onClick={save}>Save</button>
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
