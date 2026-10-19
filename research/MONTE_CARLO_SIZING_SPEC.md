# Monte Carlo sizing scenarios — fixed before candidate simulation

2026-10-19. Research only. No average-size normalization, compounding, live
changes, entry changes or claim that simulation creates a trading edge.

## Inputs and chronology

Use the frozen799-entry legacy book and strict-prefix prepared inputs from
`output/walkforward_sizing`. Exclude182 warmup2021 entries from simulation.
Use2022–2024 (422 entries) for selecting policy parameters and2025–2026
(195 entries through2026-10-13) for subsequent historical evaluation and
evaluation-pool scenario simulations. These years have already been researched;
this is a chronological separation, not an untouched holdout.

Dicey flags come from the saved annual prior-year-only risk fits. Strong CW
eligibility is recomputed at the strict-prefix cell coordinates using saved
prior-year tax+2 or tax+4 masks. Missing required prefix/cell inputs cannot
qualify for a boost. NW cannot receive a CW-tax boost. Original base entry
taxes13/5/2/2/6 remain implicit in the frozen entry book; no tax is deducted
from realized PnL. Higher-tax mask training retains its legacy cache/input
limitations. Neither base entries nor fills are rebuilt in this study.

## Fixed candidate list

- Exposure benchmarks: fixed multipliers0.75,0.85,1,1.05,1.10,1.15.
- Risk-only: dicey0.75/0.80/0.85, otherwise1.
- Adaptive: dicey0.75/0.80/0.85; otherwise stronger-tax+2/+4 CW boost
  1.05/1.10/1.15; otherwise1. All18 combinations. Risk overrides conviction.
  09:16 may boost after its original tax13 gate and the stronger prior-fitted
  tax15/17 mask. Original entries remain taken. Total27 policies including
  benchmarks. Benchmarks deliberately do not implement the dicey cap.
- The same decision rule is used across every scenario, without looking at
  the resampled trade's own outcome. Sample outcome and its historical
  pre-entry state together. No return-dependent size chosen after outcome.
  This is a joint historical signal/outcome bootstrap. Annual signal models
  are not refitted inside each simulated path; original signal-state inputs
  (including prior-book stress) are resampled, not recomputed from that path.
  It is not a generative tick model or an online-feedback strategy simulation.
- For2021, keep every candidate at1 and label warmup. Never balance a future
  path, year or complete historical mean to1. Report actual means and exposure
  diagnostics, not cash returns or risk parity.

## Simulations and selection

Seed20261019. Each draw has5000 paths of150 consecutive trade opportunities,
not150 calendar days. This is a hypothetical horizon, not annualized output.
Use common sampled indices and adverse-outcome transformations across all
policies for paired comparisons. No capital/ruin estimate without a capital
base. Saved normalized-Rs net PnL and costs scale linearly with size.

Training selection uses non-wrapping moving-block bootstrap, block length10.
Select maximum mean terminal profit (all policies), maximum mean profit among
adaptive policies, and maximum mean profit subject to95th-percentile MDD no
greater than flat1's training95th-percentile MDD. Flat1 is a feasible candidate.
Freeze these selections before reading evaluation results. Return the complete
training frontier, rather than claim a global maximum. No post-score retuning.

Evaluation scenarios (same5000x150 horizon for all27 policies):

1. IID row bootstrap, length1: destroys sequencing; diagnostic only.
2. Moving-block bootstrap, length5.
3. Moving-block bootstrap, length10: main scenario.
4. Moving-block bootstrap, length20: longer local clustering.
5. Edge/cost stress: length10; positive net PnL x0.85, losses x1.25,
   then subtract0.50 normalized units per original unit trade. A declared
   hypothetical shock, not a forecast or calibrated execution estimate.
6. Adverse-block stress: length10; starts in the worst quarter of historical
   block net totals get3x sampling weight. This deliberately shifts the
   scenario distribution; its probabilities are conditional simulation
   frequencies, not estimated real-world event probabilities.

Report mean/median/5th/95th terminal net, mean/median/95th/99th MDD, mean of
worst5% terminal net, paired profit differences/intervals, probability of
beating flat net and of higher net with lower DD, actual mean/min/max sizes,
cuts/boosts and historical per-period results. Do not maximize the luckiest
single simulated path or advertise a simulation percentile as a guarantee.

## Artifacts and interpretation

Store all candidate trade/day/quarter/year comparisons in Excel and register
ledger iteration17. Save all simulated terminal/MDD outcomes and sampled
indices in compressed arrays, plus readable aggregate CSVs, parameters,
selection records, frozen tax/risk inputs, source hashes and verification.
Generate PNG/SVG charts of historical equity/DD, paired profit differences,
profit versus tail DD frontier, scenario comparisons and equity fan bands.

Charts must distinguish exposure benchmarks from adaptive policies and show
that a larger constant multiplier scales both net and DD. Bootstrap intervals
condition on the empirical book and chosen scenario; they do not incorporate
all model/selection/execution uncertainty or repair legacy entry-LUT bias.
