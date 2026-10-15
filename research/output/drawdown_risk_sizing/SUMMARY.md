# Drawdown-first sizing with mandatory dicey-day cuts

- Drawdown-first full-history research: see DRAWDOWN_SIZING_SPEC.md, saved before evaluation. No 60/20/20 split.
- Full-history risk-model fitting, parameter search, quantiles and normalization are IN SAMPLE; not causal live performance or a claim that losing days are predictable.
- Dicey = top-quartile composite estimated loss/downside/instability risk OR missing CW entry features. Defined before evaluating the cut policies; never simply flags realized losing days.
- Loss classifier lambda20 and downside ridge lambda50 exclude fullfit S1/S2 risk inputs. Risk coefficients, entry feature transforms and daily risk scores are saved.
- Every new policy must cut ALL dicey entries to its fixed ceiling (0.90/0.85/0.75); normalization cannot undo the cut. Global floor0.75, ceiling1.15, 09:16 CW <=1, arithmetic mean exactly1 over799 entries.
- Three numerical search objectives per ceiling: minimum MDD, maximum net/MDD, maximum net. Qualifying policies require net>=flat, MDD<=flat and worst daily net>=flat worst. Feasibility-first search may explore violations; unqualified results are explicit.
- Reference stress features use flat-book results up to yesterday; they do not implement a halt based on the sized book equity. Annual/leg/risk-group means need not equal1.
- Previous optima and flat are BENCHMARKS and do not enforce the new dicey ceilings; Qualified refers only to new cut policies.
- Net/costs scale linearly from saved production PnL. Units normalized Rs for25000 notional; no quantity-specific tick replay/lot rounding. Event-weighted exposure proxy reported separately from arithmetic multiplier mean.
- Risk estimates use entry inputs; their historical fitting and the full-period normalization use future information relative to early dates. Full-history numerical local search has no global-optimum guarantee.

Dicey entries: 201 / 799. Qualified new policies: 9.

Lowest observed qualifying MDD: Dicey<=0.85: Minimum DD; net 10749.08, MDD 105.41, net/MDD 101.97, worst day -56.16; mean size 1.000000000000; dicey mean/max 0.8057/0.8500.
