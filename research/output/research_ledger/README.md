# GammaShort research ledger

Open `GammaShort_Research_Ledger.xlsx`. This combines 16 saved research iterations
and 193 daily policy paths. It does not refit strategies or change live trading.
The original source workbooks remain unchanged.

## Workbook navigation

- **START_HERE**: iteration index, dates, coverage, research protocol and source.
- **All_Iterations**: overall and saved split/warmup/scored-period metrics.
- **01_… through 16_…**: separate sheets for each iteration, described below.
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

The **Algo_** sheets add boost/cut explanations and executable sizing formulas:

- **Algo_Catalog / Algo_Guide**:21 algorithm families, mathematical definitions,
  inputs, outputs, training protocol, limitations and source-code references.
- **Algo_Policy_Map / Algo_Changes / Algo_Priority**: all189 saved policy IDs,
  version changes and the exact precedence of risk cuts, warmup and boosts.
- **Algo_Conviction / Algo_Profile_Feasibility**: all799 historical S1/S2 edge
  gates and why the Gentle profile could not satisfy its mean-one budget.
- **Algo_Annual**:3,196 date/candidate rows replay all four annual fixed-boost
  policies, with warmup, risk/strong flags, tax hurdles, reasons and Excel sizes.
- **Algo_Opt_Inputs / Algo_Opt_Coefs / Algo_Opt_Replay / Algo_Opt_Checks**:
  coefficients, offsets, bounds, floor/ceiling clipping, action reasons and
  executable raw/final-size formulas for11,985 trade allocations across15
  saved optimized policies. Diagnostic historical risk/conviction flags shown
  on normalized-optimization rows were not constraints in that earlier study.

`Sizing_Algorithms_Explained.xlsx` is the smaller editable algorithm workbook;
`Sizing_Algorithms_Calculated.xlsx` includes spreadsheet-engine-calculated
results. The master contains the same algorithm sheets plus all trade/period
PnL reporting. Details are in `../../SIZING_ALGORITHMS.md`.
Family mappings for legacy selected-only reports identify the study's algorithm
families; generic archived selection labels do not assert that every family
was used simultaneously. Original source tables preserve the available detail.

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
mkdir -p /tmp/gs-algorithms-recalc
libreoffice -env:UserInstallation=file:///tmp/gs-algorithms-lo-20261015 --headless --convert-to xlsx --outdir /tmp/gs-algorithms-recalc research/output/research_ledger/Sizing_Algorithms_Explained.xlsx
cp /tmp/gs-algorithms-recalc/Sizing_Algorithms_Explained.xlsx research/output/research_ledger/Sizing_Algorithms_Calculated.xlsx
research/.venv/bin/python research/verify_research_ledger.py
```

For a new iteration, append a new ID/source/protocol/coverage entry to
`research/research_iterations.json`, preserve its source workbook, and rebuild
and verify the ledger. Add a loader if its daily artifact schema is new. Report
an iteration as complete only after its trade and period outputs are included.
The registry and this convention preserve the owner's request for future work.


## Annual reruns15/16

The separate past-only reruns preserve the old10/11 archives. Annual models
fit completed prior years;2021 warmup stays1. Strict-prefix inputs and frozen
training offsets replace entry-window and future mean balancing. Scoring
mean is reported as observed. Original taxes and archived09:16 CW cap1 remain;
WF11 dicey cap.85 overrides all optimized boosts. See15_/16_Trades and their
quarterly/yearly/monthly/FY sheets; Source_tables retains scalers, S1/S2/risk
coefficients, references and formula definitions. `Algo_WF_Folds`,
`Algo_WF_Inputs`, `Algo_WF_Coefs` and `Algo_WF_Replay` retain annual settings
and1,234 executable sizing replays. The separate rerun workbook contains
2022+ equity/DD charts. All remain conditional on legacy base LUT/fill bias.

| Policy | Net | MDD | Worst day | Net/DD | Mean size |
|---|---:|---:|---:|---:|---:|
| Flat size 1 | 7,593.21 | 144.37 | -61.28 | 52.60 | 1.000000 |
| WF10 maximum profit | 7,547.88 | 152.63 | -70.47 | 49.45 | 0.976479 |
| WF11 dicey0.85 maximum profit | 7,435.66 | 138.66 | -53.87 | 53.63 | 0.956000 |

Neither rerun passes the declared forward qualification (higher net, higher net/DD and no worse worst day).
