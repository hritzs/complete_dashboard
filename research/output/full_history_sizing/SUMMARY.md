# GammaShort full-history sizing

- Owner-requested full-history analysis: all days fitted/evaluated together; NO 60/20/20 split.
- Dates 2021-01-01 through 2026-10-14; 1430 days; 799 recorded entries; 1428 days match the frozen production reference exactly.
- IN SAMPLE: PnL-fitted equations use outcomes across this same history. Rankings are descriptive, not unseen-data alpha or a live deployment decision.
- Sizing multiplier base 1; max 1.15; 09:16 CW never above 1; no average-size rescaling. Existing event-day base sizing remains inside the original PnL.
- Complete CW+NW book included; parameter/score/stability/calendar/borderline rules adjust CW only. IV/RV/VRP and drawdown rules can adjust both legs.
- Uses saved production PnL with linear size scaling, including its existing cost assumptions. No new tick replay, liquidity/impact model or fixed-fee recalculation.
- Normalized Rs = index points * 25000 / entry underlying. Extra_net is incremental historical profit, not factor-adjusted alpha. No Sharpe/CAGR without account-capital assumptions.
- Missing entry-window readings receive neutral adjustments for those inputs; availability counts are in Coverage. RV from same-contract closes up to yesterday.
- Whole-history drawdown is recomputed from the concatenated daily series; yearly results use the same full-history fit, not annual refits.
- Calendar candidates use DTE, Event, Weekday and absolute gap; combined calendar excludes the prior monthly-expiry proxy.
- Trades statistic counts nonzero PnL days; Entries counts actual positive-size recorded trades, including breakevens.
- Frozen 60/20/20 reports and their previous flat decisions remain separate historical analyses.

## Highest historical net / drawdown

| Candidate | Net | MDD | Net/MDD | Extra net | Mean size |
|---|---:|---:|---:|---:|---:|
| Bucket: Table + IV ratio | 9937.072 | 124.742 | 79.661 | -584.965 | 0.902 |
| S2 mild | 10289.012 | 130.310 | 78.958 | -233.025 | 0.935 |
| VRP high cut: SPREAD20 | 8786.903 | 111.327 | 78.929 | -1735.135 | 0.834 |
| Bucket cut: Table + IV ratio | 9877.180 | 125.402 | 78.764 | -644.857 | 0.896 |
| DD gradual cut | 9249.335 | 118.142 | 78.290 | -1272.703 | 0.905 |
| Bucket cut: Five table inputs | 9655.971 | 123.945 | 77.905 | -866.066 | 0.853 |
| S2 mild + DD guard | 10253.584 | 131.781 | 77.807 | -268.454 | 0.933 |
| Bucket: Five table inputs | 9855.920 | 127.153 | 77.512 | -666.118 | 0.868 |
| VRP tiers cut: VRP20 | 10264.471 | 134.873 | 76.105 | -257.566 | 0.971 |
| Bucket: Table | 9922.066 | 130.433 | 76.070 | -599.971 | 0.906 |

Flat: net 10522.04, MDD 144.37, net/MDD 72.88.
Best historical net: Calendar: Event; net 10587.37, MDD 145.50, net/MDD 72.77.
Candidates beating flat on both historical net and net/MDD: 0.

Artifacts: full_history_sizing.xlsx and candidates.csv in this folder. Reproduce with research/.venv/bin/python research/sizing_full_history.py.
