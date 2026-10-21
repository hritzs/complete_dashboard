# GS stop, regime and drawdown research contract — 2026-10-21

Owner objective: reduce avoidable downside and severe drawdowns while retaining
useful GS expectancy. High win rate alone is not the optimization target.
This contract defines the next study; no model, optimal stop or risk reduction
has been established. No live implementation is changed.

## 1. Baseline and exact stop semantics

Use the immutable matched replay in `output/logic_comparison_20261021`, its
1430 original dates, selected CW/NW entries, event adjustments, LUTs and09:16
LUT penalty. This snapshot is not a claim about uncommitted live edits.
Keep the old archived books separate. Preserve the verified Muhurat calendar.

CW in the snapshotted engine checks total option-plus-futures PnL on its minute
grid. The gross trigger is -entry_underlying*SL_bps/10000 in source points.
At PNL_SCALE25000 this is -2.5*SL_bps normalized units for an ordinary full-size
CW position. The snapshot uses16bps for DTE_idx0 and14bps otherwise, hence
40 and35 normalized gross units, respectively, before event scaling and fees.
It is not a16%/14% premium stop or a stop on the underlying's price alone.
Event gross PnL/hedge notional are divided by3; source entry cost remains its
own fixed deduction and must not be silently rescaled. NW has a separate
implementation; do not apply the CW formula to it without auditing NW logic.

Minute checks, forward-average source fills, fees and gaps can cause the
realized net loss to differ from the gross trigger. Preserve this source
behavior for the version-matched comparison. Independently build a causal
execution baseline with quote availability, no backward fill and no future
averaging before interpreting model gains as executable. Do not attribute a
fill-model change to predictive alpha.

## 2. Risk hierarchy and permitted decisions

Maintain four distinct layers: position hard stop, intraday portfolio loss
budget including realized losses, portfolio peak-to-current drawdown limit,
and model/regime early-warning decisions. Shared GS+complement capital, margin
and net delta/gamma/vega exposures are measured jointly.

No model may widen an already established hard loss budget or suppress a
mandatory delta hedge. Once a day-level risk limit causes a stop, the first
experiment permits no new GS entry that session. Limits are trigger levels,
not guaranteed fills. Model actions can be hold, reduce, or exit; reduction
uses integer executable lots and books all close/rebalance costs. Do not
invent an affordable half-lot when the position contains one lot.

Account capital, maximum tolerable currency loss/day and drawdown budget are
not specified in this workspace research. Do not invent them or present
normalized PnL as account returns. Research may use relative-to-baseline
budgets until these values are supplied; live position sizing depends on them.

## 3. Observable regimes

Use `GS_REGIME_COMPLEMENT_SPEC.md` for trailing price efficiency, momentum,
RV, fixed-contract IV/breadth, spreads, OI cadence and hedge-stress definitions.
Regimes are Calm, Directional_expansion, Volatile_whipsaw,
Liquidity_stress, or Unavailable. Unavailable must never default to Calm.
Regime at time t uses only data available strictly before t. No full-day
range, eventual losing-day classification or end-of-day OI enters the signal.

Calm keeps baseline controls; directional expansion tests partial reduction
or early exit first; volatile whipsaw tests reduction without a directional
long; liquidity stress cannot justify buying protection at an assumed mid.
Long-option additions remain a separately evaluated sleeve. Data unavailability
retains existing hard controls and vetoes new model-driven exposure.
Hysteresis/persistence and cooldown must be frozen before replay; do not switch
regimes on each noisy quote. Earliest-entry losses during feature warmup remain
a limitation, not an assumed protection success.

## 4. Targets: SL before recovery and incremental downside

At each eligible decision time while the baseline trade is still open,
let P_t be current net marked-to-liquidation PnL (including accumulated and
estimated exit costs), and B0 the entry-fixed hard net risk budget of the new
causal baseline. Keep legacy gross stops labeled separately.

Training event target: tau_SL = first future available observation with
P_u <= -B0; tau_R = first with P_u >= P_t + 0.25*B0. Use a fixed10minute
horizon or earlier baseline session/trade end. Y=1 when SL occurs first
within that horizon, Y=0 when recovery occurs first. If neither occurs or
the baseline exits before either, mark censored; do not label it safe.
If timing resolution cannot distinguish both events, mark ambiguous/censored.
If the hard stop is already reached at t, execute the control, do not ask
the classifier to predict an event already observed.

Use future observations only to construct labels on completed training days.
The counterfactual label follows an unchanged, separately replayed baseline;
after an overlay exits early, its nonexistent future holding PnL cannot be
read from the altered policy. Never extend exposure past its baseline exit
without a separate specified strategy.

Second target: additional adverse excursion A_t = max(0, P_t - min(P_u))
over the same future horizon, with horizon/end/censor flags. Estimate its
conditional upper quantile, not just its mean. Whole-trade MAE/MFE are
descriptive audit statistics, never inputs available at entry. Censoring and
class imbalance require explicit evaluation and minimum event counts.

Start with calibrated logistic and regularized quantile baselines, compared
with a simple rule and current-distance-to-stop baseline. Compare Brier score,
reliability, precision/recall at action thresholds, actionable lead time and
coverage. Do not count repeated minute observations as independent trades.
Do not add a neural network until it beats these later-period baselines.

## 5. Limited stop experiment and validation

First comparison: matched unchanged GS; a fixed tighter stop with the same
trigger/fill semantics; a regime-only early exit; a calibrated SL-risk early
exit; and regime/model partial reduction with identical entry/lot rules.
Freeze a small stop multiplier grid0.75/0.875/1.00 of the entry risk budget.
No widening above baseline in this first study. Freeze two action-probability
thresholds using training/inner validation only. They are not selected yet.
Do not run an unbounded grid of indicators, stops, thresholds and models.

Select on inner chronological windows, evaluate on outer future blocks,
purge entire days and overlapping label horizons. Fit scalers, imputers,
percentile references, model coefficients and probability calibration on past
data only. Require sufficient stop/recovery events per fold or mark unavailable.
Do not silently drop censored cases and claim unconditional loss probability.
Report conditional event probabilities and horizon event coverage separately.

Evaluate policy PnL on EVERY calendar/traded day, not just correctly predicted
losers. Attribute avoided losses, profitable trades cut, missed recoveries,
execution cost and changed hedge PnL. Any stop/size change requires a new
path replay, not clipping daily losses or scaling the old terminal PnL.
Keep separate comparisons for any improved causal execution baseline.

The historical period has been researched before; outer folds are retrospective
tests, not an untouched dataset. Finish with a new forward paper period.
No adoption simply because a historical candidate has the smallest DD.

## 6. Proper drawdown series and risk statistics

Closed-day normalized equity E_d = cumulative sum(net_daily_PnL).
Peak H_d = max(0, E_1,...,E_d); DD_d = H_d-E_d >=0.
The series starts from zero normalized cumulative PnL, not zero account capital.
Percentage DD requires actual initial capital and any external cash flows.
No percentage return/DD is fabricated from these additive research units.

Record full-path peak date, trough date, recovery date, depth, calendar time
underwater and number of underwater sampled sessions. Open episodes remain
unrecovered. Annual/quarterly reset DD is separate from the GLOBAL DD carried
across boundaries; do not add period DDs or reset the actual account peak.
Maintain rolling20/60/252-observation DD with an explicit pre-window equity
anchor, not an ambiguous max/min of equity. Label observations vs actual days.

Intraday global equity E_(d,t) = previous closed-day equity + net current-day
realized cash PnL + marked liquidation PnL of every open option/futures position,
including incurred fees and estimated remaining exit costs. Do not double-count
realized PnL already booked in position marks. H_(d,t) includes all preceding
intraday and prior-day marks; global intraday DD=H-E. A daily loss from day open,
an intraday peak-to-trough loss, and global account DD are different quantities.
Combined-book marks must use synchronized available timestamps, correct bid/
ask for each signed position and explicit stale-quote flags. Missing marks are
unavailable, never zero, and may prevent certifying intraday DD.

Saved daily checkpoints cannot supply intraday DD, intratrade MAE or first-hit
labels. Reconstruct these from raw quotes, executions, position/cash ledgers
and model features; compare final reconstructed cash to daily net PnL before
accepting an overlay. Record worst intraday vs closing DD and stop overshoot.

Report net profit, mean/worst trade/day, max DD, time to recover, empirical
worst5% traded-day mean loss, losing-trade streaks, turnover/costs, stopped-day
coverage and false exits. Show quarter/year/month/FY trade separation and
each outer test block. Use block resampling of days for uncertainty; a
Monte Carlo historical path is a stress scenario, not a guarantee of future DD.

## 7. Current deliverable and next execution gates

`gs_stop_risk_baseline.py` saves the existing daily DD series, episodes, rolling
and period DD, stop/exit audit and chart in `output/gs_stop_risk/`. No optimized
stop, intraday series, prediction model or simulated risk improvement is claimed.

Next work is source-faithful intraday ledger reconstruction, causal execution
baseline and parity checks, training-label generation, chronological calibrated
prediction, then limited stop/regime policy replay. Before live adoption,
account currency risk budgets and forward-paper evidence are required.

Reference: [stop triggers do not guarantee fill prices](https://www.investor.gov/introduction-investing/general-resources/news-alerts/alerts-bulletins/investor-bulletins-15).
