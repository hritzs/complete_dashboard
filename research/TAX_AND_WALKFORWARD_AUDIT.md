# Tax and walk-forward audit — 2026-10-19

Read-only inspection of the original shared strategy sources and the local
research replay. No strategy, sizing or live configuration was changed.

## What the original tax does

`/mnt/shared/GammaShortData/GammaShort/phase2_optimize.py:334–369` implements
`solve_lut(master_profile, trade_tax)`. For each observed cell it computes:

    P = accumulated training PnL, after configured real costs
    N = accumulated training evidence / weighted trade count
    weight = round(100 * (P - N * trade_tax))

The minimum-cut / maximum-weight closure chooses a joint set of cells under
the six-axis monotonicity constraints. Minimum anchor evidence and zero-weight
tie rules also apply. A cell verdict is therefore not an independent test of
that cell's sample mean against tax.

The production CW stages use `(13,5,2,2,6)` at09:16,09:17,09:18,09:19,09:20+.
Higher tax makes the training objective more demanding. It does not charge a
new execution fee and is never subtracted again from reported trade PnL.
Actual entry and hedge costs are separate; the phase2 cost comments and profile
construction at lines134–138 and840–889 explicitly distinguish them.

Illustration only: P=160 and N=10 gives objective credit30 at tax13,10 at15
and-10 at17. The post-cost training PnL remains160;30 is a solver weight before
cents scaling, not the reported PnL. The graph, rather than that one weight
alone, determines whether the cell survives.

## Where walk-forward belongs

Tax is inside each training solve, not a charge applied after walk-forward:

    choose a training cutoff
      -> collect completed earlier training observations
      -> apply actual modeled costs
      -> solve stage LUTs using P-N*tax
      -> enforce stage nesting
      -> freeze tables and any sizing calibration
      -> replay the next period only
      -> advance the cutoff and repeat

If tax levels themselves are selected, selection must use training / inner
validation periods, never the outer scored period. Holding the established
13/5/2/2/6 vector fixed is different from selecting a new vector on test PnL.

`holdout_test.py:49–78` provides a real chronological holdout implementation:
it restricts profile rows to training financial years before solving taxed
LUTs. Its selection later uses training results. This is one holdout split;
it is not itself an automatic rolling walk-forward loop.

The production table directory's README records separate split-half and
walk-forward validation/calibration studies. The existence of those studies
does not make every replay with the exported production tables walk-forward.

## What the sizing runs actually replayed

Local `GammaShort_Backtest_Final.py:460–473` loads one set of five CSV LUTs
as Boolean arrays. It does not load a different table vintage for each year.
The frozen799-entry book used by sizing15/16 came from that fixed-table replay.
Their sizing models were annual prior-only fits, but their base entries were
not rebuilt using a separate prior-only LUT for every scored period.

Local `higher_tax_sizing.py:70–78` correctly filters training observations by
cutoff before solving base tax+2/+4 masks. That earlier experiment changes
conviction on the legacy base book; it does not repair the base book's entry
selection merely by adding a prior-fitted higher-tax mask.

The legacy `7DOptimizerVMonotonic.py:43–44` also currently contains reversed
exclusion dates: start2027-08-01, end2027-04-30. Its interval condition at
line907 consequently excludes no observations. This is a property of that
inspected legacy file, not proof that the running live process uses it. The
newer phase2 explicitly rejects reversed exclusion ranges at lines795–799.

## Implication for the next sizing experiment

The latest reruns did not establish the requested profit/DD improvement at
unchanged average activity. WF11 scored mean0.956; uniform flat sizing at that
same ex-post mean has slightly lower DD than WF11. Reduced participation must
be separated from an allocation edge.

A strategy-aligned candidate can use a cell's survival under progressively
higher **past-trained** tax hurdles as an edge-strength input, with evidence
and downside checks. This measures robustness to a stricter historical hurdle;
it is not a calibrated expected return or win probability. Modest boosts and
dicey cuts must be evaluated against both flat1 and exposure diagnostics.
The mapping from this input to size must be fixed or chosen using inner past
validation before scoring, with no post-result tuning.

For a fully chronological comparison, refit the base entry LUTs as well as
the stronger-tax masks and sizing calibration at each cutoff, and replay the
first eligible entry under that fold's tables. CW and NW retain their own
entry procedures. A sizing overlay on the old fixed799 entries must continue
to be labelled conditional instead.

Sizing objective clarification was requested: target arithmetic mean1 using
past calibration, or prioritize a fixed risk budget even when mean falls.
Exact arithmetic mean1 over unknown future trades cannot be promised without
future-dependent balancing. No new size mapping or performance result is
claimed by this audit.
