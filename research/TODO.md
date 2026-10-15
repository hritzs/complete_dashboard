# GammaShort research TODO

Research is isolated under `research/`. Do not translate a research result into `services/` or `ui/` without the owner's explicit direction.

## Latest request: complete-history GammaShort sizing

The owner requested a full-history sizing calculation without a 60/20/20 split, deferring the neural-network directional-strategy idea. `sizing_full_history.py` fits and evaluates the existing sizing families on all 1,430 saved days (2021-01-01 through 2026-10-14), with base 1, max 1.15, no average-size rescaling, and no boosting 09:16 CW entries. Results are in `output/full_history_sizing/`: workbook, ranking CSV, summary, and input hash manifest.

The 66-candidate comparison is explicitly in sample. Flat net/MDD is 10,522.04 / 144.37; best net/MDD candidate is Table + IV ratio at 9,937.07 / 124.74. Highest net is Calendar Event at 10,587.37 / 145.50, with a slightly lower net/MDD ratio than flat. No candidate beats flat on both full-history net and net/MDD. Historical split-based studies below remain archived; their split rule does not apply to this new analysis.

## Current operating conclusion

- Keep base size at 1. Only cut size on clearly poor days; maximum permitted size is 1.15.
- Do not rescale sizes to an average and never boost the 09:16 table. Archived split-based studies required beating flat on both validation net and net/MDD. The latest full-history analysis uses no split and reports those metrics over all dates.
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
