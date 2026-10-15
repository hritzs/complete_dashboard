# GammaShort research summary and continuation state

Updated 2026-10-15. This is offline research; it contains no authorization to change live trading behavior.

## Current decision

Keep the current book flat at size 1. The owner allows cuts on clearly poor days, caps any allowed increase at 1.15, forbids rescaling to a TRAIN average, and explicitly forbids boosting the 09:16 table. A candidate must beat flat on both net profit and net profit / maximum drawdown on VALIDATION before selection.

## What the studies found

Completed studies tested table/input sizing, pre-entry stability, drawdown overlays, early-exit rules, volatility risk premium, volatility targeting, stop distance, calendar/regime flags, parameter scores, capped sizes, and borderline/near-YES cuts. None establishes a reliable candidate that passes the owner's validation rule. The v6 borderline study selected flat; near-threshold trades were not consistently worse than firm trades, and isolated cells were too rare for useful inference.

The detailed candidate definitions, sample periods, numerical results, and caveats are in `../SIZING_RESEARCH_HANDOFF.md`. Backtest PnL uses normalized Rs (index points × 25,000 / entry underlying), not actual account rupees.

## Relaxed 09:20+ entry study: pending

`GammaShort_Backtest_Final.py` has a research-only `GS_RELAX=1` path for four preregistered neighbor-support rules (R1–R4), and `entry_relax_eval.py` evaluates each at size 1 and 0.75 under the same validation gate. The run is **incomplete**: `output/backtest_relax/run_027bf0249b/days/` had 652 of 1,430 expected daily pickle files when checked, and no backtest process was running. There is no valid study decision yet. Resume using the command in `../TODO.md`; then evaluate only when the run is complete and confirm production PnL matches the reference.

## Derived strategies: scaffold only

The `GS_DERIVED` / `simulate_legs` addition in `GammaShort_Backtest_Final.py` is incomplete. It provides a generic multi-leg simulation helper but does not wire long-gamma, iron-fly, late-day short, or other candidates into the per-day run. It has not been run or validated. Treat all derived strategy ideas as untested; define and preregister the exact strategy and decision rule before implementing/evaluating one.

## Continue here

1. Resume and finish the relaxed-entry run, then run `entry_relax_eval.py` and record the complete output and decision.
2. Update `SIZING_RESEARCH_HANDOFF.md` and this summary with that result; preserve the fixed rules and TEST-once discipline.
3. If beginning derived strategies, keep them as separate research candidates, use only same-day tick inputs/fill/hedge assumptions, and report their limitations. Do not edit `services/` or `ui/` from research conclusions.

See `../TODO.md` for commands and method constraints. Existing source modifications and generated reports may be uncommitted; inspect `git status` before starting new edits.
