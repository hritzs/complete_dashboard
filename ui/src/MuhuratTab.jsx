import { createSignal, onCleanup, onMount, Show, For } from 'solid-js';
import { postJSON } from './api.js';
import { box, muted } from './styles.js';

// MUHURAT -- a special trading session (NSE Diwali Muhurat, 2026: Sun 8 Nov,
// timings per the NSE circular). It sells exactly as a normal day -- the LUT
// tab's arming, build and monitoring -- only the open and close differ: the
// LUT runs on the session clock (first minute after the open = the 09:16
// table), time to expiry counts only the session's trading minutes and every
// timed exit is 3 min before the close. Only a listed date changes anything.
// Backend: services/execution-gateway/internal/trading/muhurat.go

const th = { 'text-align': 'right', padding: '6px 8px', 'font-size': '11px', opacity: 0.7, 'white-space': 'nowrap' };
const td = { 'text-align': 'right', padding: '5px 8px', 'font-size': '13px', 'white-space': 'nowrap' };
const left = { 'text-align': 'left' };
const n2 = (v, d = 2) => (v == null || v === 0 ? '—' : Number(v).toFixed(d));
const BUILD_BG = { BUILT: '#2e7d32', FIRING: '#c98500', FAILED: '#b23b3b', REFUSED: '#b23b3b', UNKNOWN: '#546e7a' };

export default function MuhuratTab() {
  const [st, setSt] = createSignal(null);
  const [msg, setMsg] = createSignal('');
  const [busy, setBusy] = createSignal(false);
  const [sess, setSess] = createSignal({ date: '2026-11-08', name: 'Diwali Muhurat', pre_open: '', open: '', close: '' });

  const load = async () => {
    try {
      const d = await (await fetch('/api/muhurat')).json();
      setSt(d);
    } catch (e) { setMsg(String(e)); }
  };
  const post = async (body) => {
    setBusy(true);
    setMsg('');
    try {
      const d = await postJSON('/api/muhurat', body);
      setSt(d);
      if (!d.success) setMsg(d.error || 'failed');
      return d;
    } catch (e) { setMsg(String(e)); } finally { setBusy(false); }
  };
  const setS = (k, v) => setSess({ ...sess(), [k]: v });

  const saveSession = () => post({ action: 'session_save', session: sess() });
  const editSession = (s) => setSess({ ...s });
  const deleteSession = (s) => {
    if (window.confirm(`Remove the special session ${s.date} (${s.open}-${s.close})?\n\nThat date then runs on the normal clock.`)) post({ action: 'session_delete', date: s.date });
  };
  let timer;
  onMount(() => { load(); timer = setInterval(() => { if (!document.hidden && !busy()) load(); }, 5000); });
  onCleanup(() => clearInterval(timer));

  const live = () => st()?.live;
  const day = () => st()?.day;
  const field = (label, el) => (
    <label class="control-block" style={{ display: 'flex', 'flex-direction': 'column', gap: '3px' }}>
      <span class="control-label">{label}</span>{el}
    </label>
  );

  return (
    <div class="lut-tab" style={{ padding: '4px 2px' }}>
      <div style={{ ...box, display: 'flex', 'align-items': 'center', gap: '14px', 'flex-wrap': 'wrap' }}>
        <strong style={{ 'font-size': '16px' }}>Muhurat trading</strong>
        <Show when={st()?.today} fallback={<span style={{ padding: '1px 8px', 'border-radius': '8px', background: '#455a64', 'font-size': '12px' }}>normal day</span>}>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: '#c98500', 'font-size': '12px' }}>SPECIAL SESSION TODAY {st().today.open}-{st().today.close}</span>
        </Show>
        <span style={muted}>{st()?.phase}</span>
        <Show when={st()?.clock}><span>LUT clock <strong>{st().clock}</strong></span></Show>
        <Show when={st()?.exit_time}><span>timed exit <strong>{st().exit_time}</strong></span></Show>
        <span style={{ ...muted, 'margin-left': 'auto' }}>{st()?.now}</span>
      </div>
      <Show when={msg()}><div style={{ ...box, color: '#e6c067', 'font-size': '13px' }}>{msg()}</div></Show>

      <div style={box}>
        <strong>Session calendar</strong>
        <div style={muted}>
          A listed date is the only thing that switches Muhurat behaviour on. NSE confirmed Sunday 8 Nov 2026; enter the timings from the NSE circular once published.
          Time to expiry on that date = calendar days + only the session's remaining trading minutes (of a normal 385-minute day); the date counts as a trading day.
        </div>
        <Show when={(st()?.sessions || []).length} fallback={<div style={{ ...muted, 'margin-top': '6px' }}>no special session listed — every day runs on the normal 09:15-15:40 clock</div>}>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
            <thead><tr>
              <th style={{ ...th, ...left }}>Date</th><th style={{ ...th, ...left }}>Name</th><th style={th}>Pre-open</th><th style={th}>Open</th><th style={th}>Close</th><th style={th}>Timed exit</th><th style={th}></th>
            </tr></thead>
            <tbody>
              <For each={st()?.sessions || []}>{(s) => (
                <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                  <td style={{ ...td, ...left }}>{s.date}</td><td style={{ ...td, ...left }}>{s.name}</td>
                  <td style={td}>{s.pre_open || '—'}</td><td style={td}>{s.open}</td><td style={td}>{s.close}</td>
                  <td style={td}>3 min before close</td>
                  <td style={{ ...td, display: 'flex', gap: '6px', 'justify-content': 'flex-end' }}>
                    <button class="tab-btn" disabled={busy()} onClick={() => editSession(s)}>Edit</button>
                    <button class="tab-btn" disabled={busy()} style={{ background: '#b23b3b' }} onClick={() => deleteSession(s)}>Remove</button>
                  </td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
        <div style={{ display: 'flex', gap: '10px', 'flex-wrap': 'wrap', 'align-items': 'flex-end', 'margin-top': '8px' }}>
          {field('Date', <input class="symbol-select" type="date" value={sess().date} onInput={(e) => setS('date', e.currentTarget.value)} />)}
          {field('Name', <input class="symbol-select" style={{ width: '150px' }} value={sess().name} onInput={(e) => setS('name', e.currentTarget.value)} />)}
          {field('Pre-open', <input class="symbol-select" type="time" value={sess().pre_open} onInput={(e) => setS('pre_open', e.currentTarget.value)} />)}
          {field('Open', <input class="symbol-select" type="time" value={sess().open} onInput={(e) => setS('open', e.currentTarget.value)} />)}
          {field('Close', <input class="symbol-select" type="time" value={sess().close} onInput={(e) => setS('close', e.currentTarget.value)} />)}
          <button class="tab-btn" disabled={busy() || !sess().open || !sess().close} onClick={saveSession}>Save session</button>
        </div>
      </div>

      <Show when={live()}>
        <div style={box}>
          <strong>Now on the session clock — NIFTY {live().expiry}</strong>
          <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
            <thead><tr>
              <th style={th}>Future</th><th style={th}>ATM</th><th style={th}>CE</th><th style={th}>PE</th><th style={th}>Straddle</th>
              <th style={th}>DTE (session)</th><th style={th}>DTE (if a normal day)</th><th style={th}>IV (OTM leg)</th><th style={th}>CE Δ</th><th style={th}>PE Δ</th><th style={th}>Γ / option</th>
            </tr></thead>
            <tbody><tr>
              <td style={td}>{n2(live().future)}</td><td style={td}>{live().atm}</td><td style={td}>{n2(live().ce)}</td><td style={td}>{n2(live().pe)}</td><td style={td}>{n2(live().straddle)}</td>
              <td style={td}><strong>{n2(live().dte, 4)}</strong></td><td style={td}>{n2(live().dte_normal, 4)}</td>
              <td style={td}>{live().iv ? (live().iv * 100).toFixed(2) + '%' : '—'}</td>
              <td style={td}>{n2(live().ce_delta, 4)}</td><td style={td}>{n2(live().pe_delta, 4)}</td><td style={td}>{n2(live().gamma, 6)}</td>
            </tr></tbody>
          </table>
        </div>
      </Show>

      <div style={box}>
        <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', 'flex-wrap': 'wrap' }}>
          <strong>Selling</strong>
          <span style={{ padding: '1px 8px', 'border-radius': '8px', background: st()?.lut_armed ? '#b23b3b' : '#455a64', 'font-size': '12px' }}>
            LUT tab build {st()?.lut_armed ? 'ARMED' : 'not armed'}
          </span>
          <span style={muted}>as on a normal day: arm, size, SL / TP and wings are set on the LUT tab; the session's first YES is the day's LUT YES (paper entry + the real build when armed), driven by its simulation and the standard monitor</span>
        </div>
        <Show when={st()?.build}>
          <div style={{ 'margin-top': '8px', 'font-size': '13px' }}>
            <span style={{ padding: '1px 8px', 'border-radius': '8px', background: BUILD_BG[st().build.status] || '#455a64', 'margin-right': '8px' }}>{st().build.status}</span>
            YES {st().build.minute} · {st().build.expiry} {st().build.strike} · {st().build.lots} lot(s) · SL {n2(st().build.sl_bps)} bps TP {n2(st().build.tp_bps)} bps · exit {st().build.exit_time}
            {st().build.trade_uid ? ` · trade ${st().build.trade_uid}` : ''}{st().build.error ? ` · ${st().build.error}` : ''}
          </div>
        </Show>
      </div>

      <Show when={st()?.today}>
        <div style={box}>
          <div style={{ display: 'flex', 'align-items': 'baseline', gap: '12px' }}>
            <strong>Session minutes — LUT on the session clock</strong>
            <span style={muted}>opening gap from {day()?.u0916 ? `${n2(day().u0916)} (${day().u0916_at})` : 'the first session minute'} · GreekSoft minute closes</span>
          </div>
          <Show when={(day()?.minutes || []).length} fallback={<div style={muted}>no session minute checked yet</div>}>
            <table style={{ width: '100%', 'border-collapse': 'collapse', 'margin-top': '6px' }}>
              <thead><tr>
                <th style={{ ...th, ...left }}>Time</th><th style={{ ...th, ...left }}>Table</th><th style={th}>Future</th><th style={th}>ATM</th><th style={th}>CE</th><th style={th}>PE</th><th style={th}>Straddle</th>
                <th style={th}>DTE raw / trading</th><th style={th}>Adj IV</th><th style={th}>IV ratio</th><th style={th}>Str ratio</th><th style={{ ...th, ...left }}>Coord</th><th style={th}>Answer</th>
              </tr></thead>
              <tbody>
                <For each={[...(day()?.minutes || [])].reverse()}>{(r) => (
                  <tr style={{ 'border-top': '1px solid rgba(255,255,255,0.06)' }}>
                    <td style={{ ...td, ...left }}>{r.time}</td><td style={{ ...td, ...left }}>{r.clock}</td>
                    <td style={td}>{n2(r.eval.underlying)}</td><td style={td}>{r.eval.strike}</td>
                    <td style={td}>{n2(r.eval.ce_ltp)}</td><td style={td}>{n2(r.eval.pe_ltp)}</td><td style={td}>{n2(r.eval.straddle)}</td>
                    <td style={td}>{n2(r.eval.raw_dte, 3)} / {n2(r.eval.trading_dte, 3)}</td>
                    <td style={td}>{r.eval.adj_build_iv ? (r.eval.adj_build_iv * 100).toFixed(2) + '%' : '—'}</td>
                    <td style={td}>{n2(r.eval.iv_ratio, 4)}</td><td style={td}>{n2(r.eval.str_ratio, 4)}</td>
                    <td style={{ ...td, ...left }}>{r.eval.coord_text}</td>
                    <td style={{ ...td, color: r.eval.allowed && !r.eval.skip ? '#5fd38d' : r.eval.skip ? '#e6a067' : undefined }}>
                      {r.eval.skip ? `SKIP (${r.eval.skip})` : r.eval.allowed ? 'YES' : 'NO'}
                    </td>
                  </tr>
                )}</For>
              </tbody>
            </table>
          </Show>
        </div>
      </Show>
    </div>
  );
}
