# Drawdown-first sizing specification

Recorded before evaluating this experiment. Research only, full-history fit
and evaluation as requested; no 60/20/20 split. This does not establish unseen
performance or authorize live changes. LUTs retain T=(13,5,2,2,6).

- Base dataset: production PnL from 1,430 daily records / 799 entries through
  2026-10-14. Earlier results are preserved.
- Mean multiplier exactly 1 over all recorded entries including breakevens;
  non-trade days excluded. Floor 0.75, global cap 1.15, CW 09:16 cap 1.
- Predictors: existing entry inputs, plus flat-book drawdown and consecutive
  losing trades known before the current entry. Those two stress readings use
  the reference flat book, not the sized book's own equity.
- Risk estimates exclude the outcome-fitted S1/S2 scores. Fit a penalized
  logistic classifier for a negative day (lambda 20, intercept unpenalized)
  and ridge expected downside max(-PnL,0) (lambda 50), using the remaining
  standardized entry features. Both fit on the full historical dataset.
- Instability is the average percentile of pre-entry underlying range,
  straddle range, and spread. Missing readings receive percentile 0.5.
- Composite risk = 0.4 * percentile(loss probability) + 0.4 *
  percentile(predicted downside) + 0.2 * instability. Dicey = top quartile of
  composite risk OR missing any of those three readings on a CW entry.
  NW entries use neutral 0.5 instability because the CW feature stream is not
  an NW entry reading. A risk flag is not a guarantee of a losing day.
- Fixed candidate dicey-day ceilings: 0.90, 0.85, 0.75. Flags and ceilings are
  enforced as position upper bounds during normalization and optimization.
  Never renormalize above a flagged day's ceiling. Report an infeasible
  total-size budget rather than relaxing any limit.
- Each ceiling: deterministic multi-start bounded coordinate pattern search
  with numerical mean-one projection at every evaluation. Objectives: minimum
  maximum drawdown, maximum PnL/drawdown, maximum PnL. All optimized selections
  must have PnL >= flat, MDD <= flat and worst daily PnL >= flat worst day.
  Feasibility-first search may explore violating points, but none qualifies
  as a result. Failure to find a feasible policy is reported explicitly.
- Save risk definitions, daily flags/reasons, sizes/PnL, coefficients,
  transforms, yearly/leg/risk-group attribution, search trace, budget checks,
  input fingerprints, equity and drawdown charts. Include flat and prior
  normalized optima as benchmarks, which do not obey the new dicey ceilings.
- Report in-sample winners and risk-group size checks. No candidate is selected
  by assigning reduced sizes to realized losing days directly. Realized PnL
  is a fitting target and evaluation metric; same-day outcomes are not inputs.
- Existing saved PnL/costs scale linearly. Event-adjusted exposure is reported
  separately; arithmetic mean multiplier 1 is not identical cash or risk.
  Fractional sizes are research allocations, not executable rounded lots.
