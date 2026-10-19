# Chronological parameter sweep and equal-exposure placement diagnostics

Iteration18, 2026-10-19. Fixed before running this new sweep. Research only.
No sizing normalization, live changes or claim of an untouched historical test.
The constant1.15 benchmark in17 is not allocation alpha. This study selects
variable-size combinations using only earlier dates and tests their placement.

## Data and limitations

Use799 legacy entries, the saved strict-prefix inputs, annually prior-fitted
risk/profit model states and stronger-tax masks from studies15–17. Warmup2021
fits the annual models;2022 supplies the first chronological calibration
period. Scored outer years are2023,2024,2025,2026 (442 entries). For each outer
year use only completed2022+ earlier entries for policy tuning. The annual
feature/risk models themselves learned only earlier years, including2021.
No scored-year outcome is used in that year's parameter selection.

Base entry LUTs, cache/fill assumptions and the existing trade list remain
legacy; this is a causal sizing calculation conditional on that book, not an
end-to-end no-lookahead rebuild. Do not change entries/stops/targets from saved
PnL alone. Previously inspected years cannot become an untouched holdout.

## Fixed grid:1944 combinations plus flat1

- Risk weights(loss rank,downside rank,instability):(.4,.4,.2),(.5,.3,.2),
  (.3,.5,.2). For each annual model, derive thresholds from that model's
  earlier training-score reference only, at quantiles.60,.70,.75.
- Risk flag is the original annual dicey flag OR this candidate's flag OR
  missing CW instability. Existing mandatory dicey cuts can never be removed.
- Risk size.75/.80/.85; stronger-tax increment+2/+4; boost1.05/1.10/1.15.
- Profit-score guard: none, both prior-standardized S1/S2>=0, or both>=.5.
  Strong eligibility always also needs CW and the selected higher-tax mask.
- Volatility dampener: off, or multiplier clip(prior training median IV /
  strictly pre-entry IV,.85,1). Missing IV gives neutral dampener1; missing CW
  instability still forces risk cut. It cannot create a new boost.
- Prior realized flat-book DD guard: off, or size<=.85 when preceding DD>75
  normalized units. Earlier outcomes are available inputs; own-day PnL is not.
- Formula: base risk cut / strong boost / ordinary1; multiply by dampener,
  clip.75–1.15, cap at1 unless strong, then apply optional prior-DD cap. Warmup
  and2022 calibration stay1 in the scored policy book. NW cannot boost;09:16
  may boost only through the stronger-tax gate after the original13 entry.

## Nested selection, frozen before each outer year

Split the earlier2022+ entries chronologically60% inner training,40% inner
validation. Require at least50 training and30 validation entries. Compute all
1944 candidate metrics on both slices, never tune using the outer year.
Deduplicate identical training-size paths before taking the top10 per search.

Training eligibility: net>flat, MDD<=1.10*flat, mean size<=1.03, and positive
net excess versus an ex-post uniform flat book at that candidate's mean. This
last quantity is a historical selection diagnostic, not normalization.
Rank eligible training candidates by net or net/DD as appropriate.

Frozen output policies:
1. Strict profit: top10 training-net candidates; among inner-validation
   qualifiers choose highest net. Qualifier requires net>flat, MDD<=flat,
   worst>=flat, mean size<=1.03 and positive allocation excess. Otherwise flat.
2. Strict ratio: top10 training net/DD; same validation qualification; choose
   highest validation net/DD. Otherwise flat.
3. Limited-DD profit: top10 training-net; same qualification except permit
   MDD<=1.10*flat and worst>=1.10*flat worst. Otherwise flat. This10% is a
   declared research allowance, not an approved real-capital risk budget.
4. Exploratory allocation: top10 unique candidates by training allocation
   excess, even if no strict qualifier; choose highest validation allocation
   excess. Always label exploratory; not an adopted policy or validation pass.

Report every combination's inner train/validation metrics and all chosen fold
parameters, cutoffs, qualification/fallback reasons, subsequent outer-year
results, trade/daily/quarter/year/month/FY paths and actual means. Sensitivity
charts summarize inner validation only; do not choose a new winner using
outer-grid results. Fixed seeds and no post-score retuning.

## Monte Carlo placement diagnostics: identical sizes, different placements

5000 Block10 paired paths of150 outer trade opportunities, seed20261020.
For each variable policy and each drawn path, keep its original size multiset
and randomly permute those exact sizes along that same outcome path. Do not
normalize, resize or pick the best permutation. Compare paired profit and DD,
actual-minus-random profit distribution and identical size sums. Also perform
5000 within-year permutations of the actual442-trade evaluation sequence,
preserving every year's exact size distribution and exposure.

The random placements are counterfactual null diagnostics, not live policies:
they may violate the original risk/conviction gate on individual positions.
The actual tested policies must satisfy their hard bounds/gates. Conditional
permutation frequencies are not calibrated future probabilities or proof of
alpha; repeated research/model uncertainty remains. Report all selected
streams including failures. No lucky simulated maximum is an objective.

Generate PNG/SVG sensitivity, historical equity/DD and exposure-matched
allocation charts, an Excel report and compressed full sensitivity/path draws.
