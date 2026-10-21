# GammaShort research TODO

Research is isolated under `research/`. Do not translate a research result into `services/` or `ui/` without the owner's explicit direction.

## 2026-10-21: latest GS and complementary long-option screen

Both studies are complete and independently verified on the same1,430 dates.
Latest matched GS is10,109.38 / maximum DD168.42 versus freshly replayed old
rules10,594.58 /144.37, on the same794 entries. Full details and actual input
vintages are in handoff4.22 and `output/logic_comparison_20261021/REPORT.md`.

The owner authorized a separate-risk sleeve alongside GS. Six fixed
long-option alternatives used100 normalized premium units at entry, with
actual ask/bid execution models and declared fee drag. Plain directional
call/put net is+5.27 across8 trades over all history, but **-43.99 across7
trades from2022 onward**. Lower-IV directional net+40.55 comes from only2
trades including the2021 winner;2022+ net is-8.71 across1 trade. All six later
window add-ons lose money. None lowers GS DD168.42; none is adopted.

See handoff4.23, `COMPLEMENT_STRATEGY_SPEC.md`,
`output/complement_strategy/complement_strategy.xlsx` and its REPORT.md. Every
individual/combined daily path, entry/exit, cost, Greek, formula and calendar
period is retained.30 policy entries represent only8 distinct dates, not30
independent observations. The sampled data has1-second timestamps with1900
date placeholders, not subsecond order-event data.11 ineligible data/window
dates were explicitly audited; raw exceptions remain preserved. No live code,
.env or shared inputs changed. Neural models and a new GS entry state machine
remain separate unimplemented hypotheses.

## Latest: Monte Carlo and chronological parameter/placement studies

The owner rejected future mean balancing and constant-multiplier profit as
allocation alpha. Iteration17 stores the six-scenario Monte Carlo study;
iteration18 adds a chronological1,944-combination parameter sweep and an
identical-exposure placement test. No live code, entries or tax vector changed.

Iteration17:27 policies;5,000 paired paths per scenario;150 trade opportunities
per path. Selection uses2022–2024, with2025+ as the evaluation pool. Fixed1.15
is only an exposure control, not an alpha result. At the flat1 training-tail
DD budget, the selected policy is flat. The training-selected adaptive policy
also failed in the later pool. See `output/monte_carlo_sizing/REPORT.md` and
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
`output/nested_sizing/nested_sizing.xlsx`, with compressed sensitivity and
placement arrays beside it. Master18_ adds monthly/FY/group reports. Monte
Carlo17_ preserves all27 daily paths and scenario summaries. Specifications
are `MONTE_CARLO_SIZING_SPEC.md` and `NESTED_SIZING_SPEC.md`.

Verification: four focused tests for each study;945,000 Monte Carlo policy-path
replays and25,000 matched-placement policy paths reconcile. All15,560 inner
candidate metric rows, chronological choices and1,768 chosen-size formulas
replay. Strict-prefix source availability is conditional on prior audits.
Legacy fixed entry LUTs, higher-tax training cache and fill assumptions remain;
previously researched years are not an untouched holdout. These experiments
do not establish end-to-end unbiased strategy performance. Original taxes
13/5/2/2/6 remain selection hurdles, not extra PnL deductions. Future work on
entry timing, SL/TP or hedging must replay execution rather than edit saved PnL.

## Previous: past-only rerun of maximum-profit iterations10/11

Completed the preregistered past-only maximum-profit reruns, stored separately as
ledger15 (WF10) and16 (WF11 dicey ceiling0.85). Original10/11 archives remain
in sample and unchanged. Specification: `WALKFORWARD_SIZING_SPEC.md`; basic to
detailed explanation: `WALKFORWARD_EXPLAINED.md`.

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

`output/walkforward_sizing/walkforward_comparison.xlsx` stores all trades,
daily sizes/PnL, quarterly/yearly comparisons, models, scalers, S1 buckets,
S2/risk coefficients, percentile samples, formulas and equity/DD charts.
Master ledger15_/16_ adds monthly/financial-year/group reports. `Algo_WF_*`
sheets replay1,234 scoring sizes using saved annual coefficients and executable
SUMPRODUCT/clipping formulas. Independent frozen-model replay and source
cutoff/fingerprint checks pass; five focused tests pass. The master now has
16 iterations/193 policy paths/211 sheets;
20,651 period rows reconcile, alongside the original spreadsheet
checks and1,234 new spreadsheet-engine sizing calculations.

## Previous request: explain boost/cut algorithms and save their Excel formulas

`SIZING_ALGORITHMS.md` and `sizing_algorithm_report.py` document21 algorithm
families, inputs/outputs, formulas, priority and version changes. The master
ledger now has185 sheets, including12 Algo_ sheets and a mapping for all189
saved policies. Standalone `Sizing_Algorithms_Explained.xlsx` and
`Sizing_Algorithms_Calculated.xlsx` contain the same algorithm explanation and
executable sizing formulas; the latter is spreadsheet-engine recalculated.

The later annual rule is warmup1 -> dicey0.85 -> non-dicey stronger-tax CW
boost1.05/1.10 -> ordinary1. Risk overrides conviction. CW09:16 can boost only
after its original tax13 and the additional15/17 hurdle. NW has no CW-tax
boost but may receive risk cuts. No future mean-one balancing occurs.
Older full-history optimization and its09:16 restriction remain archived:
S1/S2 determine priority bounds; coordinate search fits allocation coefficients;
bounded projection enforces historical mean1 and caps. Ordinary budget boosts
are not high-conviction labels. These are not neural-network sizing models.

Verification passes for799 historical conviction gates,3,196 annual sizes and
11,985 optimized trade sizes across15 policies. Actual Excel annual sizes match
exactly; optimized sizes match within1e-14. The prior189 daily paths and20,223
period rows also reconcile. Reporting only: no model refits or live changes.

## Previous request: exact risk formulas and a persistent Excel iteration ledger

`RISK_FORMULAS.md` documents logistic loss probability (L2 lambda20), ridge
expected negative PnL (lambda50, winners contribute zero), exact percentile/tie
conventions, the three instability ranges/spreads, fixed40/40/20 weights, the
75th-percentile dicey threshold and mandatory sizing caps. These weights are
research choices, not learned probabilities. Full-history and annual versions
have different fitting/reference protocols and remain labelled accordingly.

`build_research_ledger.py` reads `research_iterations.json` without refitting
strategies or changing saved source workbooks. `output/research_ledger/` holds
the master Excel ledger for14 iterations/189 daily policy paths, separate
trade/daily/quarter/year/month/FY/group/policy/input/source-summary sheets per
iteration, risk coefficients/transforms/references, exact trade risk replays,
Excel formulas, and equity/DD charts. Older selected-only or rounded exports
are explicitly marked; unsaved daily candidate paths are not fabricated.

Verification passes across173 sheets,189 daily paths and20,223 period rows.
LibreOffice recalculation reproduces all799 original Excel risk scores within
6.7e-16, with identical dicey flags. The Python annual replay also reproduces
all617 scored rows. Saved audit, verification and SHA256 manifests accompany
the master workbook and the smaller calculated-formula workbook.

**Standing reporting convention for every future iteration:** preserve the
source artifact, register a new iteration ID/protocol/coverage, rebuild this
ledger and run `verify_research_ledger.py`. Include trade-wise PnL/sizing and
calendar quarterly/yearly results, with costs, sample period, fitting protocol,
precision and causal limitations. Do not describe an iteration as fully
reported until these sheets exist. See the ledger README for commands.

## Previous request: tick-based percentile / relative-value entry research

Completed the eight fixed entry hypotheses in `TICK_ENTRY_SPEC.md` using fresh
strictly-before-entry tick features. `tick_entry_features.py` extracted all720 CW
dates (716 usable); the 2022+ score covers617 base entries, including550 CW
(547 usable) and67 unchanged NW. No live changes. Keep original LUT taxes
(13,5,2,2,6), saved exits/costs and event weights; use size multiplier1 to isolate
entry effects. 2021 is warmup; annual spread/stress cutoffs use prior years only.

None improves both net and net/DD. Flat2022+ net/MDD is7593.21/144.37;
P20 is5992.09/173.71; P10 is6560.90/153.02. Delaying cheap-looking early
entries one minute gives7362.48/139.86 (128 delays,4 skips): lower DD but
3.04% less net. The delay's net placebo tail fraction .047 becomes .376 after
eight-rule correction; this does not establish robust new profit alpha.
Two call and two put apparent butterfly credits occur in scored observations;
they do not establish executable arbitrage. No candidate is adopted.

Artifacts: `output/tick_entry_study/REPORT.md`, comparison workbook with curves,
all799-trade CSV, coverage/year/stage attribution, thresholds, 1,000 randomized
trigger allocations per rule, apparent butterfly candidates, source hashes and
`audit_result.json`. Eleven tests pass, plus independent raw-PnL, DD, gate,
cutoff, NW/warmup invariance and hash checks. Reproduction: run feature extraction,
`tick_entry_eval.py`, then `tick_entry_audit.py` with `.venv/bin/python`.

Important limit: fresh overlay features and annual cutoffs are past-only, but
legacy base LUTs used broader history and saved fills are not certified causal.
This is conditional entry-overlay research, not a clean end-to-end strategy or
a continuously scanned statistical-arbitrage backtest. Missing features retain
the original trade; no future normalization, later re-scan or NW fallback occurs.

## Previous request: higher-tax conviction, including 09:16 boosts

The owner explicitly permits a modest 09:16 boost when conviction is strong, superseding the prior blanket no-boost rule. Keep original entry LUT taxes **(13,5,2,2,6)**. `higher_tax_sizing.py` adds separately fitted conviction masks at +2 and +4: 09:16 must first pass the base tax 13 and then tax 15 or 17 to get a 5% or 10% boost. Dicey sizing at 0.85 always overrides conviction. The other CW stages use their own unchanged base tax plus the same additional hurdle. NW receives no CW-tax boost.

This experiment uses annually fitted **past-only sizing overlays**: risk models/scalers/rank references and higher-tax min-cut masks are fitted through the prior year. The 182 entries in 2021 are warmup at size 1; 617 entries from 2022 onward are scored. There is no future whole-period normalization: actual mean is reported. This remains conditional on the legacy fixed entry list, whose base LUTs used broader history; it is not a leakage-free end-to-end strategy replay.

Results do not support adopting any of the four fixed candidates over flat. On scored 2022+ entries, flat net/DD is 7,593.21/144.37. Tax +2 with boost 1.10 gives 7,519.15/149.90, mean 0.9912; tax +4 with boost 1.10 gives 7,490.89/149.90, mean 0.9861. The 1.05 versions reduce net further with only marginally lower DD. There are 66 boosted 09:16 entries under +2, and 61 under +4. Higher tax alone is not proof of a future edge.

Artifacts: `output/higher_tax_sizing/` includes all-trade comparisons, workbook, training cutoffs, five prior-only risk-model artifacts, ten higher-tax LUT artifacts, summary and input hashes. `HIGH_TAX_CONVICTION_SPEC.md` fixed the experiment before evaluation. Seven new tests cover exact min-cut versus brute force, anchor evidence, risk override, prior-only fitting and empirical ranks; independent artifact checks verify all dates, masks, models, PnL, DD, means and hashes. No live service changes.

## Previous request: trade comparison and modest strong-edge boosts

The owner requested a trade-by-trade comparison and modest boosts when estimated edge is strong. `CONVICTION_SIZING_SPEC.md` fixed the gate/profiles before evaluating the new policy. Reuse the frozen DD-risk inputs/flags: CW S1 upper tercile + S2 at least CW median, estimated loss probability <= book median, and no dicey flag. There are 166 strong-estimate entries, of which 134 are blocked by the 09:16 no-boost rule and 32 can be boosted. NW has no equivalent two-score conviction label.

The gentle profile (strong 1.05–1.10, ordinary <= 1.05) cannot meet mean 1 under the 201 mandatory dicey cuts: total upper budget 792.35 <799 required. It was reported infeasible without weakening rules. The modest-priority profile is feasible: eligible strong 1.10–1.15, ordinary <= 1.10, dicey 0.75–0.85, 09:16 <= 1, average 1. Strong entries never size below ordinary entries. Other boosts are budget redistribution, not evidence of strong conviction.

Selected lowest-DD qualifying policy: net 10,938.10 / DD 108.63 / ratio 100.69 / worst day -56.25, versus previous DD-first 10,749.08 /105.41 /101.97 /-56.16 and flat 10,522.04 /144.37 /72.88 /-61.28. It adds 189.02 net versus previous DD-first at 3.22 greater DD. All 799 entries remain:251 cut,142 unchanged,406 boosted; only 32 boosts carry the high-conviction label, while 374 are ordinary budget reallocations. Average high-conviction size 1.1406; average dicey size 0.8250; whole mean 1.

Artifacts: `output/conviction_sizing/conviction_trade_comparison.xlsx`, all 799 rows in `trade_comparison.csv`, chronological examples, summary, coefficients and input hashes. Twelve tests pass across conviction/risk/normalization; independent checks reproduce gates, budgets, daily sizes/PnL, DD, counts and attribution. Full-history fitting/normalization remains in sample and does not validate future edge. Live behavior is unchanged.

## Previous request: prioritize DD and mandatory cuts on dicey days

The owner wants reduced exposure on uncertain/risky days, not just better aggregate profit. `DRAWDOWN_SIZING_SPEC.md` fixed this experiment before evaluation. `sizing_drawdown_risk.py` defines dicey entries using fitted loss probability, expected downside, pre-entry instability, and missing CW features; it then enforces ceilings 0.90/0.85/0.75 before bounded mean-one normalization. The risk model excludes the outcome-fitted S1/S2 scores. All entries stay within 0.75–1.15; 09:16 CW is never boosted. LUT tax T=(13,5,2,2,6) and selected trade set remain unchanged.

201 of 799 historical entries are flagged. Lowest observed qualifying DD policy: dicey ceiling 0.85, net 10,749.08, MDD 105.41, ratio 101.97, worst day -56.16. Average size on dicey days is 0.8057, on other days 1.0653, and on all entries exactly 1. Net +2.16%, DD -26.98% versus flat. A profit-focused ceiling-0.85 alternative gives net 11,328.04 / MDD 124.86 / worst day -56.25. All nine new candidate policies satisfy net >= flat, DD <= flat and worst day >= flat worst in this in-sample run.

Outputs: `output/drawdown_risk_sizing/` workbook, risk flags/reasons, coefficients, summary and hashes. Nine tests pass across the risk and normalization modules; saved flags/models/sizes, mean/caps, PnL/DD and attribution were independently verified. Full-history model fitting and normalization use the full dataset and do not establish causal live risk prediction. The current research focus is DD-first sizing with mandatory risk cuts; neural directional research stays deferred.

## Previous request: normalize average size to 1 and optimize

The owner now explicitly requires increases and decreases to balance so average size across recorded trade days remains exactly 1. This supersedes the earlier no-rescaling instruction for this research. `sizing_normalized_optimization.py` compares 65 normalized historical templates, flat, and three optimized policies over all 1,430 days / 799 entries. Every candidate has mean size 1, floor 0.75, max 1.15, and no 09:16 CW boost; no trades are skipped.

Normalization projects onto the size bounds and an exact total-size budget of 799. Optimization uses a seeded, multi-start coordinate search on 34 entry-input features, with profit, DD-capped profit, and profit-floored net/MDD objectives. Best observed profit is 11,495.56, MDD 128.76, ratio 89.28. Best observed ratio is 98.72 with net 11,243.25 and MDD 113.89. Flat is 10,522.04 / 144.37 / 72.88. These are full-history in-sample numerical optima, not proven live alpha. Exact full-history normalization also uses the full score distribution.

Outputs: `output/normalized_sizing/normalized_sizing.xlsx` (including daily sizes/PnL, coefficients, optimization trace, equity/DD charts), ranking and coefficient CSVs, summary, input hashes. Arithmetic mean size 1 does not guarantee identical cash/risk exposure because original event-day sizing is embedded in PnL; event-weighted exposure is reported separately. Five focused unit tests cover normalization and hard optimizer constraints.

## Previous request: full-history sizing without normalization

The owner requested a full-history sizing calculation without a 60/20/20 split, deferring the neural-network directional-strategy idea. `sizing_full_history.py` fits and evaluates the existing sizing families on all 1,430 saved days (2021-01-01 through 2026-10-14), with base 1, max 1.15, no average-size rescaling, and no boosting 09:16 CW entries. Results are in `output/full_history_sizing/`: workbook, ranking CSV, summary, and input hash manifest.

The 66-candidate comparison is explicitly in sample. Flat net/MDD is 10,522.04 / 144.37; best net/MDD candidate is Table + IV ratio at 9,937.07 / 124.74. Highest net is Calendar Event at 10,587.37 / 145.50, with a slightly lower net/MDD ratio than flat. No candidate beats flat on both full-history net and net/MDD. Historical split-based studies below remain archived; their split rule does not apply to this new analysis.

## Current operating conclusion

- Base benchmark is size1. Research limits remain0.75–1.15. The past-only reruns15/16 report actual mean; WF11 enforces dicey<=0.85. Both failed the forward qualification, so the live operating conclusion remains flat.
- The owner allows09:16 boosts after original tax13 and a stronger-tax conviction gate in the separate higher-tax experiment13. The specific10/11 reruns15/16 retain their archived09:16 CW cap1 and add no stronger-tax gate. Exact future mean1 cannot be forced without lookahead; a mean-one offset is fitted on prior training trades only and frozen for scoring.
- Current decision is flat. No completed sizing study has established a reliable out-of-sample improvement.

## Completed studies

Table and parameter sizing, pre-entry stability, drawdown overlays, early exits, VRP, volatility targeting, stop distance, calendar/regime flags, score sizing, capped sizing, borderline-cell cuts, and the preregistered 09:20+ relaxation experiment have been evaluated. None passed the validation gate. See `SIZING_RESEARCH_HANDOFF.md` and `output/RESEARCH_SUMMARY.md` for results and caveats.

## Immediate continuation

1. The relaxed-entry study is complete. Its 1,430-day run includes two dates beyond the frozen 1,428-day reference; the evaluator aligns to reference dates, verifies exact production-PnL equality, and excludes those two unmatched dates before splitting. Current decision: flat; no R1–R4 size beat flat on both validation net and net/MDD.
2. If continuing to a derived-strategy experiment, first select and preregister the concrete strategy and its entry time, legs/strikes, hedge cadence/threshold, exits, costs, comparison baseline, and split. The `GS_DERIVED` / `simulate_legs` code in `GammaShort_Backtest_Final.py` is an unfinished generic scaffold: no strategy candidates are wired into `process_day`, and it has not been run or validated. Do not treat it as a result.
3. Keep any new experiment separate from sizing research, write only under `research/output/`, and do not edit `services/` or `ui/` without explicit direction.

## Relaxed-entry run command

From `research/` (the run folder/config already exists):

```bash
GS_RELAX=1 GS_OUTPUT_DIR="$PWD/output/backtest_relax" \
  GS_TICK_DIR=/mnt/shared/TickDataIV \
  GS_LUT_DIR=/mnt/shared/GammaShortData/GammaShort/LUTS_r46V2_cwnw_adj007_005_T13_5_2_2_6 \
  GS_EVENT_PATH=/mnt/shared/EventDates.xlsx \
  GS_DAILY_CSV=/mnt/shared/Hardik/Project_Codes/GammaShortDailyProcess/GammaShortDailydata.csv \
  GS_WORKERS=20 ./.venv/bin/python GammaShort_Backtest_Final.py
```

Then run:

```bash
./.venv/bin/python entry_relax_eval.py
```

The evaluator writes `output/entry_relax_report.xlsx`. Existing `.pkl` day records are resumable. Keep research outputs in `research/output/`; the share is read-only.

## Fixed relaxed-entry candidates

All rules accept a 09:20+ NO cell when its ±1 neighbors on IV ratio, straddle ratio, and Build IV provide the required number of YES cells. The existing veto, Build-IV floor, and expiry cutoff still apply. A relaxed trade replaces that day's later production CW entry or fills a no-trade day. Each candidate is evaluated at size 1 and 0.75.

- R1: from 10:30, at least 1 YES neighbor.
- R2: from 11:00, at least 1.
- R3: from 12:00, at least 1.
- R4: 10:30–11:59 requires at least 2; from 12:00 requires at least 1.

Decision: choose the candidate with best validation net / MDD only if both validation net and net / MDD beat the current book; otherwise stay with the current book. A validation winner receives the predeclared TEST shuffle check. Do not adjust rules after inspecting performance.

## Method constraints for archived split-based studies

- Chronological 60/20/20 split by day: first 60% TRAIN, next 20% VALIDATION, last 20% TEST.
- Fit all bin edges, scores, and scaling on TRAIN only. Inputs must be available by the decision time.
- Lock a short candidate list and decision rule before evaluation. TEST is reported once and never used to choose or tune.
- A candidate is not accepted merely because it raises TRAIN or TEST profit; the validation gate above controls the decision.
- Backtest PnL is normalized Rs: `index points × 25,000 / entry underlying`. It represents one Rs 25,000 notional unit.

## Research files

- `GammaShort_Backtest_Final.py`: tick-level CW/NW backtest; `GS_RELAX=1` adds the relaxed-entry diagnostics without changing production results. `GS_DERIVED=1` currently declares an unfinished generic multi-leg scaffold only.
- `entry_relax_eval.py`: fixed R1–R4 evaluator.
- `sizing_equation.py`, `sizing_equation_v2.py`, `sizing_vrp.py`, `sizing_v3.py`–`sizing_v6_borderline.py`: completed sizing studies.
- `SIZING_RESEARCH_HANDOFF.md`: detailed study history, data definitions, caveats, and decisions.
- `output/RESEARCH_SUMMARY.md`: concise results and current research state.
