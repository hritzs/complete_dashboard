# Past-only rerun of iterations10/11

Fixed before new candidate performance is calculated. Research only; no live,
credential or shared-input writes. Frozen1430-date/799-entry production book.

- Two predeclared policies: WF10 maximum-profit allocation; WF11 maximum-profit
  allocation with mandatory dicey ceiling0.85. Flat is the baseline.
- 2021 warmup remains size1. For2022..2026, all fitting uses completed prior
  calendar years only. Freeze each fold before scoring its year. Include all
  prior trade outcomes as training labels, never the scoring year's outcomes.
- Re-extract799 pre-entry observations. Tick Time_dt must be strictly less
  than decision. Select ATM from latest prior underlying; fixed strike during
  preceding60sec; backward as-of only, max quote age3s, >=20 valid snapshots.
  Compute LTP straddle range/change, spread, underlying range, prefix OTM IV,
  weekend-adjusted BuildIV/DTE, IV ratio, straddle ratio and opening gap from
  strictly before09:16. Missing inputs are explicit, never backfilled.
- Previous daily reference fields must have PrevDate and Prev2Date before the
  decision date. IDV weights0.2/0.5/0.3, ADJ change normalization and RV5/10/20
  match the old definitions. Reference-file availability/provenance is assumed
  as dated, not independently feed-certified. Source hashes/fingerprints saved.
- Refit S1 bucket score (quintiles/discrete; shrink20), S2 ridge (lambda10),
  imputations, scales and clipping on prior-year training entries. No saved
  full-history scores, coefficients or warm starts. WF10 uses the original34
  predictor schema; WF11 adds prior flat DD/streak and fitted loss/downside.
- Logistic risk lambda20 and downside ridge lambda50; risk inputs exclude S1/S2.
  Instability uses CW range/range/spread ranks; missing/NW rank0.5. Rank
  references and composite75th percentile fit on training only. Fixed weights
  0.4/0.4/0.2; missing CW instability independently forces dicey.
- Bounds: floor0.75; global cap1.15;09:16 CW cap1. WF11 dicey cap0.85 wins.
  No Iteration12 conviction gate or Iteration13 higher-tax boosts are added.
- Fit linear raw size1+X*b with coefficient bounds[-1,1], fixed-seed multi-start
  coordinate search, only maximum-profit objective. WF10 seed42,96 Gaussian
  starts at scales.025/.075/.15/.30 and six original step sizes; WF11 seed73,
  60 Gaussian starts at.04/.12/.25 and five original steps. One-feature +/- .08
  starts; best two starts; <=3 coordinate sweeps per step; penalty1e-5 mean(b²).
  No full-history warm starts; no post-score hyperparameter selection.
- Training offset solves bounded arithmetic mean1 using a mathematically
  equivalent breakpoint projection. Freeze that scalar offset in the scored
  year: NEVER solve it over future entries. Report actual forward average size.
  Verify projector against the archived43-bisection implementation.
- WF11 training qualification retains net>=flat, MDD<=flat, worst>=flat worst.
  If no qualifying policy or mean-one training budget is infeasible, explicitly
  fall back to risk-only (.85 dicey,1 otherwise), keeping hard risk caps and
  reporting failure/actual mean. WF10 can fall back to flat. Training constraints
  do not guarantee the same forward risk/profit limits.
- Score all folds once; report full/warmup/scored, actual mean, same-mean flat
  ex-post diagnostic, cuts/boosts, all trades/days, quarterly/yearly/monthly/FY,
  fold cutoffs/models/coefficients, features, coverage, fit feasibility and tests.
  Register separate ledger iterations15/16, preserve original10/11 archives.
- Qualification for forward improvement: net>flat, net/MDD>flat, worst>=flat.
  Report both candidates even if neither wins. Historical years have already
  been researched; this is not an untouched final holdout.
- Conditional sizing-overlay study only. Base LUTs/entries used wider history;
  saved fills/costs remain unchanged and are not feed-certified. No claim of
  unbiased end-to-end strategy performance; fractional sizes scale saved costs/
  PnL linearly without live lot/fixed-fee replay.
