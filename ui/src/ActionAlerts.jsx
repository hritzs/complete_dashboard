// Banner shown on every page: actions a trade did not complete (hedge left
// unfilled, build short, exit legs still open, live out of step with the
// Paper Sim) with the reason, and each trade's latest broker rejection
// (MTM / RMS) with its cause. Nothing here sends an order -- the operator
// decides. Alerts come live over the websocket ("action_alert", re-dispatched
// by App) and from /api/alerts on load; rejections from /api/portfolio/today.
import { createSignal, onMount, onCleanup, For, Show } from 'solid-js';
import { safeFetchJson } from './api.js';

const KIND_LABEL = {
  HEDGE_PARTIAL: 'Hedge partly filled',
  HEDGE_FAILED: 'Hedge failed',
  HEDGE_REFUSED: 'Hedge refused',
  SIM_HEDGE_SHORT: 'Sim hedge short',
  SIM_MISMATCH: 'Live ≠ Paper Sim',
  BUILD_FAILED: 'Build failed',
  BUILD_UNFILLED: 'Build short',
  EXIT_UNFILLED: 'Exit left legs open',
};

const keyOf = (a) => `${a.day}|${a.time}|${a.trade_uid}|${a.kind}|${a.reason}`;
const shortUid = (uid) => (uid || '').replace(/^TRD_U\d+_GREEKSOFT_\d+_/, '');

export default function ActionAlerts() {
  const [alerts, setAlerts] = createSignal([]);
  const [rejects, setRejects] = createSignal([]);
  const [dismissed, setDismissed] = createSignal(new Set());

  const add = (list) => {
    const have = new Set(alerts().map(keyOf));
    const fresh = (list || []).filter((a) => a && !have.has(keyOf(a)));
    if (fresh.length) setAlerts([...alerts(), ...fresh].slice(-50));
  };
  const loadAlerts = async () => {
    const d = await safeFetchJson('/api/alerts');
    if (d && Array.isArray(d.alerts)) add(d.alerts);
  };
  const loadRejects = async () => {
    const d = await safeFetchJson('/api/portfolio/today');
    const trades = Array.isArray(d?.trades) ? d.trades : [];
    setRejects(trades.filter((t) => t.last_reject).map((t) => ({
      key: `rej|${t.trade_uid}|${t.last_reject.time || ''}`,
      trade_uid: t.trade_uid,
      count: t.reject_count || 1,
      kind: t.last_reject.kind || 'REJECTED',
      cause: t.last_reject.cause || t.last_reject.reason || '',
      time: t.last_reject.time ? new Date(t.last_reject.time).toLocaleTimeString('en-IN', { hour12: false, timeZone: 'Asia/Kolkata' }) : '',
    })));
  };

  const onLive = (e) => add([e.detail]);
  let timer;
  onMount(() => {
    window.addEventListener('action_alert', onLive);
    loadAlerts();
    loadRejects();
    timer = setInterval(() => { loadAlerts(); loadRejects(); }, 5000);
  });
  onCleanup(() => {
    window.removeEventListener('action_alert', onLive);
    clearInterval(timer);
  });

  const dismiss = (k) => setDismissed(new Set([...dismissed(), k]));
  const shownAlerts = () => alerts().filter((a) => !dismissed().has(keyOf(a))).slice().reverse();
  const shownRejects = () => rejects().filter((r) => !dismissed().has(r.key));

  const row = { display: 'flex', gap: '10px', 'align-items': 'baseline', padding: '4px 10px', 'font-size': '12px', 'border-bottom': '1px solid rgba(255,255,255,0.08)' };
  const btn = { 'margin-left': 'auto', padding: '1px 8px', 'font-size': '11px', 'min-width': '0', cursor: 'pointer' };

  return (
    <Show when={shownAlerts().length || shownRejects().length}>
      <div style={{ background: '#3a1d1d', border: '1px solid #ff6b6b', 'border-radius': '6px', margin: '6px 12px', 'max-height': '220px', overflow: 'auto' }}>
        <For each={shownAlerts()}>{(a) => (
          <div style={row}>
            <span style={{ color: '#ffb4b4', 'font-weight': 700, 'white-space': 'nowrap' }}>{a.time} {KIND_LABEL[a.kind] || a.kind}</span>
            <span style={{ color: '#ddd', 'white-space': 'nowrap' }}>{shortUid(a.trade_uid)}</span>
            <span style={{ color: '#fff' }}>{a.reason}<Show when={a.ask}><b style={{ color: '#ffd27a' }}> — {a.ask}</b></Show></span>
            <button style={btn} onClick={() => dismiss(keyOf(a))}>Dismiss</button>
          </div>
        )}</For>
        <For each={shownRejects()}>{(r) => (
          <div style={row}>
            <span style={{ color: '#ff6b6b', 'font-weight': 700, 'white-space': 'nowrap' }}>{r.time} {r.kind === 'MTM_LIMIT' ? 'MTM rejection' : r.kind === 'OTHER' ? 'Rejection' : `${r.kind} rejection`}</span>
            <span style={{ color: '#ddd', 'white-space': 'nowrap' }}>{shortUid(r.trade_uid)}</span>
            <span style={{ color: '#fff' }}>{r.cause}{r.count > 1 ? ` (${r.count} rejected)` : ''}</span>
            <button style={btn} onClick={() => dismiss(r.key)}>Dismiss</button>
          </div>
        )}</For>
      </div>
    </Show>
  );
}
