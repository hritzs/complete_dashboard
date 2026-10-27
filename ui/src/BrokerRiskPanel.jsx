import { createSignal, onMount, onCleanup, Show, For } from 'solid-js';

// Broker MTM & margin (GreekSoft's own numbers, read-only): GET
// /api/broker/risk -> getNetPositionMTM + MarginDetailRequest through the
// gateway's session. The response fields are not documented, so every
// field the broker returns is listed (numbers first); refreshed every 30 s.

const flatten = (v, prefix = '', out = []) => {
  if (v == null) return out;
  if (Array.isArray(v)) {
    v.forEach((x, i) => flatten(x, `${prefix}[${i}]`, out));
  } else if (typeof v === 'object') {
    for (const [k, x] of Object.entries(v)) flatten(x, prefix ? `${prefix}.${k}` : k, out);
  } else {
    out.push([prefix, v]);
  }
  return out;
};
const isNum = (x) => x !== '' && x !== null && !Number.isNaN(Number(x));
const show = (x) => (isNum(x) ? Number(x).toLocaleString('en-IN', { maximumFractionDigits: 2 }) : String(x));

function Section(props) {
  const rows = () => {
    const r = flatten(props.data).filter(([k]) => !/(appID|svcName|streaming_type|infoID|serverTime)$/i.test(k));
    return [...r.filter(([, v]) => isNum(v)), ...r.filter(([, v]) => !isNum(v))];
  };
  return (
    <div style={{ flex: '1 1 380px', 'min-width': '0' }}>
      <div style={{ 'font-weight': 600, margin: '4px 0' }}>{props.title}</div>
      <Show when={!props.error} fallback={<div style={{ color: '#ef4444', 'font-size': '12px' }}>❌ {props.error}</div>}>
        <Show when={rows().length} fallback={<div style={{ opacity: 0.6, 'font-size': '12px' }}>no fields returned</div>}>
          <div style={{ 'max-height': '260px', overflow: 'auto' }}>
            <table class="positions-table" style={{ 'font-size': '12px' }}>
              <tbody>
                <For each={rows()}>{([k, v]) => (
                  <tr><td style={{ opacity: 0.75 }}>{k}</td><td class={isNum(v) && Number(v) < 0 ? 'negative' : ''} style={{ 'text-align': 'right' }}>{show(v)}</td></tr>
                )}</For>
              </tbody>
            </table>
          </div>
        </Show>
      </Show>
    </div>
  );
}

export default function BrokerRiskPanel() {
  const [data, setData] = createSignal(null);
  const [err, setErr] = createSignal('');
  const [busy, setBusy] = createSignal(false);
  const load = async () => {
    setBusy(true);
    try {
      const d = await (await fetch('/api/broker/risk', { headers: { Accept: 'application/json' } })).json();
      if (d.success) { setData(d); setErr(''); } else setErr(d.error || 'fetch failed');
    } catch (e) { setErr(String(e?.message || e)); }
    setBusy(false);
  };
  onMount(() => { load(); const t = setInterval(load, 30000); onCleanup(() => clearInterval(t)); });
  return (
    <div style={{ 'margin-bottom': '14px' }}>
      <div class="positions-header">
        <div class="positions-title">Broker MTM &amp; margin</div>
        <span style={{ 'font-size': '12px', opacity: 0.6, 'margin-left': '8px' }}>
          GreekSoft's own numbers, read-only{data()?.time ? ` · ${data().account} · ${data().time}` : ''}
        </span>
        <button class="btn" style={{ 'margin-left': 'auto' }} onClick={load} disabled={busy()}>{busy() ? 'Refreshing…' : 'Refresh'}</button>
      </div>
      <Show when={!err()} fallback={<div class="empty-state" style={{ color: '#ef4444' }}>❌ {err()}</div>}>
        <Show when={data()} fallback={<div class="empty-state">🔄 Fetching…</div>}>
          <div style={{ display: 'flex', gap: '16px', 'flex-wrap': 'wrap' }}>
            <Section title="MTM (getNetPositionMTM)" data={data().risk?.mtm} error={data().risk?.mtm_error} />
            <Section title="Margin (MarginDetailRequest, F&O)" data={data().risk?.margin} error={data().risk?.margin_error} />
          </div>
        </Show>
      </Show>
    </div>
  );
}
