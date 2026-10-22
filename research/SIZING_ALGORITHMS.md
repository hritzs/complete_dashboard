# What controls boosts, cuts and restrictions

Iteration19 compares independent old/latest entry, hedging and exit replays
at size1 with identical frozen inputs and tables, retaining the archive as a
separate vintage reference (`LOGIC_COMPARISON_SPEC.md`). Iteration20 adds a
separate premium-budgeted long-option screen alongside latest GS
(`COMPLEMENT_STRATEGY_SPEC.md`). Neither reuses old PnL scaling as a new engine
or treats additional capital as allocation alpha. A24/A25 document the methods.

For20, flat means **latest GS**. Generic Entries counts active portfolio days;
Underlying_strategy_entries counts the individual GS and long-option positions.
Generic Size is the fixed policy coefficient, not option contract lots. The
source workbook stores actual fractional quantities, premium budget, fees,
entry greeks and combined paths. A30% premium stop can overshoot; additional
modeled fees are not a verified statutory/broker fee schedule. No neural model
or established profitable complement is claimed by the fixed first screen.

Iteration18 extends17 with1944 fixed parameter combinations and chronological
inner tuning/validation before each outer year. No normalization occurs. The
new factors are risk-weight/quantile sensitivity, profit-score guards,
volatility dampening and a prior realized-DD cap. `Algo_Nested_Selection` and
`Algo_Nested_Replay` store the actual dated choices and executable size rules.
Strict selectors may revert to the baseline control when no shortlisted
policy validates; this is not a new admitted risk policy. Exploratory results
are labelled separately. Random placements preserve identical sizes/exposure
and test timing of allocation, not larger average positions. See
`NESTED_SIZING_SPEC.md` and `output/nested_sizing/REPORT.md`.

Latest iteration17 uses **no mean normalization**. Its27 predeclared sizing
rules compare fixed exposure, risk-only cuts and risk-first stronger-tax
boosts. Read `MONTE_CARLO_SIZING_SPEC.md` for the full protocol and
`output/monte_carlo_sizing/REPORT.md` for charts and results. Monte Carlo
resamples paired historical signals/outcomes; it does not manufacture an edge.
Policy selection uses2022–2024 before evaluation-pool simulations on2025+.
`Algo_MC_Parameters`, `Algo_MC_Selection` and `Algo_MC_Replay` explain and
recompute all27x799 daily sizing decisions, including2021 warmup at1.
The earlier normalization experiments below remain archived, not the current
instruction. Existing legacy entry/fill/cache limitations still apply.

These are research algorithms, not a statement that a new sizing model is
active in live execution. The original reporting replay does not refit archived
models. The separate annual rerun15/16 below refits sizing models using prior
years only, with no live change. Earlier annual higher-tax and tick-entry
results did not justify adoption over flat size1.

## Latest annual higher-tax rule: order matters

1. The original strategy must first approve the entry. Published stage taxes
   remain **13,5,2,2,6**. A tax is a LUT construction hurdle, not a PnL charge.
2. **2021 warmup:** use multiplier 1; no learned annual conviction estimate.
3. Score risk using the saved previous-year logistic/downside models and
   percentile references. If dicey, use **0.85**, even if conviction is strong.
4. Otherwise a CW cell surviving the separate higher-tax LUT gets **1.05 or
   1.10**, according to the fixed candidate. This includes 09:16: it must pass
   both the original tax-13 gate and tax-15 or tax-17 conviction gate.
5. Otherwise use **1**. NW cannot receive a CW higher-tax boost, but can receive
   a model-driven risk cut. There is no whole-future-period mean-one balancing.

    size = 1                         if warmup
           0.85                      if dicey
           candidate_boost           if CW and higher_tax_mask[cell]
           1                         otherwise

The gate is binary and the boost is fixed by candidate, not proportional to a
neural-network confidence. No neural net currently determines these sizes.

## How the higher-tax gate is learned

For every observed six-dimensional LUT cell, accumulate prior-year modeled
post-cost counterfactual profit P and event-weighted evidence count N:

    cell_weight_cents = round(100 * (P - N * tax))

Weights are optimized jointly under the production monotonicity implications,
using **maximum-weight closure / minimum cut**, implemented with iterative
Dinic maximum flow. Positive cells with weighted evidence below 1 cannot
anchor a closure; zero weights receive the original small sink tie penalty.
Unobserved cells are expanded by implication, not independently fitted.
Stage cascading is applied after solving: stage1 &= stage2, stage0 &= stage1,
stage3 &= stage2, stage4 &= stage3 (zero-based indexing). The complete stronger
tax vectors are (15,7,4,4,8) or (17,9,6,6,10).

This determines whether a cell belongs to a stronger historical profit-hurdle
region. It does not output a calibrated probability or guarantee future profit.
Masks and risk models are fitted through the preceding December 31. Legacy
base LUTs used broader history, and the old feature/fill pipeline is not
certified causal, so this remains a conditional overlay experiment.

## Earlier full-history strong-edge and bounded optimization

The earlier conviction definition used two profit scores:

    S1 = sum of (bucket_mean_PnL - training_mean_PnL) * n/(n+20)
    S2 = standardized linear ridge profit score, lambda = 10

S1 groups continuous inputs into training quintiles and uses actual levels for
discrete inputs. Shrinkage reduces small-bucket effects. S2 fits centered PnL
against standardized inputs and table indicators. The v4 report adds mean PnL
to its S2 predictions; the later v5/full-history implementation uses the
centered score. This shift affects units, not ranking within that model.

    strong = CW
             AND NOT dicey
             AND standardized_S1 >= CW 2/3 quantile
             AND standardized_S2 >= CW median
             AND estimated_loss_probability <= book median

These thresholds were fitted on the full history. The archived experiment
blocked 09:16 boosts; that blanket ban was superseded for the later annual
higher-tax experiment, not retroactively removed from old results.

For the feasible "Modest priority" profile:

- Eligible strong entries: **1.10–1.15**.
- Dicey entries: **0.75–0.85**, overriding conviction.
- Ordinary entries: **0.75–1.10**; 09:16 CW remains at most 1 in this archive.
- Whole recorded trade arithmetic mean: exactly **1**.

The "Gentle" profile was infeasible: its maximum aggregate size was 792.35
against a required budget of 799. It was not silently relaxed or executed.
An ordinary boost in this optimizer can be a budget reallocation; it must not
be labelled a high-conviction trade. The archived selected policy had 32
strong-edge boosts and 374 ordinary reallocations.

## Continuous sizing, projection and optimization

The continuous policy has transformed input vector X and fitted allocation
coefficients b, separate from risk-model coefficients beta/gamma:

    raw_size_i = 1 + sum_j X_ij * b_j
    final_size_i = min(upper_i, max(lower_i, raw_size_i + offset))
    sum_i final_size_i = number_of_recorded_entries

The **bounded projection** finds one common offset by monotone bisection
(43 iterations). It enforces lower/upper bounds and exact historical mean 1
together. Dividing by the mean then clipping would not enforce both.

The **deterministic multi-start coordinate pattern search** adjusts allocation
coefficients within [-1,1]. It uses fixed-seed Gaussian starts, one-feature
starts, prior warm starts where specified, and decreasing coordinate steps.
It retains the best feasible policy observed for each objective. It is a
local numerical search, not a neural net or proof of a global optimum.

Normalized sizing compares maximum profit, profit subject to flat DD cap, and
maximum net/DD subject to a flat-profit floor. DD-risk and conviction searches
compare minimum DD, maximum net/DD and maximum profit while requiring net at
least flat, DD no greater than flat and worst day no worse than flat. A small
1e-5*mean(b^2) penalty discourages unnecessary coefficients. Optimizer-specific
steps, seeds and qualification tests remain in the original source files.

These optimizations and their balancing offsets use complete historical PnL.
They are in-sample allocation exercises, not a live way to guarantee a future
mean of 1. The saved coefficients/offsets and every candidate size are replayed
in the Excel report, including active floor/ceiling and the action reason.

## Other archived algorithm families

- **Shrunk bucket adjustments:** size contribution
  0.6*(bucket_mean-overall_mean)*n/(n+40)/overall_mean. Combines table,
  stability, volatility-risk-premium or calendar buckets, then clips by the
  experiment's bounds. Determines historical relative allocation, not risk
  probability. Earlier versions fit training splits; later versions full-fit.
- **Score terciles:** map low/middle/high S1/S2 scores to fixed multipliers,
  such as 0.75/1/1.15, 0.5/1/1.15, or cut-only/skip-low alternatives.
- **Volatility/stop scaling:** median(volatility)/current volatility, or
  cushion/median(cushion), clipped 0.5–1.15; separate 14/stop_bps experiment.
  Determines exposure scaling, not expected edge.
- **LUT-boundary restriction:** reduce size near a threshold whose neighboring
  cell is NO, near veto/floor, or for a YES cell with <=1 YES neighbor. Fixed
  distance tests are 2%/5%; fixed cuts are 0.75/0.5 as specified by candidate.
- **Drawdown/streak guards:** use earlier realized flat-book results for
  three-loss, DD>100 or gradual-cut rules. The v5 top-boost guard instead uses
  the sized book's own prior DD>=50 to replace a boost with 1.
- **Tick entry overlays:** percentile/residual/spread/stress gates skip or
  defer an existing CW entry, rather than generate a size boost. Features use
  strictly earlier ticks. All eight fixed candidates failed qualification.
- **Relaxed entry:** after a fixed time, admit a previously NO 09:20+ cell
  with required YES-neighbor support, replacing a later entry or filling a
  no-entry day. Saved alternatives use multiplier 1 or 0.75. This changes the
  entry set, unlike a multiplier applied to the same trades.

`Algo_Catalog` explains inputs, formula, output, fitting and limitations for
each family. `Algo_Policy_Map` links all saved policy IDs to these families.
`Algo_Changes` distinguishes archived restrictions from later overrides.
`Algo_Conviction` replays the earlier edge gate; `Algo_Annual` replays all four
annual sizing branches; `Algo_Opt_Replay` replays continuous allocations and
contains executable Excel raw-size and clipping formulas. The existing master
workbook retains all trade and period PnL comparisons.

## Annual rerun of maximum-profit iterations10/11

Ledger15/16 repair the sizing fit: 2021 is warmup at size1; for each later
calendar year, fit all imputations, scales, S1/S2 scores, risk models,
percentile references and allocation coefficients using completed prior years.
Rebuild intraday inputs strictly before entry, using fresh quotes and backward
as-of snapshots. Missing CW instability forces a risk flag; other imputation
medians come from that fold's training rows. The fixed 40/40/20 score remains.

The continuous rule is `raw=1+X*b`. A bounded piecewise-linear projection finds
an offset giving training mean1. Save that offset with the model and keep it
unchanged for the scored year: `size=clip(raw+saved_offset,.75,cap)`. Do not
recalculate an offset from the scored year's trades. Its actual mean can be
below or above1; exact future mean1 is not guaranteed. An ex-post flat book
scaled by the actual mean is a diagnostic only, never a live rule.

WF10 fits34 allocation inputs for maximum prior profit. WF11 fits38, adding
prior flat-book drawdown/streak and risk predictions. The two searches use
their declared seeds/starts/steps, coefficients bounded[-1,1], and no archived
full-history weights or warm starts. The full settings were fixed in
`WALKFORWARD_SIZING_SPEC.md` before inspecting new candidate PnL.

Bounds retain the archived floor.75, global cap1.15 and09:16 CW cap1 for this
specific rerun. WF11 additionally caps every dicey entry at.85, and requires
training profit>=flat, MDD<=flat and worst day>=flat. If mean-one constraints
are infeasible or search finds no qualifying fit, use explicit risk-only
fallback: size.85 on dicey entries and1 otherwise. No future normalization and
no relaxation of the risk cap occurs. Training qualifications do not constrain
future realized DD or profit. WF10 has no mandatory dicey cap.

`Algo_WF_Folds` gives each training cutoff, status and frozen offset;
`Algo_WF_Inputs` and `Algo_WF_Coefs` retain each scoring row and annual weights;
`Algo_WF_Replay` uses executable SUMPRODUCT and clipping formulas to replay all
617 scoring entries for both policies. Warmup182 entries remain flat. Saved
JSON models also retain risk/scoring transformations and percentile samples;
`audit_walkforward_sizing.py` replays these independently without fitting.

This isolates the sizing correction on the existing entry book. Legacy base
LUT construction and fill assumptions still prevent an end-to-end unbiased
claim. Previously researched historical years are not an untouched holdout.
These two reruns do not add iteration12 conviction gates or iteration13
higher-tax boosts, and do not change the live system.

## Iteration21: fixed CW stop/regime exits, unchanged sizing

Algorithm A26. The baseline is the frozen latest GS book at size1. Four fixed
overlays use gross CW stop multipliers0.75/0.875, a persistent completed-bar
underlying regime exit, or0.875 plus that exit. Exit cash uses the current
option/futures position and accrued hedge costs, discarding all later hedges.
It is not daily-PnL clipping, not an old PnL multiplier and not a sizing study.
NW remains unchanged. Source-model intraday DD carries prior intraday equity
peaks across days; it inherits legacy source fills/backfills and is not
certified executable account DD. The complete predeclared recipe is in
`GS_INTRADAY_STOP_REPLAY_SPEC.md`; no model or optimized stop is adopted.

## Iteration22: position-adjustment implementation audit

A27 compares unchanged GS with protective wings, a roll, a tested-side long
option, an untested-side short option, a next-week long straddle and extra
short straddles. Entries/base sizes are unchanged; additional positions and
rolls have their own modeled costs. These are exploratory saved implementation
paths, with raw quote accounting separated from failed strike-at-fill recipe
conformance. Original trades are retained and every deviation is reported.
See `GS_ADJUSTMENT_AUDIT_NOTES.md`. No policy is adopted or labeled causal.
