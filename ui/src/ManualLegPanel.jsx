import { createSignal, onMount, Show, For } from 'solid-js';

// Manual legs on an open trade: add any strike / CE-PE / expiry (BUY or
// SELL) as part of the trade -- choosing whether it counts in the trade's
// P&L and greeks -- or close (part of) one open leg. Real MARKET orders,
// each behind an OK/Cancel confirm. POST /api/trade/manual-leg.

const post = async (body) => {
  try {
    const res = await fetch('/api/trade/manual-leg', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    return await res.json();
  } catch (err) {
    return { success: false, error: String(err) };
  }
};

const resultText = (d) => {
  const r = d.result || {};
  return d.success
    ? `${r.action} ${r.side} ${r.filled} ${r.contract} @${r.avg_price} -- open after: ${r.open_after}${r.action === 'ADD' ? (r.in_risk ? ' (in P&L / greeks)' : ' (NOT in P&L / greeks)') : ''}`
    : `Failed: ${d.error || r.message || 'unknown error'}`;
};

// Close some or all lots of one open leg (Position Details "Close" button).
export async function closeManualLeg(tradeUid, position, lotSize, onDone) {
  const qty = Number(position.quantity) || 0;
  const lot = Number(lotSize) || 1;
  const maxLots = Math.max(1, Math.round(qty / lot));
  const contract = `${position.option_type} ${position.strike}${position.expiry ? ` ${position.expiry}` : ''}`;
  const lotsTxt = window.prompt(`Close ${contract} (${position.action} ${qty}) with a MARKET order.\nLots to close (1-${maxLots}, blank = all):`, String(maxLots));
  if (lotsTxt == null) return;
  const lots = lotsTxt.trim() === '' ? 0 : Math.floor(Number(lotsTxt));
  if (lotsTxt.trim() !== '' && (!Number.isFinite(lots) || lots <= 0 || lots > maxLots)) { window.alert('Invalid lots'); return; }
  if (!window.confirm(`REAL ORDER: ${position.action === 'SELL' ? 'BUY' : 'SELL'} ${lots ? lots * lot : qty} ${contract} at MARKET?`)) return;
  const d = await post({ trade_uid: tradeUid, action: 'CLOSE', token: Number(position.token), lots, confirm: 'CONFIRM' });
  window.alert(resultText(d));
  onDone && onDone(d);
}

export default function ManualLegPanel(props) {
  const it = () => props.item || {};
  const [expiries, setExpiries] = createSignal([]);
  const [expiry, setExpiry] = createSignal('');
  const [strike, setStrike] = createSignal('');
  const [opt, setOpt] = createSignal('CE');
  const [side, setSide] = createSignal('BUY');
  const [lots, setLots] = createSignal('1');
  const [inRisk, setInRisk] = createSignal(true);
  const [busy, setBusy] = createSignal(false);
  const [msg, setMsg] = createSignal('');

  onMount(async () => {
    setExpiry(it().expiry || '');
    setStrike(String(it().strike || ''));
    try {
      const d = await (await fetch(`/api/sbuild/expiries?symbol=${encodeURIComponent(it().symbol || 'NIFTY')}`)).json();
      if (d.success) setExpiries(d.expiries || []);
    } catch (_) { /* restarting */ }
  });

  const submit = async (e) => {
    e.stopPropagation();
    const lotN = Math.floor(Number(lots()));
    const k = Number(strike());
    if (!lotN || lotN <= 0 || !k) { setMsg('Enter strike and lots'); return; }
    const sameExp = !expiry() || expiry() === it().expiry;
    if (!window.confirm(
      `REAL ORDER on trade ${it().tradeUid || it().id}:\n${side()} ${lotN} lot(s) ${it().symbol} ${expiry() || it().expiry} ${k} ${opt()} at MARKET` +
      `${sameExp ? '' : '  (DIFFERENT EXPIRY from the trade)'}\n` +
      `${inRisk() ? 'Counts in this trade\'s P&L, greeks, PointsOut, SL and TP.' : 'Kept OUT of this trade\'s P&L and greeks (shown, closed by Full Exit).'}\n\n${side()}?`)) return;
    setBusy(true); setMsg('Sending...');
    const d = await post({ trade_uid: it().tradeUid || it().id, action: 'ADD', expiry: expiry(), strike: k, option_type: opt(), side: side(), lots: lotN, include_risk: inRisk(), confirm: 'CONFIRM' });
    setBusy(false);
    setMsg(resultText(d));
    props.onDone && props.onDone(d);
  };

  const field = { display: 'flex', 'flex-direction': 'column', gap: '4px', 'font-size': '11px', opacity: 0.85 };
  const input = { padding: '6px 8px', background: '#0f0f18', color: '#fff', border: '1px solid #44445a', 'border-radius': '5px', 'font-size': '13px', 'min-width': '0' };

  return (
    <section class="trade-card manual-actions-card" onClick={(e) => e.stopPropagation()}>
      <div class="trade-card-title">Add leg to this trade</div>
      <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '10px', 'align-items': 'flex-end', padding: '6px 0' }}>
        <label style={field}>Expiry
          <select style={input} value={expiry()} onChange={(e) => setExpiry(e.currentTarget.value)}>
            <For each={expiries().length ? expiries() : [it().expiry]}>{(x) => <option value={x}>{x}{x === it().expiry ? ' (trade)' : ''}</option>}</For>
          </select>
        </label>
        <label style={field}>Strike
          <input style={{ ...input, width: '90px' }} type="number" step="50" value={strike()} onInput={(e) => setStrike(e.currentTarget.value)} />
        </label>
        <label style={field}>Type
          <select style={input} value={opt()} onChange={(e) => setOpt(e.currentTarget.value)}><option>CE</option><option>PE</option></select>
        </label>
        <label style={field}>Side
          <select style={input} value={side()} onChange={(e) => setSide(e.currentTarget.value)}><option>BUY</option><option>SELL</option></select>
        </label>
        <label style={field}>Lots ({it().lotSize || '—'} each)
          <input style={{ ...input, width: '70px' }} type="number" min="1" step="1" value={lots()} onInput={(e) => setLots(e.currentTarget.value)} />
        </label>
        <label style={{ ...field, 'flex-direction': 'row', 'align-items': 'center', gap: '6px', 'font-size': '12px' }}>
          <input type="checkbox" checked={inRisk()} onChange={(e) => setInRisk(e.currentTarget.checked)} />
          Include in this trade's P&amp;L &amp; greeks
        </label>
        <button class="dashboard-btn purple" disabled={busy()} onClick={submit}>Add leg</button>
      </div>
      <div style={{ 'font-size': '12px', opacity: 0.7 }}>
        Close an existing leg with <strong>Close</strong> in Position Details. Every leg (any expiry) is closed by Full Exit.
      </div>
      <Show when={msg()}><div style={{ 'font-size': '12px', 'margin-top': '6px', color: msg().startsWith('Failed') ? '#ff6b6b' : '#7ee787' }}>{msg()}</div></Show>
    </section>
  );
}
