# Tick-based percentile and relative-value entry study

Fixed before calculating candidate performance. Research only; all inputs and
outputs stay local/read-only share. No live changes or new leverage.

- Existing 1,430-day production book /799 entries, including CW and NW. Keep
  original LUT entry gates/taxes(13,5,2,2,6), exits, event weighting and costs.
  Entry overlays affect existing CW signals only; NW production entries stay
  unchanged. Size multiplier1 for all executed trades.
- Extract fresh features at actual CW entry timestamps. Load only ticks with
  Time_dt STRICTLY BEFORE the decision instant. Select the ATM strike from
  the last observed underlying before that instant, not the eventual fill
  strike. No backfill. As-of snapshots carry quotes for at most3 seconds;
  invalid/crossed quotes are unavailable. Features use the same fixed strike
  across each pre-entry window. Maximum lookback300 seconds, capped by open.
- Within-window premium percentile: sellable CE+PE bid relative to earlier
  fixed-strike bids in the preceding60 seconds. Midrank ties: a constant
  premium has percentile0.5, not1. At least20 earlier valid snapshots.
- Residual richness: fit mid-straddle versus underlying displacement,
  displacement squared and elapsed time on pre-entry snapshots, excluding
  the final5 seconds. At least25 fit points. Standardize the current
  sellable-bid-minus-predicted-fair-mid residual by max(robust residual MAD,
  fit residual standard deviation,0.05 point). A statistical feature, not
  proof of arbitrage. Underlying/quadratic/time terms control local movement,
  convexity and drift; parameter estimates use that day's past ticks only.
- Other features: pre-entry underlying60-second range, straddle-mid60-second
  change, current spread percentage and quote coverage/staleness.
- Additional diagnostic: center-versus-neighbor call/put butterfly credit
  2*centerBid-leftAsk-rightAsk. Require valid displayed quantities, quotes
  <=1 second old and inter-leg timestamp skew<=1 second. Positive credit is
  an APPARENT quote violation, not an executed strategy or guaranteed net
  arbitrage; fees, whole lots and simultaneous execution remain unresolved.
- Fixed entry candidates:
  P20/P10: reject a CW entry when premium percentile<0.20/<0.10.
  Rcheap: reject when sellable residual z<-1.
  Rrich: reject when z<1 (enter only rich valid observations).
  Lspread: reject spread above prior-years90th percentile.
  Mstress: reject when BOTH underlying range and straddle change exceed their
  respective prior-years90th percentiles.
  Combined: reject P20 OR Rcheap OR Lspread.
  Delaycheap: when P20 OR Rcheap and original entry09:16/09:17, delay one
  minute; require the next minute's existing LUT_ON and finite saved full
  strategy counterfactual PnL. Otherwise skip that day's CW entry. No later
  re-scan/NW fallback is simulated. Other entry stages unchanged.
- Missing required features leave that candidate's original entry unchanged;
  report coverage and matched valid subset attribution, rather than hiding
  missing-data selection effects. Delay needs only one valid adverse trigger.
- 2021 warmup unchanged. Score2022+; fit all across-day percentile thresholds
  using completed prior years only. Use same-stage/same-CW-DTE group with
  >=30 prior valid observations, otherwise pooled prior CW with>=60. No
  tuning thresholds on forward PnL or normalization over future trades.
- Compare net profit, maximum DD, profit/DD, worst day, entries/skips/delays,
  exposure and yearly/stage results. Qualification needs net>baseline and
  net/DD>baseline with worst day no worse. Report all8 fixed candidates;
  this exploratory comparison is not an untouched final test.
- Legacy fixed base LUTs were fitted on broader history. Saved execution and
  counterfactual fill assumptions are unchanged. This is a conditional entry
  overlay study, not certified causal end-to-end strategy performance.
- Before scoring candidates, add an informational placebo check for all8:
  1,000 random trigger allocations (seed42), preserving trigger counts inside
  calendar-year/original-stage/DTE groups and candidate-feature availability.
  Keep each day's original or saved one-minute-delayed payoff assumptions.
  Report one-sided net and net/DD permutation tail fractions and an eight-rule
  Bonferroni net fraction. This does not repair legacy selection bias or the
  repeated research across earlier experiments and does not select new rules.
