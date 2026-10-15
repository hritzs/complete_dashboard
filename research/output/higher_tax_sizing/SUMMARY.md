# Higher-tax conviction sizing including09:16

- Updated owner rule: 09:16 may receive a modest boost if its base-tax13 cell ALSO survives a separately fitted tax15 or17 LUT and is not dicey. Original base LUTs/taxes are unchanged.
- Annual sizing overlay fits use prior years ONLY. 2021 is warmup at size1. Risk scalers, classifier/downside models, empirical ranks/thresholds and extra-hurdle LUTs are saved per fold. No future whole-period normalization or coefficient optimization.
- Four fixed candidates: tax increments2/4 × boosts5%/10%; dicey size0.85, ordinary size1. CW risk cuts override high-tax conviction. NW receives no CW-tax boost.
- Actual mean is reported, not forced to1 using future dates. Same_mean_flat_net is an EX-POST exposure-matched diagnostic, not a live benchmark decision or normalization rule.
- Conditional overlay only: the LEGACY fixed entry LUTs used wider history, including later observations. Original feature/cache timing and execution are not certified causal. Do not call these end-to-end leakage-free strategy returns.
- Counterfactual source: round48 no-roll ps1, cache ends 2026-08-19. 2026 LUT/risk fitting stops2025-12-31. Counterfactual PnL includes matching modeled entry/hedge costs. The legacy published LUTs predate this no-roll refit; they are retained as entry gates, not re-solved.
- Monotone higher-tax LUTs use the original maximum-closure objective totalPnL-weightedCount*tax, cents rounding, minimum anchor evidence1 and stage cascade. A compressed observed-cell solver is checked against brute-force closures; unobserved cells expand by implication.
- Saved production PnL/costs scale linearly; fractional research allocations, no lot rounding, new fill replay or fixed-fee recalculation. Units normalized Rs. These are research artifacts; no live service edits.

| Candidate | Net | MDD | Net/MDD | Mean size | Boosts09:16 |
|---|---:|---:|---:|---:|---:|
| Tax+2 boost1.05 | 10338.15 | 144.15 | 71.72 | 0.980163 | 66 |
| Tax+2 boost1.1 | 10447.98 | 149.90 | 69.70 | 0.993179 | 66 |
| Tax+4 boost1.05 | 10324.02 | 144.15 | 71.62 | 0.978223 | 61 |
| Tax+4 boost1.1 | 10419.72 | 149.90 | 69.51 | 0.989299 | 61 |
| Flat size 1 | 10522.04 | 144.37 | 72.88 | 1.000000 | 0 |
