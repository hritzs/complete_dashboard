# Trade comparison and conviction boosts

- Specification fixed in CONVICTION_SIZING_SPEC.md before evaluating new policies. Same frozen trades/risk model and full-history input matrix as previous DD study.
- Strong CW edge = S1 upper tercile and S2 >= CW median, estimated loss probability <= book median, no dicey flag. 09:16 strong trades are explicitly blocked from boost. NW lacks this two-score confirmation.
- Gentle profile: strong 1.05-1.10, ordinary <=1.05. Modest priority: strong1.10-1.15, ordinary <=1.10. Dicey <=0.85; 09:16<=1; floor0.75; arithmetic mean1 over799 entries. Infeasible budgets are reported without relaxing constraints.
- Modest priority ensures eligible strong trades are never sized below ordinary trades. Normalization preserves strong-entry lower bounds and risk upper bounds. Other increases can be budget reallocation, not a high-conviction label.
- New numerical optima must preserve flat PnL, DD and worst-day limits. Benchmark rows may violate new conviction lower bounds/ordinary caps and are marked Benchmark.
- Trade comparisons include all799 entries with actual date/leg/stage/available strike/expiry and flat/previous/new size and PnL. Example trades are first two chronologically in each chosen group, not selected on profit.
- Full-history scores, risk estimates, thresholds, optimization and normalization are IN SAMPLE, not measured future edge. No live changes. LUT tax vector13/5/2/2/6 unchanged.
- Existing saved PnL/costs scale linearly, without new execution replay, fixed-fee recalculation or lot rounding. Arithmetic mean size1 does not imply identical event-adjusted cash exposure.

Strong estimated edge 166; eligible boosts 32; 09:16 blocked 134.
Selected by lowest qualifying DD: Modest priority: Maximum ratio; net 10938.10, MDD 108.63, ratio 100.69, worst day -56.25.
Size actions: cut 251, unchanged 142, boosted 406; mean 1.000000000000.
Eligible strong size min/mean/max 1.1000/1.1406/1.1500.

All799 trades: trade_comparison.csv. First chronological examples: example_trades.csv. Full workbook: conviction_trade_comparison.xlsx.

## Policy comparison

| Policy | Net | MDD | Net/MDD | Cuts | Unchanged | Boosts | Mean size |
|---|---:|---:|---:|---:|---:|---:|---:|
| Flat size 1 | 10522.04 | 144.37 | 72.88 | 0 | 799 | 0 | 1.000000000 |
| Previous DD-first | 10749.08 | 105.41 | 101.97 | 300 | 120 | 379 | 1.000000000 |
| Modest priority: Maximum ratio | 10938.10 | 108.63 | 100.69 | 251 | 142 | 406 | 1.000000000 |

## Trade groups for the selected policy

| Group | Entries | Previous mean size | New mean size | New minus previous PnL |
|---|---:|---:|---:|---:|
| Dicey risk cut | 201 | 0.8057 | 0.8250 | 46.81 |
| Other 09:16 | 26 | 0.9717 | 0.9949 | 4.52 |
| Other entry | 406 | 1.1015 | 1.0811 | -43.53 |
| Strong edge 09:16 blocked | 134 | 0.9660 | 0.9840 | 118.14 |
| Strong edge boost | 32 | 1.0973 | 1.1406 | 63.08 |

## First chronological trade examples

| Date | Leg | Stage | Group | Flat size | Previous size | New size | Flat PnL | New PnL |
|---|---|---|---|---:|---:|---:|---:|---:|
| 2021-01-04 | CW | 09:20+ | Dicey risk cut | 1 | 0.7500 | 0.8500 | 44.50 | 37.83 |
| 2021-01-06 | CW | 09:17 | Dicey risk cut | 1 | 0.8500 | 0.8500 | -45.31 | -38.51 |
| 2021-01-05 | CW | 09:17 | Strong edge boost | 1 | 1.1500 | 1.1500 | -10.68 | -12.29 |
| 2021-02-03 | CW | 09:18 | Strong edge boost | 1 | 1.1500 | 1.1500 | 54.11 | 62.23 |
| 2021-01-19 | CW | 09:16 | Strong edge 09:16 blocked | 1 | 1.0000 | 1.0000 | 39.15 | 39.15 |
| 2021-01-27 | CW | 09:16 | Strong edge 09:16 blocked | 1 | 1.0000 | 1.0000 | 48.26 | 48.26 |

## Budget feasibility

- Gentle: lower budget 608.85, upper budget 792.35, required 799; feasible=False.
- Modest priority: lower budget 610.45, upper budget 814.25, required 799; feasible=True.
