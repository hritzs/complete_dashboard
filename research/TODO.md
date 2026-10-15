# GammaShort research TODO

Research is isolated under `research/`. Do not translate a research result into `services/` or `ui/` without the owner's explicit direction.

## Latest request: prioritize DD and mandatory cuts on dicey days

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

- Research now redistributes size above and below 1 with exact mean 1, floor 0.75 and cap 1.15. Previous live operating conclusion remains flat pending a separate implementation decision.
- The latest research explicitly normalizes average trade size to 1; never boost the 09:16 table. Archived split-based studies and their earlier no-rescaling restriction remain historical records. The current analysis has no split and reports full-history metrics.
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
