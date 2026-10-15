# GammaShort research TODO

Research is isolated under `research/`. Do not translate a research result into `services/` or `ui/` without the owner's explicit direction.

## Current operating conclusion

- Keep base size at 1. Only cut size on clearly poor days; maximum permitted size is 1.15.
- Do not rescale sizes to a TRAIN average and never boost the 09:16 table. The owner's validation gate remains: a candidate must beat flat on both net profit and net profit / maximum drawdown on VALIDATION.
- Current decision is flat. No completed sizing study has established a reliable out-of-sample improvement.

## Completed studies

Table and parameter sizing, pre-entry stability, drawdown overlays, early exits, VRP, volatility targeting, stop distance, calendar/regime flags, score sizing, capped sizing, and borderline-cell cuts have been evaluated. The 09:20+ relaxation experiment has a fixed rule and an evaluator, but its backtest is incomplete; it has no decision yet. See `SIZING_RESEARCH_HANDOFF.md` and `output/RESEARCH_SUMMARY.md` for results and caveats.

## Immediate continuation

1. Resume the relaxed-entry backtest. `output/backtest_relax/run_027bf0249b/days/` currently contains 652 of the expected 1,430 day records, and no backtest process was running when checked. The script resumes by skipping existing day files.
2. Use the already fixed R1–R4 candidate definitions and run `./.venv/bin/python entry_relax_eval.py` only after all expected days are present. Verify its production PnL matches the reference run and preserve the chosen validation rule; report TEST once.
3. Update the handoff and output summary with the actual result, or state why the run could not resume (for example, unavailable tick share/dependencies). Do not infer results from the partial run.
4. Only after that study, define and preregister any derived strategy experiment. The `GS_DERIVED` / `simulate_legs` code in `GammaShort_Backtest_Final.py` is an unfinished generic scaffold: no strategy candidates are wired into `process_day`, and it has not been run or validated. Do not treat it as a result.

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

## Method constraints

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
