# Trade comparison and modest conviction boosts

Written before evaluating the new sizing candidates. Full-history research only.

1. Reuse the frozen 799-entry dataset, entry risk scores/flags, 38 model inputs,
   and flat/DD-first benchmarks from `output/drawdown_risk_sizing/`. Retain
   LUT taxes (13,5,2,2,6), all entries, mean multiplier 1, floor 0.75, global
   maximum 1.15, dicey ceiling 0.85 and CW 09:16 ceiling 1.
2. Strong estimated edge: on CW entries, saved S1 score is at least its CW
   top-tercile threshold AND saved S2 score is at least its CW median.
   Require loss probability <= whole-book median and no dicey flag.
   All thresholds use the full history. These are fitted estimates, not
   validated future confidence. NW has no equivalent two-score confirmation
   and cannot receive this high-conviction label.
3. High-conviction boost eligibility additionally excludes 09:16 CW. Show
   strong-but-09:16-blocked trades explicitly. Risk always overrides edge.
4. Fixed boost profiles:
   - Gentle: eligible strong entries size 1.05–1.10; other non-dicey entries
     at most 1.05, with 09:16 still capped at 1.
   - Modest priority: eligible strong entries size 1.10–1.15; other non-dicey
     entries at most 1.10. Thus strong entries never size below ordinary
     entries. Dicey entries stay 0.75–0.85 under both profiles.
   Check total lower/upper budgets before searching. Report infeasible
   profiles without relaxing mean-one, risk cuts or the 09:16 restriction.
5. For each feasible profile reuse the seeded feasibility-first coordinate
   optimizer, with minimum-DD, maximum-ratio and maximum-profit objectives.
   Qualifying results must preserve flat profit, DD and worst-day limits.
   Select the lowest observed qualifying DD; do not change the edge gate
   after observing PnL. Exact normalization must preserve the conviction
   lower bounds as well as all risk/quantity upper bounds.
6. Save an all-trades CSV and workbook comparing date, leg/stage, entry/exit
   time, available strike/expiry, flat vs previous DD-first vs new size/PnL,
   action/reason, estimated risk/edge scores, and conviction eligibility.
   Also summarize cut/unchanged/boost counts, PnL attribution by action and
   conviction/risk group, total profit/DD/worst day, and search feasibility.
   Example trades are chosen chronologically within groups, not by PnL.
7. Same limitations as the preceding research: outcome-fitted scores and
   full-period thresholds/normalization are in sample; saved net PnL/costs
   scale linearly; fractional sizes are not rounded live lots. Average
   multiplier 1 does not guarantee identical event-adjusted cash exposure.
