# Exact risk calculations and iteration reporting

These formulas describe the saved implementations in `sizing_drawdown_risk.py`
and `higher_tax_sizing.py`. They document research estimates, not a calibrated
probability guarantee or an adopted live policy. No models are refitted by the
report builder.

## Inputs and transformations

Each trade has a predictor vector x: IV/RV measures, the original LUT inputs,
entry-window readings, calendar/leg indicators and previous flat-book drawdown
and loss streak. The coefficient sheet lists every included feature. Outcome-
fitted S1/S2 columns ending `_fullfit` are excluded from both risk regressions.

In the original full-history study, continuous variables are filled using the
full-sample median, standardized using full-sample mean and population standard
deviation, then clipped to [-3,3]. CW indicators remain 0/1. Previous drawdown
is clipped to [0,3] after dividing by 75; previous loss streak similarly by 3.
Saved `Model_inputs` are the exact transformed regressors. In the later annual
study, every raw column is imputed/standardized/clipped using the prior-year
training set, including indicator and stress columns. Its saved fold JSON files
contain medians, means, standard deviations, coefficients and references.

## Loss probability

Training label: y_i = 1 if the base trade's net PnL_i < 0, otherwise 0.

    z_i = beta_0 + sum_j beta_j * x_ij
    p_i = 1 / (1 + exp(-clip(z_i,-25,25)))

The logistic objective is summed negative log likelihood plus
`(20/2)*sum_j beta_j^2`, excluding the intercept from the penalty. The code uses
Newton/IRLS updates, at most 60 iterations, stopping when step norm<1e-8;
curvature is floored at 1e-6. Clipped logits provide numerical stability.
These are model estimates; no separate probability calibration was performed.

## Expected downside

    target_i = max(-net_PnL_i,0)
    gamma = (X_centered' X_centered + 50*I)^(-1)
            X_centered' (target - mean(target))
    gamma_0 = mean(target) - mean(X)' gamma
    downside_i = max(gamma_0 + x_i' gamma,0)

This is ridge regression with an unpenalized intercept, equivalent to minimizing
`sum_i(target_i-gamma_0-x_i'gamma)^2 + 50*sum_j gamma_j^2`.
It estimates the unconditional negative part of PnL, with winners contributing
zero. It is neither a conditional-loss forecast nor VaR/expected shortfall.
Its unit is the saved backtest's normalized PnL unit.

## Instability and exact ranks

For the original approximately60-second window ending at the entry second:

    underlying_range_bps = 10000*(max(U)-min(U))/U_at_entry
    straddle_range_pct = 100*(max(CE_LTP+PE_LTP)-min(CE_LTP+PE_LTP))/straddle_at_entry
    spread_pct = 100*((CE_ask-CE_bid)+(PE_ask-PE_bid))/straddle_at_entry
    instability = (rank_U + rank_straddle + rank_spread)/3

Each instability reference includes only valid CW readings. Missing readings
and NW readings receive neutral rank 0.5. Any missing CW instability reading
also independently forces the dicey flag. The legacy extractor uses second
buckets and includes the entry second: this is not the strictly-before-entry
extractor introduced in the later tick-entry experiment. Do not silently swap
the fresh tick features into an archived risk model.

The original rank is pandas average tied rank divided by N, equivalent to
`RANK.AVG(value,reference,1)/COUNT(reference)` in Excel. Precisely, if L reference
values are less than v and E equal v:

    rank(v) = (L + (E+1)/2)/N   when E>0
              L/N             when E=0 (annual scoring of a new value)

Thus a constant sample ranks at (N+1)/(2N), slightly above0.5. This differs
from the mid-CDF percentile of the later premium-percentile experiment.
Some legacy raw readings differ only beyond Excel's 15 significant digits.
`Risk_Rank_Keys` therefore stores exact dense-order integer keys derived from
the original Python readings, together with 17-digit raw text. The executable
Excel instability ranks use these keys to preserve the archived order and ties.
They remain equivalent to ranking the original raw double-precision readings.
Regenerate the keys with the builder when changing raw inputs; they are frozen
archive inputs, not automatically updated by editing a displayed raw value.
Probability and downside ranks compare calculated numbers directly using
SUMPRODUCT; COUNTIF with concatenated numeric criteria can round comparisons.
The annual version uses only prior-year references; new values below/above
all reference values rank 0/1. It never ranks using the future scoring year.

## Combined score, dicey rule and size

    risk_i = 0.4*rank(loss_probability_i)
           + 0.4*rank(expected_downside_i)
           + 0.2*instability_i
    threshold = 75th percentile of training risk scores (linear interpolation)
    dicey_i = (risk_i >= threshold) OR missing_CW_instability_feature

The 40/40/20 weights are a fixed research design choice, not weights learned
from data. The score is a relative risk index, not a loss probability.

The original full-history threshold is 0.6677157174036119, with 201/799 dicey
entries. Its regressions, reference ranks, thresholds and mean-one sizing
normalization use the complete sample, so the results are IN SAMPLE.
Later annual thresholds vary by fold; 2021 is warmup, scored at size1.

The DD optimization constrains every flagged trade to its candidate ceiling
0.90/0.85/0.75. A bounded projection `size=clip(raw_size+offset,lower,upper)`
chooses the offset so the historical arithmetic mean equals1; it cannot undo
the risk ceiling. That historical balancing is not causal future balancing.
The later high-tax overlay uses size 0.85 on dicey trades; otherwise 1.05/1.10
for qualifying higher-tax conviction, otherwise 1. Risk cuts override boosts.
Original LUT taxes (13,5,2,2,6) remain entry hurdles, not deductions from PnL.

## Required reporting for each iteration

`build_research_ledger.py` rebuilds the master workbook from a versioned registry
and saved artifacts. Every iteration has its own policy map, daily results,
trade separation, calendar quarterly/yearly results, summary and saved source
candidate tables. Reports also provide month, financial-year and leg/stage
breakdowns. Period DD resets to zero at each period start; daily global DD
retains the preceding equity peak. Trade counts use execution/size flags, not
nonzero PnL. Profit factor is gross positive net-day PnL divided by absolute
negative net-day PnL; no annualized return or Sharpe is invented without capital.

All modern candidate daily arrays are retained. Some older workbooks saved only
the selected daily sizing, rounded to3 decimals, or only summary comparisons.
Those coverage/precision limits are explicit; unsaved candidate daily histories
are not invented. Their original candidate tables remain available separately.
Legacy and modern date ranges differ; compare only matching periods/books.

Future research iterations must be registered and the ledger rebuilt before
being described as reported. Keep archived source workbooks unchanged and label
training protocol, warmup/test periods, source precision and causal limitations.
