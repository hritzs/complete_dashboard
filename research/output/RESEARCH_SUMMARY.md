# GammaShort research summary and continuation state

Updated 2026-10-15. This is offline research; it contains no authorization to change live trading behavior.

## Current decision

Keep the current book flat at size 1. The owner allows cuts on clearly poor days, caps any allowed increase at 1.15, forbids rescaling to a TRAIN average, and explicitly forbids boosting the 09:16 table. A candidate must beat flat on both net profit and net profit / maximum drawdown on VALIDATION before selection.

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
