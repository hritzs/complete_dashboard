import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// Time to expiry the chain greeks -- and so every hedge -- use right now, how it is
// made up, and the ATM IV / greeks on that clock next to a normal day's clock
// (owner, 2026-11-06). GET /api/clock. Shown on the Portfolio and Muhurat tabs.
const n = (v, d = 2) => (v === undefined || v === null || Number.isNaN(Number(v)) ? '—' : Number(v).toFixed(d));
const pct = (v) => (v ? (v * 100).toFixed(2) + '%' : '—');
const th = { padding: '4px 8px', 'text-align': 'right', 'font-size': '11px', color: 'rgba(255,255,255,0.6)', 'font-weight': 600, 'white-space': 'nowrap' };
const td = { padding: '4px 8px', 'text-align': 'right', 'font-size': '12px', 'white-space': 'nowrap' };
const left = { 'text-align': 'left' };

export default function ExpiryClock(props) {
  const [c, setC] = createSignal(null);
  let t;
  const load = async () => {
    if (document.hidden) return;
    try { setC(await (await fetch(`/api/clock?symbol=${encodeURIComponent(props?.symbol || 'NIFTY')}`)).json()); } catch (_) { /* restarting */ }
  };
  onMount(() => { load(); t = setInterval(load, 5000); });
  onCleanup(() => clearInterval(t));
  return (
    <div style={{ padding: '10px 12px', border: '1px solid rgba(255,255,255,0.08)', 'border-radius': '10px', margin: '8px 0', background: 'rgba(255,255,255,0.02)' }}>
      <div style={{ display: 'flex', gap: '10px', 'align-items': 'center', 'flex-wrap': 'wrap' }}>
        <strong>Time to expiry used by the greeks &amp; hedging</strong>
        <Show when={c()?.session} fallback={<span style={{ padding: '1px 8px', 'border-radius': '8px', background: '#455a64', 'font-size': '11px' }}>normal day 09:15–15:40</span>}>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: '#c98500', 'font-size': '11px' }}>
            {c().session.name} {c().session.open}–{c().session.close} = the whole day
          </span>
        </Show>
        <span style={{ 'font-size': '11px', color: 'rgba(255,255,255,0.55)' }}>{c()?.clock}</span>
        <span style={{ 'margin-left': 'auto', 'font-size': '11px', color: 'rgba(255,255,255,0.45)' }}>{c()?.now}</span>
      </div>
      <Show when={c()?.success} fallback={<div style={{ 'font-size': '12px', color: '#e6a067', 'margin-top': '4px' }}>{c()?.error || 'loading…'}</div>}>
        <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
          <thead><tr>
            <th style={{ ...th, ...left }}>Expiry</th>
            <th style={th} title="calendar days from today to the expiry day">Calendar days</th>
            <th style={th} title="today's part of a day still to come">Today's part</th>
            <th style={th}>Min left today</th>
            <th style={{ ...th, color: '#ffd27a' }} title="calendar days + today's part: the DTE the chain greeks and every hedge use">DTE used (days)</th>
            <th style={th} title="DTE / 365, the T in Black-76">T (years)</th>
            <th style={th} title="the same time on a normal 09:15-15:40 day">Normal-day DTE</th>
            <th style={th}>ATM</th>
            <th style={th}>Straddle</th>
            <th style={{ ...th, color: '#ffd27a' }} title="ATM IV from the OTM leg's price on the DTE used">IV used</th>
            <th style={th} title="the same prices on the normal-day clock">IV normal clock</th>
            <th style={th} title="the feed's own ATM IV (CE/PE average) -- should match 'IV used'">Feed IV</th>
            <th style={th}>CE Δ</th>
            <th style={th}>Γ / option</th>
            <th style={th}>Γ normal clock</th>
          </tr></thead>
          <tbody>
            <For each={c()?.rows || []}>{(r) => (
              <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                <td style={{ ...td, ...left }}>{r.expiry}</td>
                <td style={td}>{n(r.calendar_days, 0)}</td>
                <td style={td}>{n(r.today_fraction, 4)}</td>
                <td style={td}>{n(r.minutes_left, 1)}</td>
                <td style={{ ...td, color: '#ffd27a', 'font-weight': 700 }}>{n(r.dte_days, 4)}</td>
                <td style={td}>{n(r.dte_years, 6)}</td>
                <td style={td}>{n(r.normal_day_dte, 4)}</td>
                <td style={td}>{r.atm || '—'}</td>
                <td style={td}>{r.ce ? n(r.ce + r.pe) : '—'}</td>
                <td style={{ ...td, color: '#ffd27a', 'font-weight': 700 }}>{pct(r.iv)}</td>
                <td style={td}>{pct(r.iv_normal_clock)}</td>
                <td style={td}>{pct(r.feed_iv)}</td>
                <td style={td}>{n(r.ce_delta, 4)}</td>
                <td style={td}>{n(r.gamma, 6)}</td>
                <td style={td}>{n(r.gamma_normal_clock, 6)}</td>
              </tr>
            )}</For>
          </tbody>
        </table>
      </Show>
    </div>
  );
}
