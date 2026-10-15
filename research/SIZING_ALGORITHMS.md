# What controls boosts, cuts and restrictions

These are research algorithms, not a statement that a new sizing model is
active in live execution. This reporting change does not retrain any model or
change a strategy. Existing results did not justify adopting the latest annual
higher-tax overlay or tick-entry filters over flat size 1.

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
