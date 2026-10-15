# GammaShort research ledger

Open `GammaShort_Research_Ledger.xlsx`. This combines 14 saved research iterations
and 189 daily policy paths. It does not refit strategies or change live trading.
The original source workbooks remain unchanged.

## Workbook navigation

- **START_HERE**: iteration index, dates, coverage, research protocol and source.
- **All_Iterations**: overall and saved split/warmup/scored-period metrics.
- **01_… through 14_…**: separate sheets for each iteration, described below.
- **Risk_Trade_Replay**: all 799 original risk predictions, raw instability
  readings, component ranks, realized outcomes, flags and verified replay.
- **Risk_Excel_Formulas**: executable Excel formulas for those 799 trades.
  Excel recalculates on opening. Static values remain in Risk_Trade_Replay.
- **Risk_Inputs / Risk_Coefs / Risk_Transforms / Risk_Input_Dictionary**:
  transformed predictors, intercepts, coefficients and transformations.
- **Risk_Rank_Keys**: exact integer order keys and 17-digit raw text preserve
  legacy distinctions smaller than Excel's numeric precision. Excel instability
  formulas rank these keys. Rebuild when changing raw inputs; editing the
  displayed raw values does not regenerate their frozen archive order keys.
- **Annual_Risk_Replay**: all 617 scored 2022+ annual predictions, component ranks
  and instability, flags and saved-value comparison. Warmup 2021 entries have
  no annual estimate and remain visible in Annual_Risk_Trades.
- **Annual_Raw_Inputs / Annual_Transformed_Inputs / Annual_Risk_Coefs /
  Annual_Risk_References / Annual_Risk_Folds**: exact per-fold reconstruction
  inputs and previous-year training cutoffs, not future-year percentiles.
- **Equity_DD_Charts**: baseline comparisons for the archived DD-first,
  higher-tax and tick-delay examples. These charts do not select a live policy.
- **Reporting_Audit**: iteration-level reconciliation results.

Each iteration has:

| Sheet suffix | Contents |
|---|---|
| Policies | Short policy IDs mapped to full candidate names and evidence limits |
| Inputs | Original saved daily inputs/features; preserved separately |
| Trades | One eligible-trade date per row; base metadata and per-policy size, net PnL, delta, action, entry, leg and available exit |
| Daily | All book dates; per-policy net PnL, cumulative equity and global DD |
| Summary | Whole-period and available split/warmup/scored-period statistics |
| Quarterly | Calendar quarter PnL, reset DD, counts, win rate, profit factor and sizing |
| Yearly | Calendar year statistics |
| Monthly | Calendar month statistics |
| Financial_year | April–March statistics |
| Groups | Leg, original-entry stage and available dicey-flag attribution |
| Source_tables | Original saved candidate summaries, coefficients, notes and other supporting tables, in labelled blocks |

Trade columns beginning **Base_** describe the original production trade.
Modified-entry exits are populated from saved counterfactual exit fields when
available. Counterfactual strikes were not saved; the base strike must not be
treated as the delayed/relaxed trade's strike. A skipped row remains in Trades
to expose its opportunity cost. Execution counts use recorded activity/size,
not whether realized PnL happens to be nonzero.

## Interpretation and precision

PnL uses the original normalized backtest units, with saved costs and event
weights. Sizes are multipliers, not whole contract quantities. Annualized
account returns and Sharpe ratios are omitted because no account-capital model
is specified. Period DD resets at each period start, whereas Daily DD uses
the preceding global equity peak. Quarter/year DD values cannot be added.
Group DD follows that group's chronological observations, not the total book.

Modern iterations preserve every saved candidate daily path. Several older
iterations saved only their selected daily policy or rounded values. Their
coverage/precision is explicit in Policies and START_HERE; unsaved candidate
histories are not fabricated. All their saved candidate summaries remain in
Source_tables. Older studies cover 1428 dates; newer studies cover 1430. Check
date ranges before comparing totals.

Full-history risk/sizing fits and mean-one balancing are in sample. The annual
and tick-entry overlays fit their own thresholds on previous years but retain
legacy base LUT selection and fill assumptions. The older instability extractor
includes the entry second and has not been certified subsecond causal.
The ledger adds reporting, not new evidence of strategy alpha.

Exact mathematical definitions: `../../RISK_FORMULAS.md`. The 40/40/20 weights
are fixed research choices. Expected downside predicts `max(-net_PnL,0)`,
including zero on profitable trades; it is not conditional loss size or VaR.

## Formula verification and rebuilding

`Risk_Formulas.xlsx` is the smaller editable formula workbook.
`Risk_Formulas_Calculated.xlsx` contains formula results recalculated by a
spreadsheet engine. `verify_research_ledger.py` compares these results with the
saved coefficients/predictions and verifies every policy's daily equity/DD,
trade counts and every quarterly/yearly/monthly/FY result.

From the repository root:

```bash
research/.venv/bin/python research/build_research_ledger.py
mkdir -p /tmp/gs-risk-ledger-recalc
libreoffice -env:UserInstallation=file:///tmp/gs-risk-ledger-lo-20261015 --headless --convert-to xlsx --outdir /tmp/gs-risk-ledger-recalc research/output/research_ledger/Risk_Formulas.xlsx
cp /tmp/gs-risk-ledger-recalc/Risk_Formulas.xlsx research/output/research_ledger/Risk_Formulas_Calculated.xlsx
research/.venv/bin/python research/verify_research_ledger.py
```

For a new iteration, append a new ID/source/protocol/coverage entry to
`research/research_iterations.json`, preserve its source workbook, and rebuild
and verify the ledger. Add a loader if its daily artifact schema is new. Report
an iteration as complete only after its trade and period outputs are included.
The registry and this convention preserve the owner's request for future work.
