// Shared number formatting for the tabs ('—' when not a number).

export const fmt = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? n.toFixed(d) : '—';
};

// Signed: "+1.25" / "-0.50".
export const sgn = (v, d = 2) => {
  const n = Number(v);
  return Number.isFinite(n) ? `${n >= 0 ? '+' : ''}${n.toFixed(d)}` : '—';
};

// CSS class for a P&L value (zero counts as positive).
export const pnlCls = (v) => ((Number(v) || 0) >= 0 ? 'positive' : 'negative');
