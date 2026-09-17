import { createSignal, onMount, onCleanup, For, Show } from 'solid-js';

// Routed through vite's dev-server proxy (see vite.config.js's
// '/api/latency' rule -> services/latency-dashboard on 8023), NOT a
// direct window.location.hostname:8023 URL -- confirmed live that the
// browser can only reach the vite dev server's own port; a second
// service's port isn't necessarily reachable directly depending on how
// this environment is tunneled/exposed. The vite proxy runs server-side,
// so it only needs 8023 to be reachable from the machine running vite,
// not from the browser.
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
  if (v >= 1000) return `${(v / 1000).toFixed(2)} ms`;
  return `${v.toFixed(0)} µs`;
};

// Slow-confirmation coloring, reusing the same log-level classes App.jsx
// already styles (log-success/log-warn/log-error) rather than adding new CSS.
const latencyLevel = (us) => {
  const v = Number(us);
  if (!Number.isFinite(v)) return 'info';
  if (v >= 2_000_000) return 'error'; // >= 2s
  if (v >= 500_000) return 'warn'; // >= 500ms
  return 'success';
};

const POLL_INTERVAL_MS = 5000;

function LatencyDashboard() {
  const [stats, setStats] = createSignal(null);
  const [recent, setRecent] = createSignal([]);
  const [error, setError] = createSignal('');

  let timer = null;

  const refresh = async () => {
    try {
      const [statsData, recentData] = await Promise.all([
        fetchJson('/api/latency/stats?window=5m'),
        fetchJson('/api/latency/recent?limit=200'),
      ]);
      setStats(statsData);
      setRecent(recentData);
      setError('');
    } catch (e) {
      setError(e.message || 'Failed to load latency data');
    }
  };

  onMount(() => {
    refresh();
    timer = setInterval(refresh, POLL_INTERVAL_MS);
  });

  onCleanup(() => {
    if (timer) clearInterval(timer);
  });

  return (
    <>
      <section class="toolbar-right">
        <div class="metric-card">
          <div class="metric-label">Samples (5m)</div>
          <div class="metric-value">{stats()?.count ?? '—'}</div>
        </div>
        <div class="metric-card">
          <div class="metric-label">Avg</div>
          <div class="metric-value">{fmtUS(stats()?.avg_us)}</div>
        </div>
        <div class="metric-card">
          <div class="metric-label">p50</div>
          <div class="metric-value">{fmtUS(stats()?.p50_us)}</div>
        </div>
        <div class="metric-card">
          <div class="metric-label">p95</div>
          <div class="metric-value">{fmtUS(stats()?.p95_us)}</div>
        </div>
        <div class="metric-card highlight">
          <div class="metric-label">p99 / Max</div>
          <div class="metric-value">{fmtUS(stats()?.p99_us)}</div>
          <div class="metric-sub">max {fmtUS(stats()?.max_us)}</div>
        </div>
      </section>

      <section class="chain-panel" style="margin-top:14px">
        <div class="panel-header">
          <div class="panel-title">Iris Order-Confirmation Latency</div>
          <div class="panel-subtitle">Local order submission &rarr; GreekSoft's first Iris push confirming it</div>
        </div>

        <Show when={error()}>
          <div class="empty-state">{error()}</div>
        </Show>

        <div class="log-container">
          <For each={recent()}>
            {(sample) => (
              <div class={`log-line log-${latencyLevel(sample.latency_us)}`}>
                <span class="log-ts">{new Date(sample.recorded_at).toLocaleTimeString()}</span>
                <span class="log-lvl">{fmtUS(sample.latency_us)}</span>
                <span class="log-msg">
                  order {sample.broker_order_id}
                  {sample.trade_uid ? ` — ${sample.trade_uid}` : ''}
                </span>
              </div>
            )}
          </For>
          <Show when={recent().length === 0 && !error()}>
            <div class="empty-state">No confirmed orders in the last 5 minutes yet.</div>
          </Show>
        </div>
      </section>
    </>
  );
}

export default LatencyDashboard;
