# GS tick-by-tick Monte Carlo — 2026-10-22

Frozen before any simulated result. Owner request: a tick-by-tick Monte Carlo
of the GS strategy on 10,000 MODEL-generated trading days, comparing sizing
rules, the study-22 adjustments, SL / TP levels and hedge-band settings, then
"which simulation gives max profit and lower DD". Research only: no live code,
.env or shared-input changes.

## 1. Engine

Every simulated trade runs through the UNCHANGED frozen latest-GS engine
(`output/logic_comparison_20261021/sources/comparison_new_engine.py`,
`simulate_trade`): same minute grid, futures hedge walk every second, points /
delta bands, intra-minute rule, SL / TP / TV / IV-drop / time exits, entry and
hedge costs. The simulator only builds the engine's input arrays (underlying,
bid/ask, futures execution averages, DTE, per-strike option LTP/bid/ask and
30-s execution averages, OTM IV, ATM reference straddle) from a model path.
Policy variants change only the engine's own parameters (SL bps, TP bps,
PARAM_HEDGE_FRAC, PARAM_INTRA_MULT, PARAM_HEDGE_DIV / PARAM_STR_D / floor,
DELTA_THRESH) or add the study-22 legs / sizing multipliers on top.

Entry: every simulated day trades the CW ATM straddle at 09:16 (size 1, the
day's DTE row). The LUT's entry decision is NOT simulated (its daily inputs
have no model counterpart): results are conditional on an entry, not a
replacement for the LUT. NW is not simulated.

## 2. Path model (calibrated on 2021-2024 real ticks only)

Calibration data: `/mnt/shared/TickDataIV`, CW expiry, trading days
2021-01-01..2024-12-31 (2025+ held out for the realism check).
Per day: 1-s underlying, ATM straddle, OTM IV at 09:16 and through the day,
option bid/ask spreads by moneyness/DTE/premium.

- Day drivers sampled jointly from the calibration days (block of the real
  day's values, not independent draws): DTE row, 09:16 underlying level, ATM
  IV at 09:16, the day's realized-to-implied ratio (VRP) and opening move.
- Underlying: 1-s log returns with intraday U-shaped variance (calibrated by
  minute of day), stochastic variance (log-OU at minute scale, calibrated
  mean reversion and vol-of-vol), Student-t innovations (calibrated degrees
  of freedom) and Poisson jumps (calibrated rate and size distribution);
  scaled so the day's realized variance matches its sampled VRP x IV^2.
- Implied vol: ATM IV follows its own path with calibrated spot-vol
  correlation (IV up when the market falls) and minute-scale noise; smile
  IV(K) = ATM IV x (1 + a·m + b·m^2), m = ln(K/F)/sqrt(T), a/b by DTE row.
- Option prices: Black-76 on the synthetic future (as the platform); bid/ask
  = mid -/+ half-spread from the calibrated spread model (minimum one tick);
  LTP = mid. Engine's own 30-s / 3-s execution averages are applied to the
  synthetic quotes exactly as to real ones. DTE falls on the session clock.
- Random seeds fixed; 10,000 days, the same days for every policy.

## 3. Realism gate (before any policy result is read)

Compare model vs held-out real 2025+ days: daily realized vol distribution,
1-min return kurtosis, intraday variance profile, IV change vs return
correlation, ATM straddle decay; and GS baseline on simulated days vs the
engine's real GS trades: SL / TP / TV / time exit shares, hedge counts,
mean and quantiles of trade PnL. Report every gap. If the baseline's simulated
PnL distribution is far from the real one, results are reported as model
stress tests only.

## 4. Fixed candidate list (one family at a time, others at baseline)

- Baseline: current engine parameters.
- SL / TP: SL x0.75, x1.25, x1.5 of the engine's SL bps; TP x0.75, x1.25; no TP.
- Hedge bands: points band x0.75 and x1.25 (PARAM_HEDGE_DIV / PARAM_STR_D);
  intra multiple 1.5 and off; minute-end hedge fraction 0.5 and 1.0 (full);
  expiry-day delta band 0.15 and 0.35 (DELTA_THRESH row 0).
- Adjustments (study 22, at the first confirmed 4.24 regime signal on the
  simulated underlying): Wings, Roll, BuyTested, SellUntested, NWLongStraddle
  is excluded (no NW model), SellMore.
- Sizing (multiplier on the whole trade): high-VRP cut 0.85 (09:16 IV/RV20
  top tercile of calibration), pre-entry instability cut 0.85 (top quartile of
  the 09:15-09:16 range/spread proxy), low-IV boost 1.15.

## 5. "Max profit and lower DD" selection protocol

Paths are split by seed into A (5,000) and B (5,000). For each family the
candidate with the highest mean PnL whose A-half DD (from 250-day block
sequences, see below) is no worse than baseline is chosen on A ONLY; one
combined policy takes each family's A-choice. All choices are then reported on
B and in a real-data replay of 2025+ (the engine on real ticks with the same
parameters; study-22 overlays from the saved study where applicable).

DD: simulated days are ordered into 40 independent 250-day years per half;
report mean annual profit, median and 95th-percentile maximum DD, worst day,
5% expected shortfall per trade, and profit/DD. A candidate counts as "more
profit and lower DD" only if it beats baseline on B on mean profit AND on both
median and 95th-percentile DD, and the real 2025+ replay does not contradict
it. Even then it is a hypothesis for a causal replay and forward paper
trading, not an adopted rule.

## 6. Outputs and checks

`output/tick_montecarlo/`: calibration tables, realism report, per-day per-
policy results (parquet), summary workbook, charts, REPORT.md, manifest with
input/code hashes. Independent verification re-runs a sample of simulated
days end to end from the saved seeds and checks every policy's PnL. Unit
tests cover the path generator's moments and the engine-input builder.
Atomic checkpoints; resume only on matching hashes.
