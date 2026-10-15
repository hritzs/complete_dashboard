# Higher-tax conviction including 09:16

Fixed before evaluating results. Latest owner instruction supersedes the
previous blanket ban on boosting 09:16 research entries.

- Preserve the published entry LUTs and taxes (13,5,2,2,6). Do not remove or
  subtract these taxes from PnL; they remain LUT construction hurdles.
- A separate higher-hurdle min-cut layer uses +2 or +4 at every stage:
  (15,7,4,4,8) and (17,9,6,6,10), with the original stage cascade.
  An actual base-approved entry is strong only if its cell survives this
  additional layer. NW is unchanged by the CW conviction tax.
- Fit the additional LUTs annually from strictly earlier calendar years of
  the read-only round48 no-roll counterfactual cache, ps1. Match production
  signal buckets, veto, event weights, entry and hedge costs. Cache coverage
  ends 2026-08-19; 2026 fitting uses only data through 2025-12-31.
- Refit loss-probability/downside risk models, imputations, scales, empirical
  percentile references and risk threshold using completed prior-year entries
  only. Exclude outcome-fitted S1/S2 predictors entirely. Previous flat-book
  DD/streak use only earlier realized outcomes. No annual full-period size
  normalization or full-history coefficient optimization.
- Warm-up year2021 remains size1, with no learned conviction flag.
- Dicey risk always overrides conviction: size0.85. Otherwise ordinary size1;
  stronger-tax surviving CW entries, INCLUDING09:16, get size1.05 or1.10.
  Fixed four candidates: increments2/4 × boosts5%/10%. No tuning on reported
  forward-period PnL. Mean1 is a target, not an enforceable future identity;
  report actual average and a same-average constant-size comparison ex post.
- This is a causal sizing OVERLAY conditional on the legacy entry list and
  preexisting execution/features. The fixed base LUTs were trained on broader
  history; cached ticks/features and execution timing are not certified
  causal. Therefore this is NOT a leakage-free end-to-end strategy result.
- Retain dicey ceiling0.85, global cap1.15, original base trade set. Save all
  trade flags/sizes/PnL and training cutoff dates, comparisons by entry stage
  including09:16, risk and LUT model provenance, higher-tax masks and tests.
- No live modifications. Shared input paths stay read-only. Higher-tax masks
  solve the same monotone maximum-closure problem as the original solver;
  verify the solver against brute-force tiny cases before using it.
