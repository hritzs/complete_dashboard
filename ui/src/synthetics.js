// Synthetic futures from a trade's own fills: strike + CE price - PE price.

const timeOf = (e) => String(e.time || '').slice(11, 19); // HH:MM:SS, as hedge events have it

// Average fill price per leg of the given fills on one strike.
function legAverages(fills) {
  const sum = { CE: [0, 0], PE: [0, 0] };
  for (const f of fills) {
    const q = Number(f.filled_qty) || 0;
    const p = Number(f.avg_price) || 0;
    if (q <= 0 || p <= 0 || !sum[f.leg]) continue;
    sum[f.leg][0] += q;
    sum[f.leg][1] += q * p;
  }
  return {
    ceQty: sum.CE[0],
    peQty: sum.PE[0],
    ce: sum.CE[0] ? sum.CE[1] / sum.CE[0] : 0,
    pe: sum.PE[0] ? sum.PE[1] / sum.PE[0] : 0,
  };
}

// The synthetic the straddle was sold at, from the build's sell fills. A build
// across strikes is weighted by the quantity sold on both legs. 0 = not known.
export function buildSynthetic(executions) {
  const byStrike = new Map();
  for (const e of executions || []) {
    if (e.kind !== 'ENTRY' || String(e.side).toUpperCase() !== 'SELL') continue;
    if (!byStrike.has(e.strike)) byStrike.set(e.strike, []);
    byStrike.get(e.strike).push(e);
  }
  let weight = 0;
  let value = 0;
  for (const [strike, fills] of byStrike) {
    const a = legAverages(fills);
    const w = Math.min(a.ceQty, a.peQty);
    if (w <= 0) continue;
    weight += w;
    value += w * (Number(strike) + a.ce - a.pe);
  }
  return weight ? value / weight : 0;
}

// Each hedge event's fills: its strike, from its time until the next event.
// Returns CE / PE average price and the synthetic per event, keyed by time|strike.
export function hedgeSynthetics(events, executions) {
  const hedges = (executions || []).filter((e) => e.kind === 'HEDGE');
  const sorted = [...(events || [])].sort((a, b) => String(a.time).localeCompare(String(b.time)));
  const out = {};
  sorted.forEach((h, i) => {
    const from = String(h.time);
    const to = sorted[i + 1] ? String(sorted[i + 1].time) : '99:99:99';
    const a = legAverages(hedges.filter((e) => e.strike === h.strike && timeOf(e) >= from && timeOf(e) < to));
    out[`${h.time}|${h.strike}`] = {
      ce: a.ce,
      pe: a.pe,
      synthetic: a.ce > 0 && a.pe > 0 ? Number(h.strike) + a.ce - a.pe : 0,
    };
  });
  return out;
}
