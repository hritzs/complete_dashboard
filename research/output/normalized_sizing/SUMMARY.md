# Mean-one GammaShort sizing optimization

- Latest owner instruction supersedes the previous no-rescaling rule: normalize arithmetic mean size to EXACTLY 1 across all recorded traded days (including breakeven entries). No-trade days excluded.
- Complete history 2021-01-01 to 2026-10-14: 1430 days, 799 entries; frozen reference agrees on 1428 dates. No 60/20/20 split.
- Full-history fit, coefficient optimization AND normalization: explicitly IN SAMPLE. Exact full-period normalization needs the full future score distribution; this is not a causal live sizing policy or a deployment decision.
- Size floor 0.75, cap 1.15. CW 09:16 upper bound remains 1. All entries are taken; there is no 0/1 trade filter. Flat is feasible.
- Normalization is a box-and-sum Euclidean projection: size=clip(raw+offset, lower, upper), offset solved by bisection. Mean stays 1 after limits; dividing then clipping would not guarantee this.
- Historical rule templates are projected under the same constraints. Normalization can raise formerly cut-only rules and change their NW size; source rule names describe the original template, not the final exposure scope.
- Optimized policy: size=clip(1+sum(beta*feature)+offset, lower, upper); numeric features median-filled, standardized and clipped [-3,3]. Indicator features use entry-known stage/DTE/calendar/leg. S1/S2 are full-history-fitted predictors.
- Technique: seeded multi-start bounded coordinate pattern search (beta [-1,1]), coarse-to-fine steps, exact mean projection at every evaluation. Original starts retained alongside cross-objective warm starts; each objective reports its best feasible policy seen anywhere in the search. Global optimality is not claimed.
- Objectives: maximum net; maximum net with MDD <= flat; maximum net/MDD with net >= flat. Hard constraints are tested during search. Tiny coefficient penalty 1e-5*mean(beta^2) discourages unnecessarily large coefficients.
- Mean 1 is an arithmetic mean of multipliers, not risk parity or identical rupee exposure. Existing event reductions remain in base PnL. Event_weighted_exposure_ratio reports event-adjusted notional proxy; it need not equal 1.
- Saved production net PnL and existing costs are scaled linearly. No quantity-specific execution replay, nonlinear market impact, lot rounding or recalculated fixed fees. Units: normalized Rs for 25000 notional.
- Full-history mean is 1; annual and CW/NW subgroup means can differ. Drawdown is recomputed over the whole daily sequence, not summed by year.
- All historical split reports and unnormalized full-history report remain preserved. Prior live operating conclusion is not changed by this descriptive optimization.

| Candidate | Net | MDD | Net/MDD | Mean size | Extra net |
|---|---:|---:|---:|---:|---:|
| Optimized: Ratio profit floored | 11243.251294 | 113.890905 | 98.719484 | 1.000000 | 721.213990 |
| Optimized: Max profit | 11495.560161 | 128.764963 | 89.275529 | 1.000000 | 973.522856 |
| Optimized: Profit DD capped | 11495.560161 | 128.764963 | 89.275529 | 1.000000 | 973.522856 |
| Flat size 1 | 10522.037304 | 144.365888 | 72.884512 | 1.000000 | 0.000000 |

69 candidates; 15 beat flat on both full-history net and net/MDD.
Highest historical ratio among candidates beating both: Optimized: Ratio profit floored; net 11243.25, MDD 113.89, net/MDD 98.72.
