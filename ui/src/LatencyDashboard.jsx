import { createSignal, onMount, onCleanup, For, Show } from 'solid-js';

// All calls go through vite's '/api/latency' proxy rule (-> latency-dashboard
// on 8023), never a direct second-port URL: the browser can only reach the
// vite dev server's own port. /api/latency/iris is the dashboard relaying the
// reconciler's live Iris websocket status.
async function fetchJson(url) {
  const res = await fetch(url);
  const text = await res.text();
  let data;
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    throw new Error(`Invalid latency-dashboard response from ${url}`);
  }
  if (!res.ok) {
    throw new Error(data?.error || `HTTP ${res.status}`);
  }
  return data;
}

const fmtUS = (us) => {
  if (us === null || us === undefined || !Number.isFinite(Number(us))) return '—';
  const v = Number(us);
  if (v >= 1000) return `${(v / 1000).toFixed(1)} ms`;
  return `${v.toFixed(0)} µs`;
};

const fmtAge = (sec) => {
  if (sec === null || sec === undefined || !Number.isFinite(Number(sec))) return 'never';
  const s = Number(sec);
  if (s < 60) return `${s.toFixed(0)}s ago`;
  if (s < 3600) return `${(s / 60).toFixed(0)}m ago`;
  return `${(s / 3600).toFixed(1)}h ago`;
};

const fmtClock = (iso) => (iso ? new Date(iso).toLocaleTimeString() : '—');

const latencyLevel = (us) => {
  const v = Number(us);
  if (!Number.isFinite(v)) return 'info';
  if (v >= 2_000_000) return 'error';
  if (v >= 500_000) return 'warn';
  return 'success';
};

const SOURCE_LABEL = { IRIS_WS: 'IRIS WS', REST_ONLY: 'REST only', NONE: 'no events' };
const SOURCE_CLASS = { IRIS_WS: 'success', REST_ONLY: 'warn', NONE: 'error' };

const WINDOWS = [
  { id: '5m', label: '5 min' },
  { id: '1h', label: '1 hour' },
  { id: 'today', label: 'Today' },
];

const POLL_INTERVAL_MS = 5000;

function StatRow(props) {
  return (
    <section class="toolbar-right">
      <div class="metric-card">
        <div class="metric-label">{props.title} samples</div>
        <div class="metric-value">{props.stats?.count ?? '—'}</div>
      </div>
      <div class="metric-card">
        <div class="metric-label">Avg</div>
        <div class="metric-value">{fmtUS(props.stats?.avg_us)}</div>
      </div>
      <div class="metric-card">
        <div class="metric-label">p50</div>
        <div class="metric-value">{fmtUS(props.stats?.p50_us)}</div>
      </div>
      <div class="metric-card">
        <div class="metric-label">p95</div>
        <div class="metric-value">{fmtUS(props.stats?.p95_us)}</div>
      </div>
      <div class="metric-card highlight">
        <div class="metric-label">p99 / Max</div>
        <div class="metric-value">{fmtUS(props.stats?.p99_us)}</div>
        <div class="metric-sub">max {fmtUS(props.stats?.max_us)}</div>
      </div>
    </section>
  );
}

function LatencyDashboard() {
  const [windowId, setWindowId] = createSignal('1h');
  const [ack, setAck] = createSignal(null);
  const [fill, setFill] = createSignal(null);
  const [orders, setOrders] = createSignal([]);
  const [iris, setIris] = createSignal(null);
  const [error, setError] = createSignal('');

  let timer = null;

  const refresh = async () => {
    const w = windowId();
    try {
      const [ackData, fillData, orderData, irisData] = await Promise.all([
        fetchJson(`/api/latency/stats?window=${w}&stage=iris_confirmation`),
        fetchJson(`/api/latency/stats?window=${w}&stage=iris_fill`),
        fetchJson(`/api/latency/orders?window=${w}&limit=300`),
        fetchJson('/api/latency/iris'),
      ]);
      setAck(ackData);
      setFill(fillData);
      setOrders(orderData);
      setIris(irisData);
      setError('');
    } catch (e) {
      setError(e.message || 'Failed to load latency data');
    }
  };

  const pickWindow = (id) => {
    setWindowId(id);
    refresh();
  };

  onMount(() => {
    refresh();
    timer = setInterval(refresh, POLL_INTERVAL_MS);
  });

  onCleanup(() => {
    if (timer) clearInterval(timer);
  });

  const irisState = () => {
    const d = iris();
    if (!d) return { cls: 'info', text: 'CHECKING…' };
    if (!d.reachable) return { cls: 'error', text: 'RECONCILER DOWN' };
    return d.status?.connected
      ? { cls: 'success', text: 'LIVE' }
      : { cls: 'error', text: 'NO FRAMES' };
  };

  const counts = () => {
    const list = orders();
    return {
      total: list.length,
      iris: list.filter((o) => o.source === 'IRIS_WS').length,
      rest: list.filter((o) => o.source === 'REST_ONLY').length,
      none: list.filter((o) => o.source === 'NONE').length,
    };
  };

  return (
    <>
      <section class="chain-panel">
        <div class="panel-header">
          <div class="panel-title">
            Iris websocket{' '}
            <span class={`log-lvl log-${irisState().cls}`} style="margin-left:8px">
              {irisState().text}
            </span>
          </div>
          <div class="panel-subtitle">
            Live order/trade push feed from GreekSoft {iris()?.status?.host ? `(${iris().status.host})` : ''}
          </div>
        </div>
        <Show
          when={iris()?.reachable}
          fallback={<div class="empty-state">{iris()?.error || 'Waiting for reconciler status…'}</div>}
        >
          <section class="toolbar-right">
            <div class="metric-card">
              <div class="metric-label">Last frame</div>
              <div class="metric-value">{fmtAge(iris().status.last_frame_age_sec)}</div>
              <div class="metric-sub">heartbeat every ~10s</div>
            </div>
            <div class="metric-card">
              <div class="metric-label">Heartbeats</div>
              <div class="metric-value">{iris().status.heartbeats}</div>
            </div>
            <div class="metric-card">
              <div class="metric-label">Order pushes</div>
              <div class="metric-value">{iris().status.order_pushes}</div>
              <div class="metric-sub">last {fmtClock(iris().status.last_order_push_at)}</div>
            </div>
            <div class="metric-card">
              <div class="metric-label">Trade pushes</div>
              <div class="metric-value">{iris().status.trade_pushes}</div>
              <div class="metric-sub">last {fmtClock(iris().status.last_trade_push_at)}</div>
            </div>
            <div class="metric-card highlight">
              <div class="metric-label">Matched / unmatched</div>
              <div class="metric-value">
                {iris().status.applied} / {iris().status.unmatched_gave_up}
              </div>
              <div class="metric-sub">since {fmtClock(iris().status.started_at)}</div>
            </div>
          </section>
        </Show>
      </section>

      <section class="chain-panel" style="margin-top:14px">
        <div class="panel-header">
          <div class="panel-title">Order confirmation latency</div>
          <div class="panel-subtitle">
            Order submitted &rarr; GreekSoft's Iris push received (ack = first push, fill = first FILLED push)
          </div>
        </div>
        <div style="display:flex;gap:8px;margin:8px 0">
          <For each={WINDOWS}>
            {(w) => (
              <button
                class={`tab-btn ${windowId() === w.id ? 'active' : ''}`}
                onClick={() => pickWindow(w.id)}
              >
                {w.label}
              </button>
            )}
          </For>
        </div>
        <div class="metric-sub" style="margin-top:4px">Acknowledgement</div>
        <StatRow title="Ack" stats={ack()} />
        <div class="metric-sub" style="margin-top:10px">Fill</div>
        <StatRow title="Fill" stats={fill()} />
      </section>

      <section class="chain-panel" style="margin-top:14px">
        <div class="panel-header">
          <div class="panel-title">Orders &mdash; how each reached us</div>
          <div class="panel-subtitle">
            {counts().total} orders &bull; {counts().iris} via Iris websocket &bull; {counts().rest} REST-only &bull;{' '}
            {counts().none} no events
          </div>
        </div>

        <Show when={error()}>
          <div class="empty-state">{error()}</div>
        </Show>

        <div class="log-container">
          <For each={orders()}>
            {(o) => (
              <div class={`log-line log-${SOURCE_CLASS[o.source] || 'info'}`}>
                <span class="log-ts">{new Date(o.created_at).toLocaleTimeString()}</span>
                <span class="log-lvl">{SOURCE_LABEL[o.source] || o.source}</span>
                <span class="log-msg">
                  {o.side} {o.quantity} &middot; order {o.broker_order_id || '—'} &middot; {o.status}
                  {' '}&middot; ack {fmtUS(o.ack_us)} &middot; fill {fmtUS(o.fill_us)}
                  {' '}&middot; {o.iris_events}/{o.total_events} events from Iris
                  {o.trade_uid ? ` — ${o.trade_uid.slice(-22)}` : ''}
                </span>
              </div>
            )}
          </For>
          <Show when={orders().length === 0 && !error()}>
            <div class="empty-state">No GreekSoft orders in this window.</div>
          </Show>
        </div>
      </section>
    </>
  );
}

export default LatencyDashboard;
