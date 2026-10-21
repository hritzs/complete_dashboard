# GammaShort research summary and continuation state

Updated 2026-10-21. This is offline research; it contains no authorization to change live trading behavior.

## 2026-10-21: latest GS and complementary long-option screen

Both studies are complete and independently verified on the same1,430 dates.
Latest matched GS is10,109.38 / maximum DD168.42 versus freshly replayed old
rules10,594.58 /144.37, on the same794 entries. Full details and actual input
vintages are in handoff4.22 and `logic_comparison_20261021/REPORT.md`.

The owner authorized a separate-risk sleeve alongside GS. Six fixed
long-option alternatives used100 normalized premium units at entry, with
actual ask/bid execution models and declared fee drag. Plain directional
call/put net is+5.27 across8 trades over all history, but **-43.99 across7
trades from2022 onward**. Lower-IV directional net+40.55 comes from only2
trades including the2021 winner;2022+ net is-8.71 across1 trade. All six later
window add-ons lose money. None lowers GS DD168.42; none is adopted.

See handoff4.23, `COMPLEMENT_STRATEGY_SPEC.md`,
`complement_strategy/complement_strategy.xlsx` and its REPORT.md. Every
individual/combined daily path, entry/exit, cost, Greek, formula and calendar
period is retained.30 policy entries represent only8 distinct dates, not30
independent observations. The sampled data has1-second timestamps with1900
date placeholders, not subsecond order-event data.11 ineligible data/window
dates were explicitly audited; raw exceptions remain preserved. No live code,
.env or shared inputs changed. Neural models and a new GS entry state machine
remain separate unimplemented hypotheses.

## Latest: chronological parameter sweep and allocation-placement test

The owner rejected future mean balancing and constant-multiplier profit as
allocation alpha. Iteration17 stores the six-scenario Monte Carlo study;
iteration18 adds a chronological1,944-combination parameter sweep and an
identical-exposure placement test. No live code, entries or tax vector changed.

Iteration17:27 policies;5,000 paired paths per scenario;150 trade opportunities
per path. Selection uses2022–2024, with2025+ as the evaluation pool. Fixed1.15
is only an exposure control, not an alpha result. At the flat1 training-tail
DD budget, the selected policy is flat. The training-selected adaptive policy
also failed in the later pool. See `monte_carlo_sizing/REPORT.md` and
`monte_carlo_sizing.xlsx` in that directory.

Iteration18 varies risk rank weights/cutoffs, cuts0.75/0.80/0.85, boosts
1.05/1.10/1.15, extra tax+2/+4, S1/S2 guards, IV dampening and prior realized-DD
caps. For each2023–2026 outer year, earlier2022+ observations supply a60/40
inner train/validation split. Annual input models remain prior-year fitted.
Top10 distinct inner-training paths are shortlisted before inner validation;
chosen parameters freeze before the scored year. No outer-grid winner is
selected after outcomes.2021–2022 stay flat calibration/warmup in this book.

None of the shortlisted policies passed the declared validation requirements,
including the relaxed10%-DD selector. All strict selectors explicitly fell
back to the original flat control; this is not a newly admitted risk policy.
The exploratory selector is reported despite failure, not adopted.

Outer2023+ results on442 trades, in normalized backtest units:

| Policy | Net | Max DD | Mean size |
|---|---:|---:|---:|
| Flat / strict baseline fallback | 5,375.05 | 126.39 | 1.0000 |
| Exploratory allocation | 4,770.88 | 114.43 | 0.8869 |

Exploratory net changes-11.24% and DD-9.47%.
It reduces participation; this is not a higher-profit/lower-DD result.
Against5,000 within-year random permutations of **the same exact sizes**, its
actual net exceeds the random mean by only8.90;
the upper-tail permutation frequency is0.4597.
Across5,000 Block10 paired paths of150 trades, it beats random placement in
50.74% of paths. No persuasive placement edge was established.
The null placements are counterfactual diagnostics and may violate individual
risk/conviction gates; actual admitted policies retain their constraints.

Parameter sensitivity charts use earlier inner validation only. IV dampening
and prior-DD guards lower mean validation DD but reduce allocation excess;
larger boosts increase allocation excess and DD. These descriptive averages
across correlated candidates are not causal effects or new outer winners.

All1,944 combinations' inner metrics,16 dated selections, full trade/daily/
quarter/year paths, formulas, parameters and three PNG/SVG charts are in
`nested_sizing/nested_sizing.xlsx`, with compressed sensitivity and
placement arrays beside it. Master18_ adds monthly/FY/group reports. Monte
Carlo17_ preserves all27 daily paths and scenario summaries. Specifications
are `../MONTE_CARLO_SIZING_SPEC.md` and `../NESTED_SIZING_SPEC.md`.

Verification: four focused tests for each study;945,000 Monte Carlo policy-path
replays and25,000 matched-placement policy paths reconcile. All15,560 inner
candidate metric rows, chronological choices and1,768 chosen-size formulas
replay. Strict-prefix source availability is conditional on prior audits.
Legacy fixed entry LUTs, higher-tax training cache and fill assumptions remain;
previously researched years are not an untouched holdout. These experiments
do not establish end-to-end unbiased strategy performance. Original taxes
13/5/2/2/6 remain selection hurdles, not extra PnL deductions. Future work on
entry timing, SL/TP or hedging must replay execution rather than edit saved PnL.

## Previous: annual past-only maximum-profit sizing rerun

Completed the preregistered past-only maximum-profit reruns, stored separately as
ledger15 (WF10) and16 (WF11 dicey ceiling0.85). Original10/11 archives remain
in sample and unchanged. Specification: `../WALKFORWARD_SIZING_SPEC.md`; basic to
detailed explanation: `../WALKFORWARD_EXPLAINED.md`.

2021 has182 warmup entries at size1. Annual expanding fits score617 entries
from2022–2026. Strict pre-entry extraction is valid for746/799 entries;
missing CW instability forces dicey, and other missing values use training
medians. Every fitted scaler, S1/S2/risk model, percentile reference, cutoff,
allocation coefficient and mean-one training offset is frozen before its
scoring year. Actual forward mean is reported; no future renormalization.
Original taxes13/5/2/2/6 and archived09:16 CW cap1 remain for these two reruns.
WF11 dicey<=0.85 is mandatory; WF10 has no dicey cap. The prior-fitted gate
flags232/617 scored entries; their WF11 mean is0.829738,
versus1.032086 for non-dicey entries.

Scored2022+ (same617 original entries, warmup excluded):

| Policy | Net | MDD | Worst day | Net/DD | Mean size |
|---|---:|---:|---:|---:|---:|
| Flat size 1 | 7,593.21 | 144.37 | -61.28 | 52.60 | 1.000000 |
| WF10 maximum profit | 7,547.88 | 152.63 | -70.47 | 49.45 | 0.976479 |
| WF11 dicey0.85 maximum profit | 7,435.66 | 138.66 | -53.87 | 53.63 | 0.956000 |

- WF10 maximum profit: net change -0.60%; MDD change +5.72%; 239 cuts/267 boosts; extra versus ex-post same-mean flat +133.27.
- WF11 dicey0.85 maximum profit: net change -2.07%; MDD change -3.95%; 317 cuts/205 boosts; extra versus ex-post same-mean flat +176.55.

Neither rerun passes the declared forward qualification (higher net, higher net/DD and no worse worst day). Every annual fold found a qualifying training fit; no fallback was used. No live policy is adopted by this research.
WF11 sacrifices2.07% net for3.95% lower DD. Its mean0.956 also reduces activity.
Against an ex-post uniformly scaled flat book at that same mean, WF11 gains
176.55 net but has DD138.66 versus138.01. This is not evidence of a large
profit increase at unchanged exposure; arithmetic mean is an activity proxy.
No settings were retuned after viewing scored performance.
The next requirement for an end-to-end test is a prior-year rebuild of the
base entry LUTs and a causal execution/fill audit, with untouched later data.
These overlay results remain conditional on the legacy broad-history entry
book and fills; previously researched years are not an untouched holdout.

`walkforward_sizing/walkforward_comparison.xlsx` stores all trades,
daily sizes/PnL, quarterly/yearly comparisons, models, scalers, S1 buckets,
S2/risk coefficients, percentile samples, formulas and equity/DD charts.
Master ledger15_/16_ adds monthly/financial-year/group reports. `Algo_WF_*`
sheets replay1,234 scoring sizes using saved annual coefficients and executable
SUMPRODUCT/clipping formulas. Independent frozen-model replay and source
cutoff/fingerprint checks pass; five focused tests pass. The master now has
16 iterations/193 policy paths/211 sheets;
20,651 period rows reconcile, alongside the original spreadsheet
checks and1,234 new spreadsheet-engine sizing calculations.

## Previous: what controls boosts, cuts, restrictions and allocation

`../SIZING_ALGORITHMS.md` documents the exact research algorithms and their
version-specific priorities. The master ledger now has185 sheets, including12
Algo_ sheets for21 families and all189 saved policy paths. Standalone
`research_ledger/Sizing_Algorithms_Calculated.xlsx` contains explanatory tables,
parameter changes, the799-entry conviction gate replay,3,196 annual sizing
decisions and11,985 continuous allocation replays across15 optimized policies.

The later annual method is risk-first: warmup size1, otherwise dicey0.85,
otherwise stronger-tax CW size1.05/1.10, otherwise1. Higher-tax membership uses
maximum-weight closure/min-cut (iterative Dinic), with the original13/5/2/2/6
tax gate preserved. 09:16 may boost after its original13 plus15/17 gate. NW
receives no CW-tax boost, but can be risk cut. There is no future normalization.

The earlier full-fit allocator combines S1 shrunk-bucket and S2 ridge scores
with risk to establish bounds, uses multi-start coordinate pattern search for
allocation coefficients, and projects raw sizes into bounds with an offset
enforcing historical mean1. Risk cuts, stage caps and strong lower bounds
remain hard constraints. An ordinary optimizer boost is a budget allocation,
not evidence of strong edge. The earlier09:16 ban remains in archived results.

Actual spreadsheet formulas reproduce every annual size exactly and optimized
sizes within1e-14; all prior risk, daily and20,223 period checks pass. No new
strategy was fitted/adopted and no live code changed. These are linear/ridge/
logistic, graph and numerical optimization methods, not neural networks.

## Previous: risk-formula documentation and Excel research ledger

Exact definitions are in `../RISK_FORMULAS.md`: regularized logistic loss
probability, ridge prediction of `max(-net_PnL,0)`, three equally weighted
instability percentile ranks, and fixed0.4/0.4/0.2 composite weights. Expected
downside is unconditional negative PnL, not conditional loss size or VaR.
The full-history risk threshold is0.6677157174036119, flagging201/799 entries;
the annual version uses saved prior-year coefficients and reference samples.

`research_ledger/GammaShort_Research_Ledger.xlsx` consolidates14 iterations and
189 saved daily policy paths. Each iteration has its own policies, inputs,
trades, daily equity/DD, summary, quarterly/yearly/monthly/FY and group results,
plus original source summary tables. Static risk replays reproduce all799
original scores and617 annual scores. Coefficients, transforms, annual rank
references, executable Excel formulas and comparison charts are included.
Older selected-only and rounded artifacts remain explicitly labelled, alongside
all their original saved candidate summaries. Date ranges differ across studies.

`research_ledger/Risk_Formulas_Calculated.xlsx` is the standalone spreadsheet-
engine-recalculated formula workbook; the master also includes formulas and
static replay values. Exact order keys and17-digit raw text preserve legacy
floating-point distinctions below Excel precision; regenerate with the builder
after changing raw inputs. `research_ledger/README.md` explains navigation,
precision, DD resets, original-versus-counterfactual metadata and reproduction.

The registry `../research_iterations.json` and TODO require every future
iteration to retain its source and rebuild/verify the ledger. No new strategy
fit, live behavior, original workbook or shared-input change is introduced.

Verification passes for173 sheets,189 policy paths and20,223 monthly/quarterly/
yearly/FY rows. The actual spreadsheet engine reproduces799 risk scores within
6.7e-16, downside within5.2e-14, and every dicey flag exactly. Every daily
equity/DD series, trade total/count and period net/reset DD is independently
checked. `research_ledger/verification.json` and hashes preserve the evidence.

## Previous: tick-based entry filters and one-minute delay

Eight fixed hypotheses in `../TICK_ENTRY_SPEC.md` were evaluated on the frozen
GammaShort book. New features use only ticks strictly earlier than entry, a
pre-decision ATM strike, backward as-of quotes (max age3s), no backfill, and
prior-year-only across-day thresholds. Original LUT taxes (13,5,2,2,6), event
weights, size multiplier1, saved exits/costs and NW entries are retained.
2021 is warmup. Of720 CW dates,716 have usable features; scored2022+ has617
base entries, with547/550 CW dates feature-covered and67 NW unchanged.

| Scored 2022+ rule | Net | MDD | Net/MDD | Skips | Delays |
|---|---:|---:|---:|---:|---:|
| Original entries |7593.21|144.37|52.60|0|0|
| Avoid premium percentile below20% |5992.09|173.71|34.50|105|0|
| Avoid premium percentile below10% |6560.90|153.02|42.88|66|0|
| Delay cheap-looking early entries1min |7362.48|139.86|52.64|4|128|

None of the eight improves both net and net/DD with worst day no worse. The
delay reduces net3.04% and DD3.12%, while worst day improves from-61.28 to-55.77.
Its random-trigger net tail fraction is .047, rising to .376 after an eight-rule
correction. Percentile rejection and the tested residual/spread/stress filters
do not establish a new profit edge. Keep the existing entry rule.

The butterfly diagnostic finds two positive call and two positive put quote
credits in scored observations. These are apparent quotes, not executable
arbitrage: whole lots, fees, quote simultaneity and all-leg fills are unresolved.
The residual model is a local price indicator; mean reversion itself has not
been established. Past-only ATM differs from saved fill strike on17 valid days.

`tick_entry_study/REPORT.md` and `tick_entry_comparison.xlsx` contain all eight
results, full and scored periods, curves, all799 trades, matched coverage,
year/stage attribution, training thresholds and 1,000 matched random-trigger
allocations per rule. Eleven tests and the independent artifact audit pass.
`audit_result.json` and `manifest.json` retain validation and hashes. No live
or shared-input changes. **Conditional entry-overlay study only:** legacy LUTs
used broader history and saved fill assumptions remain uncertified, so this
does not establish leakage-free end-to-end performance or live statistical arb.

## Previous: stronger-tax conviction with 09:16 boosts allowed

The owner superseded the previous blanket 09:16 no-boost restriction. Original entry taxes remain (13,5,2,2,6). The new conviction layer fits tax increments +2/+4, so a base-approved 09:16 entry must additionally survive tax 15/17 before receiving size 1.05/1.10. Risk flags override the boost and enforce 0.85. The higher-tax model is an additional gate; the original tax is neither removed nor charged against reported PnL.

`higher_tax_sizing.py` fits every year's additional LUTs and risk models using completed prior years only; full-history S1/S2 predictors and future normalization are excluded. The 2021 warmup has 182 size-1 entries; the forward overlay has 617 entries from 2022 onward. Base entries still come from the legacy wider-history LUTs and existing cached features/execution, so this is a **conditional sizing-overlay check, not an end-to-end leakage-free strategy result**.

| Scored period 2022+ | Net | MDD | Actual mean size | Boosted 09:16 entries |
|---|---:|---:|---:|---:|
| Flat | 7,593.21 | 144.37 | 1.0000 | 0 |
| Higher tax +2, boost 1.05 | 7,409.32 | 144.15 | 0.9743 | 66 |
| Higher tax +2, boost 1.10 | 7,519.15 | 149.90 | 0.9912 | 66 |
| Higher tax +4, boost 1.05 | 7,395.19 | 144.15 | 0.9718 | 61 |
| Higher tax +4, boost 1.10 | 7,490.89 | 149.90 | 0.9861 | 61 |

None improves flat on both profit and profit/DD. This supersedes optimistic interpretations of the earlier full-history fits; it does not prove high-tax conviction is a profitable live sizing signal. Actual means are reported without future balancing. The workbook includes same-average constant-size diagnostics, explicitly ex post.

See `higher_tax_sizing/higher_tax_comparison.xlsx`, `trade_comparison.csv`, `training_cutoffs.csv`, fold-specific risk JSON and higher-tax LUT NPZ files, summary and manifest. The min-cut solver in `../lut_tax_closure.py` is verified against brute-force tiny closures and preserves minimum weighted anchor evidence 1. Seven new tests and independent artifact checks pass. Shared files and live services remain unchanged.

## Previous: trade comparison and modest conviction boosts

The same 799 entries now have a complete flat/previous-DD/new-policy trade comparison. The predeclared strong-edge gate is CW S1 upper tercile + S2 >=CW median + low estimated loss probability + no dicey flag. Of 166 strong estimates,134 are at 09:16 and remain blocked;32 qualify for a modest boost. The feasible profile enforces strong 1.10–1.15, ordinary <= 1.10, dicey 0.75–0.85, 09:16 <= 1, and exact mean 1. Its high-conviction size averages 1.1406. The gentler profile cannot fund mandatory cuts while restoring mean 1 and is explicitly infeasible (maximum budget 792.35 vs 799 required).

| Policy | Net | MDD | Net/MDD | Worst day |
|---|---:|---:|---:|---:|
| Flat |10,522.04|144.37|72.88|-61.28|
| Previous DD-first |10,749.08|105.41|101.97|-56.16|
| New conviction policy |10,938.10|108.63|100.69|-56.25|

Tradeoff: new policy adds 189.02 PnL versus previous DD-first but DD is 3.22 higher. It cuts 251 entries, leaves 142 unchanged, boosts 406. Thirty-two boosts are strong-conviction allocations;374 are ordinary budget redistribution, capped 1.10. All 201 dicey entries remain below full size, averaging 0.8250. No score label guarantees profit: the first chronological boosted example (2021-01-05) loses 10.68 flat vs 12.29 sized; the next (2021-02-03) gains 54.11 flat vs 62.23 sized.

`conviction_sizing/trade_comparison.csv` contains all 799 dates, leg/stage, available strike/expiry, entry/exit, flags/reasons and flat/previous/new quantities/PnL. `conviction_sizing/conviction_trade_comparison.xlsx` adds action/group/year/leg attribution, model coefficients, feasibility checks and charts. Specification: `../CONVICTION_SIZING_SPEC.md`. Twelve tests plus independent artifact checks pass. Full-history scores, thresholds, optimization and normalization are in sample; no causal live edge or deployment is established.

## Previous: drawdown-first optimization with mandatory dicey-day cuts

`../DRAWDOWN_SIZING_SPEC.md` was written before this experiment. `../sizing_drawdown_risk.py` fits estimated loss risk/downside from entry inputs, excludes fullfit S1/S2 from those risk models, and combines them with pre-entry instability to flag risky entries. Missing CW entry-window readings also flag a day. Of 799 entries, 201 are flagged. Normalization cannot undo their hard size ceiling.

| Policy | Net | MDD | Worst day | Dicey mean size | Whole-book mean |
|---|---:|---:|---:|---:|---:|
| Flat | 10,522.04 | 144.37 | -61.28 | 1.0000 | 1.000000 |
| DD-first, dicey cap 0.85 | 10,749.08 | 105.41 | -56.16 | 0.8057 | 1.000000 |
| Profit-focused, dicey cap 0.85 | 11,328.04 | 124.86 | -56.25 | 0.7883 | 1.000000 |

DD-first adds 2.16% net and lowers DD 26.98%; the profit alternative adds 7.66% net and lowers DD 13.51%. Global sizes remain 0.75–1.15, with no boost at 09:16. Fixed comparisons were dicey caps 0.90/0.85/0.75 and three search objectives each. All nine new results meet baseline profit, DD and worst-day limits. The prior unrestricted optima are preserved as benchmarks and are not compliant with the new mandatory dicey-day ceilings.

These are full-history in-sample estimates; fitting, quantiles, optimization and normalization use the whole dataset. They do not show that future dicey days can be identified reliably. Stress features use flat-book equity through the previous day, not a live sized-equity halt. Existing saved PnL/costs scale linearly; no quantity-dependent fill replay. LUT tax vector (13,5,2,2,6) is retained as table selection, not deducted from PnL.

Artifacts: `drawdown_risk_sizing/drawdown_risk_sizing.xlsx`, candidate CSV, daily risk scores/flags/reasons, saved coefficients and hash manifest. The workbook includes daily positions/PnL, equity/DD charts, risk-group/year/leg attribution, feature transforms and search trace. Nine focused tests pass; saved risk and sizing models, mandatory cuts, mean-one budgets, DD and attribution replay checks passed.

## Previous: average-size-one optimization

The owner explicitly replaced the previous no-rescaling instruction with exact average size 1 across all traded days, balancing size increases and decreases. `../sizing_normalized_optimization.py` evaluates 69 policies over 1,430 days / 799 entries with size floor 0.75, cap 1.15, no skipping, and no boost to 09:16 CW entries. All policies satisfy the same total multiplier budget, 799. Full-history fitting and normalization are deliberately in sample; there is no 60/20/20 split.

| Observed policy | Net | MDD | Net/MDD | Mean size |
|---|---:|---:|---:|---:|
| Flat | 10,522.04 | 144.37 | 72.88 | 1.000000 |
| Best observed profit (also satisfies flat DD cap) | 11,495.56 | 128.76 | 89.28 | 1.000000 |
| Best observed ratio | 11,243.25 | 113.89 | 98.72 | 1.000000 |

The profit-focused result adds 9.25% net with 10.81% lower DD. The ratio-focused result adds 6.85% net with 21.11% lower DD. These are historical improvements, not unseen-data alpha or global-optimum guarantees. The optimizer searches bounded coefficients for a 34-feature sizing equation, uses exact bounded mean-one projection, and saves the best feasible policy seen for each objective across all searches. Fifteen candidates beat flat on both full-history net and net/MDD.

Workbook: `normalized_sizing/normalized_sizing.xlsx`; includes daily sizes/PnL, yearly and CW/NW attribution, coefficients/transforms, trace, and equity/DD charts. There are also ranking/coefficients CSVs, a summary, and input hashes. Existing saved net PnL/costs scale linearly; there is no quantity-dependent tick replay. Mean multiplier 1 is an arithmetic exposure budget; event-weighted notional exposure is reported separately. Neither the fitted parameters nor the full-period normalization are a causal live policy.

## Previous: complete-history sizing without normalization

The owner deferred neural-network directional research and requested sizing calculations for the complete GammaShort history. `../sizing_full_history.py` refits and evaluates 66 constrained variations on all 1,430 days through 2026-10-14, including CW and NW production PnL. It retains max size 1.15, no rescaling to mean 1, and no boosting 09:16 CW entries. This is deliberately in sample; the archived split-based conclusions below are separate analyses.

| Full-history result | Net | MDD | Net/MDD |
|---|---:|---:|---:|
| Flat | 10,522.04 | 144.37 | 72.88 |
| Best ratio: Table + IV ratio | 9,937.07 | 124.74 | 79.66 |
| Highest profit: Calendar Event | 10,587.37 | 145.50 | 72.77 |

No variation beats flat on both net and net/MDD. Units are normalized Rs. The analysis scales existing production PnL and its costs linearly; it does not replay fills at different quantities. Three CW entries lack entry-window features; those missing inputs receive neutral adjustments. Full rankings, annual results, CW/NW totals, daily sizes/PnL, fitting effects and feature coverage are stored in `full_history_sizing/full_history_sizing.xlsx`. `full_history_sizing/manifest.json` records input hashes.

## Current decision

The previous live operating conclusion remains flat at size 1. Current research permits modest 09:16 boosts after both the unchanged base tax-13 gate and a stronger-tax gate, with risk cuts taking priority. Latest overlay models fit only prior years and report actual mean exposure. Earlier exact-mean normalization and no-09:16-boost studies remain historical records. No live sizing change is implemented.

## What the studies found

Completed studies tested table/input sizing, pre-entry stability, drawdown overlays, early-exit rules, volatility risk premium, volatility targeting, stop distance, calendar/regime flags, parameter scores, capped sizes, and borderline/near-YES cuts. None establishes a reliable candidate that passes the owner's validation rule. The v6 borderline study selected flat; near-threshold trades were not consistently worse than firm trades, and isolated cells were too rare for useful inference.

The detailed candidate definitions, sample periods, numerical results, and caveats are in `../SIZING_RESEARCH_HANDOFF.md`. Backtest PnL uses normalized Rs (index points × 25,000 / entry underlying), not actual account rupees.

## Relaxed 09:20+ entry study: complete; keep current book

`GammaShort_Backtest_Final.py` has a research-only `GS_RELAX=1` path for four preregistered neighbor-support rules (R1–R4), evaluated at size 1 and 0.75. The run contains 1,430 dates through 2026-10-14, while the frozen production reference contains 1,428 dates through 2026-10-12. The evaluator excludes the two relaxed-only dates before splitting, verifies exact production-PnL equality on all reference dates, and then applies the fixed validation gate.

**Decision: current book (flat).** Validation current book: net 2,106.41, MDD 126.39, net/MDD 16.67. Best relaxed candidate was R2 at 0.75 size: net 2,093.83, MDD 137.88, net/MDD 15.19. It fails both gates; all other R1–R4 sizes also fail. TEST was reported once for the selected current book: net 1,311.65, MDD 120.99, net/MDD 10.84 (96 trades). No candidate was selected, so no shuffle check was due. Detailed rows are in `output/entry_relax_report.xlsx`.

## Derived strategies: scaffold only

The `GS_DERIVED` / `simulate_legs` addition in `GammaShort_Backtest_Final.py` is incomplete. It provides a generic multi-leg simulation helper but does not wire long-gamma, iron-fly, late-day short, or other candidates into the per-day run. It has not been run or validated. Treat all derived strategy ideas as untested; define and preregister the exact strategy and decision rule before implementing/evaluating one.

## Continue here

1. If beginning derived strategies, first preregister the concrete strategy, legs, entry time, hedge and exit rules, costs, baseline, and split. Keep it separate from the short-straddle sizing conclusion.
2. `GS_DERIVED` / `simulate_legs` remains an unimplemented scaffold, not a result. Keep all work in `research/`; do not edit `services/` or `ui/` from research conclusions.

See `../TODO.md` for commands and method constraints. Existing source modifications and generated reports may be uncommitted; inspect `git status` before starting new edits.
