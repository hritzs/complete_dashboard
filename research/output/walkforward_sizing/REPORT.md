# Walk-forward sizing rerun

- Past-only sizing-overlay rerun of10/11; strict prefix intraday features; prior daily reference dates enforced.
- 2021 warmup size1; each following calendar year fits completed prior years only; no full-history weights/warm starts.
- Training mean-one offset frozen in scoring year; actual mean reported, no future normalization.
- Legacy base LUTs/entries used wider history; saved fills/costs and dated reference fields are not independently certified causal.
- New forward overlay fixes fitting/normalization leakage, not end-to-end entry selection bias; previously researched years are not untouched holdouts.
- All original entry taxes preserved; archived09:16 CW cap1; WF11 mandatory dicey<=.85; low.75/global1.15.
- WF10 has no mandatory dicey restriction; its risk columns are comparison diagnostics only. WF11 uses the risk flag as a hard cap.
- Prefix OTM IV/DTE, straddle ratios and LTP ranges replace cached intraday predictors, so this is a repaired-input rerun, not identical feature values.
- If WF11 training feasibility/qualification fails, report explicit risk-only fallback; do not relax risk caps.
- Size multipliers scale existing event weights and saved net costs/PnL linearly; no whole-lot or quantity-specific execution replay.

Scored 2022+ (617 entries; 2021 warmup excluded):

| Policy | Net PnL | Max DD | Worst day | Net/DD | Mean size | Cuts | Boosts |
|---|---:|---:|---:|---:|---:|---:|---:|
| Flat size 1 | 7,593.21 | 144.37 | -61.28 | 52.60 | 1.000000 | 0 | 0 |
| WF10 maximum profit | 7,547.88 | 152.63 | -70.47 | 49.45 | 0.976479 | 239 | 267 |
| WF11 dicey0.85 maximum profit | 7,435.66 | 138.66 | -53.87 | 53.63 | 0.956000 | 317 | 205 |

Compare forward mean/ex-post same-mean flat diagnostics; these are not a live normalization rule.
Input coverage: 746/799 valid strict prefixes.

Neither rerun passes the declared forward qualification (higher net, higher net/DD and no worse worst day). Every annual fold found a qualifying training fit; no fallback was used.

- WF10 maximum profit: net change -0.60%; MDD change +5.72%; 239 cuts/267 boosts; extra versus ex-post same-mean flat +133.27.
- WF11 dicey0.85 maximum profit: net change -2.07%; MDD change -3.95%; 317 cuts/205 boosts; extra versus ex-post same-mean flat +176.55.

Detailed explanation: `../../WALKFORWARD_EXPLAINED.md`. Source and frozen-model replay verification: `verification.json`. No live change.

The past-fitted risk gate flags232/617 scored entries. WF11 averages0.829738
on these dicey entries and1.032086 otherwise. All flagged entries respect.85.
These counts replace the old in-sample201/799 count for this experiment.

WF11's lower DD comes with average size0.956. An ex-post uniform flat book at
that mean has net7259.11 and DD138.01, versus WF11's7435.66 and138.66. The
allocation adds176.55 net relative to that diagnostic but has slightly higher
DD. This does not establish the requested higher-profit/lower-DD result at
unchanged exposure. The same-mean comparison is an activity proxy, not a live
normalization rule or proof of identical capital usage.
