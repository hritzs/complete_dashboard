# Tick-entry research results

Conditional entry-overlay study; all original LUT taxes (13,5,2,2,6), production
NW trades, size multiplier1 (including original event weights) and saved
exits/costs remain unchanged. Features use strictly
earlier ticks, fixed pre-decision ATM strike and backward as-of snapshots.
2021 is warmup; across-day thresholds fit completed prior years only.

| Policy | Net | MDD | Net/DD | Worst day | Skips | Delays |
|---|---:|---:|---:|---:|---:|---:|
| Flat | 7593.21 | 144.37 | 52.60 | -61.28 | 0 | 0 |
| P20 | 5992.09 | 173.71 | 34.50 | -55.77 | 105 | 0 |
| P10 | 6560.90 | 153.02 | 42.88 | -55.77 | 66 | 0 |
| Rcheap | 5116.55 | 208.66 | 24.52 | -61.28 | 138 | 0 |
| Rrich | 1662.51 | 148.08 | 11.23 | -50.37 | 443 | 0 |
| Lspread | 7360.34 | 144.37 | 50.98 | -61.28 | 6 | 0 |
| Mstress | 7057.76 | 162.80 | 43.35 | -61.28 | 15 | 0 |
| Combined | 4128.38 | 226.07 | 18.26 | -50.37 | 200 | 0 |
| Delaycheap | 7362.48 | 139.86 | 52.64 | -55.77 | 4 | 128 |


Qualification (net and net/DD above flat, worst day no worse): none.
The one-minute delay cuts net by 230.72
(3.04%) and MDD by 4.51
(3.12%). It delays128 entries and skips4;
it is a profit/risk tradeoff, not a higher-profit entry rule. Its randomized
net tail fraction is 0.0470, or
0.3756 after correcting these eight comparisons.
There is no robust new profit edge established by this comparison.
This is an exploratory comparison of eight fixed hypotheses, not an untouched
final holdout or permission to deploy. Values are the backtest's normalized PnL
units, not account-level rupees. Calendar daily equity includes no-trade days.

P20/P10 reject low within-minute premium percentiles. Rcheap rejects bid residual
z<-1, Rrich requires z>=1 for valid observations. Lspread rejects prior-year top
decile spread; Mstress requires both range and premium change above prior-year
90th percentiles. Combined unions P20, Rcheap and Lspread. Delaycheap defers
adverse original 09:16/09:17 signals one minute only if the saved next-minute
LUT gate passes, otherwise skips. No later rescan or NW fallback is simulated.
Missing features keep the original trade. Coverage and matched-subset attribution
are explicit. Constant-size flat comparisons use the retained entry-count
fraction, an ex-post activity proxy rather than measured capital exposure.

Butterfly credits are quote diagnostics only, sampled at existing CW entries.
Scored-period positive quote credits: 2 call and 2 put
observations. See butterfly_candidates.csv; these are not completed trades.
Positive credit does not establish executable arbitrage: quote simultaneity,
fee schedules, contract lots and all-leg fills require separate verification.
This does not test a continuously scanned or delta-hedged statistical-arbitrage
strategy. Residual richness is a short-window price model, not demonstrated
mean reversion or a reliable expected-profit estimate.

Legacy base LUTs were fitted on broader history; saved fill/counterfactual
assumptions have not been certified causal. Although the new tick features and
annual threshold fitting are past-only, this is NOT an end-to-end leakage-free
strategy backtest. Filtering evaluates existing entries only, at fixed size;
it does not yet test different sizing or live order placement.
The past-only ATM differs from the saved fill strike on 17 valid
CW days; features deliberately use the strike knowable before the entry instant.
Stored tick timestamps are assumed to represent observation availability;
exchange/feed provenance and the legacy fill pipeline are not certified here.

See tick_entry_comparison.xlsx, all 799 trades
in trade_comparison.csv, daily_comparison.csv, coverage.csv, matched_coverage.csv,
yearly.csv, stage_comparison.csv, training_thresholds.csv and features.csv.
Coverage: 716/720 CW days have usable features; scored-period
coverage is 547/550.
placebo_checks.csv reports 1,000 fixed-seed randomized trigger allocations per
candidate, matched by year/original stage/DTE and feature availability. Tail
fractions are exploratory; Bonferroni8 covers these eight net comparisons only,
not all earlier research. It cannot establish causal end-to-end alpha.
