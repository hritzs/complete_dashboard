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

// The fields GreekSoft returns (seen live 2026-10-27), labelled.
const MARGIN_LABELS = [
  ['spanExp', 'Margin used (SPAN + exposure)'],
  ['span', 'SPAN margin'],
  ['utilizedFund', 'Utilized fund'],
  ['realizedM2M', 'Realized M2M (day)'],
  ['unrealizedM2M', 'Unrealized M2M'],
  ['optSellCr', 'Option sell credit'],
  ['optBuyPrem', 'Option buy premium'],
  ['payIn', 'Pay-in'],
  ['payOut', 'Pay-out'],
];

function Card(props) {
  const v = () => Number(props.value);
  return (
    <div style={{ background: 'rgba(255,255,255,0.04)', 'border-radius': '6px', padding: '10px 14px', 'min-width': '170px' }}>
      <div style={{ 'font-size': '12px', opacity: 0.65 }}>{props.label}</div>
      <div class={props.signed ? (v() < 0 ? 'negative' : 'positive') : ''} style={{ 'font-size': '18px', 'font-weight': 600 }}>
        {props.value == null || props.value === '' ? '—' : `₹${v().toLocaleString('en-IN', { maximumFractionDigits: 2 })}`}
      </div>
    </div>
  );
}

const savedLimit = () => { try { return localStorage.getItem('broker.totalLimit') || ''; } catch (_) { return ''; } };

export default function BrokerRiskPanel() {
  const [limit, setLimitRaw] = createSignal(savedLimit());
  const setLimit = (v) => { setLimitRaw(v); try { localStorage.setItem('broker.totalLimit', v); } catch (_) { /* private mode */ } };
  const md = () => data()?.risk?.margin?.response?.data || {};
  // The broker's own total (availFund, the account's fund limit), else the
  // limit typed in below; used = utilizedFund (= SPAN + exposure).
  const brokerTotal = () => Number(md().availFund);
  const total = () => (brokerTotal() > 0 ? brokerTotal() : Number(limit()));
  const used = () => Number(md().utilizedFund ?? md().spanExp);
  const available = () => (total() > 0 && Number.isFinite(used()) ? total() - used() : null);
  const usedPct = () => (total() > 0 && Number.isFinite(used()) ? (100 * used()) / total() : null);
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
          <div style={{ display: 'flex', gap: '10px', 'flex-wrap': 'wrap', 'align-items': 'stretch', margin: '6px 0 10px' }}>
            <Show when={brokerTotal() > 0} fallback={
              <div style={{ background: 'rgba(255,255,255,0.04)', 'border-radius': '6px', padding: '10px 14px', 'min-width': '200px' }}>
                <div style={{ 'font-size': '12px', opacity: 0.65 }}>Total limit (₹) -- broker sent none; yours, saved in this browser</div>
                <input type="number" min="0" step="100000" value={limit()} placeholder="e.g. 150000000"
                  onInput={(e) => setLimit(e.currentTarget.value)} style={{ width: '170px', 'margin-top': '4px' }} />
              </div>}>
              <Card label="Total fund (broker availFund)" value={brokerTotal()} />
            </Show>
            <Card label="Used (utilized fund = SPAN + exposure)" value={Number.isFinite(used()) ? used() : null} />
            <Card label={usedPct() == null ? 'Available' : `Available (${(100 - usedPct()).toFixed(1)}% free)`} value={available()} signed />
          </div>
          <Show when={usedPct() != null}>
            <div style={{ height: '8px', background: 'rgba(255,255,255,0.08)', 'border-radius': '4px', margin: '0 0 14px', overflow: 'hidden' }}>
              <div style={{ height: '100%', width: `${Math.min(100, Math.max(0, usedPct()))}%`, background: usedPct() > 90 ? '#e66767' : usedPct() > 75 ? '#d9a441' : '#3fb27f' }} />
            </div>
          </Show>
          <div style={{ display: 'flex', gap: '10px', 'flex-wrap': 'wrap', margin: '6px 0 14px' }}>
            <Card label="Broker MTM (open positions)" value={data().risk?.mtm?.data?.[0]?.MTM} signed />
            <For each={MARGIN_LABELS}>{([k, label]) => (
              <Card label={label} value={data().risk?.margin?.response?.data?.[k]} signed={/M2M/.test(k)} />
            )}</For>
          </div>
          <div style={{ 'font-size': '12px', opacity: 0.6, margin: '-6px 0 10px' }}>
            Available = total fund (GreekSoft availFund) - utilized fund. Everything GreekSoft returns is listed below.
          </div>
          <div style={{ display: 'flex', gap: '16px', 'flex-wrap': 'wrap' }}>
            <Section title="MTM (getNetPositionMTM)" data={data().risk?.mtm} error={data().risk?.mtm_error} />
            <Section title="Margin (MarginDetailRequest, F&O)" data={data().risk?.margin} error={data().risk?.margin_error} />
          </div>
        </Show>
      </Show>
    </div>
  );
}
