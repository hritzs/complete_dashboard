// Shared inline styles (Solid style objects).

// Bordered section box and muted note text (LUT Build, LUT actual build, Mock).
export const box = { border: '1px solid rgba(255,255,255,0.10)', 'border-radius': '8px', padding: '10px 12px', margin: '10px 0' };
export const muted = { opacity: 0.65, 'font-size': '12px' };

// Dimmer note text and small caps field label (Straddle Build, its Portfolio rows).
export const mutedDim = { color: 'rgba(255,255,255,0.55)', 'font-size': '12px' };
export const capsLabel = { color: 'rgba(255,255,255,0.5)', 'font-size': '11px', 'text-transform': 'uppercase', 'letter-spacing': '0.04em' };

// Dark form input and a label stacked above its field (App forms).
export const formInput = { padding: '9px', background: '#0f0f18', color: '#fff', border: '1px solid #44445a', 'border-radius': '5px' };
export const formLabel = { display: 'flex', 'flex-direction': 'column', gap: '6px', 'font-size': '13px' };
