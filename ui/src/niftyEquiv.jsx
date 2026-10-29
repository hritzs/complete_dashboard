// NIFTY-equivalent figures for other indices: a SENSEX straddle price or P&L
// per straddle x (NIFTY level / SENSEX level), so retention reads on NIFTY's
// scale next to the normal figure. Levels: GET /api/index/levels (each
// chain's synthetic future), polled every 2 s by one shared poller.
import { createSignal, Show } from 'solid-js';

const [levels, setLevels] = createSignal({});
let started = false;
const poll = async () => {
  try {
    const d = await (await fetch('/api/index/levels')).json();
    if (d && d.levels) setLevels(d.levels);
  } catch (_) { /* keep the last levels */ }
};
const start = () => {
  if (started) return;
  started = true;
  poll();
  setInterval(poll, 2000);
};

const base = (sym) => String(sym || '').toUpperCase().replace(/\s+/g, '');

// niftyRatio: NIFTY level / this index's level; null for NIFTY itself or unknown.
export function niftyRatio(symbol) {
  start();
  const s = base(symbol);
  if (!s || s === 'NIFTY') return null;
  const l = levels();
  return l.NIFTY > 0 && l[s] > 0 ? l.NIFTY / l[s] : null;
}

export function niftyEq(symbol, value) {
  const r = niftyRatio(symbol);
  const v = Number(value);
  return r && Number.isFinite(v) ? v * r : null;
}

// niftyEqText: ' · ≈ NIFTY ₹x' for inline text ('' for NIFTY / unknown).
export function niftyEqText(symbol, value, label) {
  const v = niftyEq(symbol, value);
  return v === null ? '' : ` · ≈ NIFTY ${label ? label + ' ' : ''}₹${v.toFixed(2)}`;
}

// <NiftyEq symbol=.. value=.. label?=..>: a small "≈ NIFTY ₹x" line; nothing for NIFTY.
export default function NiftyEq(props) {
  const v = () => niftyEq(props.symbol, props.value);
  return (
    <Show when={v() !== null}>
      <div style={{ 'font-size': '10.5px', opacity: 0.75, 'white-space': 'nowrap' }} title={`× ${(niftyRatio(props.symbol) || 0).toFixed(4)} (NIFTY / ${base(props.symbol)} level)`}>
        ≈ NIFTY {props.label ? props.label + ' ' : ''}₹{v().toFixed(2)}
      </div>
    </Show>
  );
}
