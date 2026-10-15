# GammaShort research summary and continuation state

Updated 2026-10-15. This is offline research; it contains no authorization to change live trading behavior.

## Latest: average-size-one optimization (owner requested)

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

The previous live operating conclusion remains flat at size 1. Current research permits normalized size redistribution with mean 1, max 1.15, and no boosting of the 09:16 table. Archived split-based studies required beating flat on both validation net and net/MDD. The latest full-history research above uses no split and is not a live implementation.

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
