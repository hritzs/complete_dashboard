import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';

// Per trade (owner, 2026-11-06): the time to expiry and IVs this trade's greeks and
// hedge actually use, next to what a normal 09:15-15:40 day's clock would give at
// the same moment -- DTE, T, each leg's IV, the ATM IV behind Points Allowed, and
// Points Allowed itself. DTEs from GET /api/clock (the feed's clock); IVs on the
// normal clock re-solved here from the same prices with undiscounted Black-76 on
// the synthetic future (the model of the feed and the gateway).
const ncdf = (x) => {
  const t = 1 / (1 + 0.2316419 * Math.abs(x));
  const d = 0.3989422804014327 * Math.exp(-x * x / 2);
  const p = d * t * (0.31938153 + t * (-0.356563782 + t * (1.781477937 + t * (-1.821255978 + t * 1.330274429))));
  return x >= 0 ? 1 - p : p;
};
const b76 = (call, F, K, T, v) => {
  if (!(F > 0 && K > 0 && T > 0 && v > 0)) return 0;
  const s = v * Math.sqrt(T);
  const d1 = (Math.log(F / K) + 0.5 * v * v * T) / s;
  const d2 = d1 - s;
  return call ? F * ncdf(d1) - K * ncdf(d2) : K * ncdf(-d2) - F * ncdf(-d1);
};
const solveIV = (call, px, F, K, T) => {
  if (!(px > 0 && F > 0 && K > 0 && T > 0)) return 0;
  let lo = 0.001, hi = 3;
  if (px <= b76(call, F, K, T, lo) || px >= b76(call, F, K, T, hi)) return 0;
  for (let i = 0; i < 80; i++) {
    const m = (lo + hi) / 2;
    if (b76(call, F, K, T, m) > px) hi = m; else lo = m;
  }
  return (lo + hi) / 2;
};
const f = (v, d = 2) => (v === undefined || v === null || !Number.isFinite(Number(v)) || Number(v) === 0 ? '—' : Number(v).toFixed(d));
const pct = (v) => (v > 0 ? (v * 100).toFixed(2) + '%' : '—');
const norm = (e) => String(e || '').trim().toUpperCase();

export default function TradeClockCompare(props) {
  const [clk, setClk] = createSignal(null);
  let t;
  const load = async () => {
    if (document.hidden) return;
    try { setClk(await (await fetch(`/api/clock?symbol=${encodeURIComponent(props.symbol || 'NIFTY')}`)).json()); } catch (_) { /* restarting */ }
  };
  onMount(() => { load(); t = setInterval(load, 5000); });
  onCleanup(() => clearInterval(t));

  const row = () => (clk()?.rows || []).find((r) => norm(r.expiry) === norm(props.expiry)) || null;
  const Tu = () => (row() ? row().dte_days / 365 : 0);
  const Tn = () => (row() ? row().normal_day_dte / 365 : 0);
  const F = () => Number(props.underlying) || Number(row()?.future) || 0;
  // One IV per strike from its OUT-OF-THE-MONEY leg (call when K > F, else put),
  // shared by both legs -- as the feed does; the monitor's ATM IV (Points Allowed)
  // is that strike IV at the ATM.
  const strikeIV = (K, ce, pe, Fx, T) => (K > Fx ? solveIV(true, ce, Fx, K, T) : solveIV(false, pe, Fx, K, T));
  const atmIV = (T) => {
    const r = row();
    if (!r || !r.atm || !r.ce || !r.pe) return 0;
    return strikeIV(r.atm, r.ce, r.pe, r.future, T);
  };
  const hdiv = () => Number(props.hedgeDiv) || 38;
  const sdiv = () => Number(props.straddleDiv) || 3;
  const floorPts = () => F() * (Number(props.minBps) || 8) / 10000;
  const allowed = (iv) => {
    const r = row();
    if (!r || !iv) return { s: 0, i: 0, a: 0 };
    const s = (r.ce + r.pe) / sdiv(), i = F() * iv / hdiv();
    return { s, i, a: Math.max(Math.min(s, i), floorPts()) };
  };
  const legs = () => (props.legs || []).filter((l) => !l.wing && l.strike && (l.option_type === 'CE' || l.option_type === 'PE') && (!l.expiry || norm(l.expiry) === norm(props.expiry)));

  const cell = { padding: '3px 8px', 'text-align': 'right', 'white-space': 'nowrap' };
  const head = { ...cell, 'font-size': '11px', color: 'rgba(255,255,255,0.6)' };
  const lab = { ...cell, 'text-align': 'left', color: 'rgba(255,255,255,0.8)' };
  const used = { ...cell, color: '#ffd27a', 'font-weight': 700 };

  return (
    <section class="trade-card">
      <div class="trade-card-title">Time to expiry &amp; IV — clock used vs a normal 09:15–15:40 day</div>
      <Show when={row()} fallback={<div class="metric-sub">{clk() ? `expiry ${props.expiry} not in the clock (nearest three only)` : 'loading…'}</div>}>
        <div class="metric-sub" style={{ 'margin-bottom': '6px' }}>
          {clk()?.session ? `${clk().session.name} ${clk().session.open}–${clk().session.close} = the whole day` : 'normal day 09:15–15:40'} · {f(row().minutes_left, 1)} min left today · {clk()?.now}
        </div>
        <table style={{ width: '100%', 'border-collapse': 'collapse', 'font-size': '12px' }}>
          <thead><tr>
            <th style={{ ...head, 'text-align': 'left' }}></th>
            <th style={{ ...head, color: '#ffd27a' }}>Used now</th>
            <th style={head}>Normal 09:15–15:40 day</th>
            <th style={head}>Difference</th>
          </tr></thead>
          <tbody>
            <tr><td style={lab}>Today's part of a day</td><td style={used}>{f(row().today_fraction, 4)}</td><td style={cell}>{f(row().normal_day_dte - row().calendar_days, 4)}</td><td style={cell}>{f(row().today_fraction - (row().normal_day_dte - row().calendar_days), 4)}</td></tr>
            <tr><td style={lab}>DTE (days) = {row().calendar_days} calendar + today's part</td><td style={used}>{f(row().dte_days, 4)}</td><td style={cell}>{f(row().normal_day_dte, 4)}</td><td style={cell}>{f(row().dte_days - row().normal_day_dte, 4)}</td></tr>
            <tr><td style={lab}>T (years) = DTE / 365</td><td style={used}>{f(Tu(), 6)}</td><td style={cell}>{f(Tn(), 6)}</td><td style={cell}></td></tr>
            <tr><td style={lab}>ATM {row().atm} IV (OTM leg) → Points Allowed</td><td style={used}>{pct(atmIV(Tu()))}</td><td style={cell}>{pct(atmIV(Tn()))}</td><td style={cell}>{atmIV(Tu()) && atmIV(Tn()) ? ((atmIV(Tu()) - atmIV(Tn())) * 100).toFixed(2) + ' pts' : '—'}</td></tr>
            <tr><td style={lab}>&nbsp;&nbsp;Straddle ÷ {sdiv()}</td><td style={used}>{f(allowed(atmIV(Tu())).s)}</td><td style={cell}>{f(allowed(atmIV(Tn())).s)}</td><td style={cell}></td></tr>
            <tr><td style={lab}>&nbsp;&nbsp;Spot × IV ÷ {hdiv()}</td><td style={used}>{f(allowed(atmIV(Tu())).i)}</td><td style={cell}>{f(allowed(atmIV(Tn())).i)}</td><td style={cell}>{f(allowed(atmIV(Tu())).i - allowed(atmIV(Tn())).i)}</td></tr>
            <tr><td style={lab}>Points Allowed (min, floor {f(floorPts())})</td><td style={used}>{f(allowed(atmIV(Tu())).a)}</td><td style={cell}>{f(allowed(atmIV(Tn())).a)}</td><td style={cell}>{f(allowed(atmIV(Tu())).a - allowed(atmIV(Tn())).a)}</td></tr>
            <For each={legs()}>{(l) => {
              // the strike's IV from its OTM leg (the trade's own leg of that type if held, else this leg)
              const K = Number(l.strike);
              const otmCE = () => K > F();
              const otmLeg = () => legs().find((x) => Number(x.strike) === K && x.option_type === (otmCE() ? 'CE' : 'PE')) || l;
              const iv = (T) => solveIV(otmLeg().option_type === 'CE', Number(otmLeg().ltp), F(), K, T);
              const ivu = () => iv(Tu());
              const ivn = () => iv(Tn());
              return (
                <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                  <td style={lab}>{l.option_type} {l.strike} IV (from {otmLeg().option_type} @ ₹{f(otmLeg().ltp)}) {l.iv ? `· feed ${f(l.iv)}%` : ''}</td>
                  <td style={used}>{pct(ivu())}</td><td style={cell}>{pct(ivn())}</td>
                  <td style={cell}>{ivu() && ivn() ? ((ivu() - ivn()) * 100).toFixed(2) + ' pts' : '—'}</td>
                </tr>
              );
            }}</For>
          </tbody>
        </table>
      </Show>
    </section>
  );
}
