# Candidate: GS intraday regime monitor and long-option complement

Status: research design and input audit only, 2026-10-21. No alpha claim,
trade replay, fitted classifier or live hedge modification. Separate from the
frozen morning-breakout study, which failed to validate a complement.

## Objective and trigger scope

Monitor while GS is actually open, from its entry until its own exit, rather
than require a 09:30 opening-range breakout. Detect observable changes in
price path, option repricing and execution stress. A loss at the final daily
close is never available to the trigger. Current executable liquidation PnL,
current hedge history and completed past feature windows are available.

Distinguish three situations: directional expansion, nondirectional volatility
expansion/whipsaw and an ordinary temporary adverse move. A directional long
option can help the first; it can lose repeatedly in the second. Reducing
short gamma may help both, but is risk reduction rather than independent alpha.

## Data and timing

Sampled stored data has one-second labels/top quotes, without full depth or
aggressor side. Do not call inferred quote imbalance true signed order flow.
Treat source label t as available at t+1 second; decisions use observations
strictly before decision time. Freshness is field-specific: repeated OI at
each quote does not prove OI updated each second. Audit publication latency
against the upstream vendor before using it live. OI value changes alone
cannot identify every publication or its original event time.

At each decision, choose expiry and strike universe from past observations.
Keep the same contracts over each feature lookback; recentering ATM or rolling
expiry can otherwise manufacture changes. Compare expiries separately, match
DTE and time of day, filter stale/invalid/crossed quotes and avoid treating
near-zero OI as a huge percentage signal. Sample audit freezes nearest expiry
and nine closest observed strikes using only information available before09:20.
Its full-prefix cadence statistics are diagnostic, never live features.

## Candidate feature definitions (not yet selected operating thresholds)

Use completed 30-second or one-minute bars for direction, rather than reuse the
previous study's restrictive one-second ER>=0.30 gate. All windows are trailing.

1. Direction efficiency: E = abs(U_t-U_(t-L)) / sum(abs(diff(U))) on completed
   bar closes; momentum M = log(U_t/U_(t-L)). Different bar intervals give
   different distributions; no automatic transfer of old threshold0.30.
   A signed breakout compares current U with the high/low of the prior window
   excluding current observation. Record persistence, not just one touch.
2. Realized variance: RV = sum(log(U_i/U_(i-1))^2) over a trailing fixed window.
   Compare with earlier completed windows and prior-day same-clock/DTE ranks.
   Do not divide by annual IV without an explicit consistent time convention.
3. Fixed-contract straddle: V_t = (CE_bid+CE_ask)/2 + (PE_bid+PE_ask)/2.
   Premium repricing alone is not a pure IV signal because delta/gamma also
   affect price. Record separate changes in valid same-contract IV, plus
   executable short liquidation debit CE_ask+PE_ask and long liquidation
   credit CE_bid+PE_bid. The cost to exit matters more than mid-price alone.
4. Delta-adjusted option residual: sum(Delta_mid_option - delta_lag*Delta_U)
   across a fixed near-ATM basket, with Greeks known at previous observation.
   This residual still contains gamma, theta, vega and quote noise; it is
   not evidence of buyer initiation. Cross-strike agreement helps reject a
   single corrupt quote. Use IV/skew as separate explanatory features.
5. Chain breadth: fraction of valid fixed contracts with positive repricing
   or IV changes across the window; report CE and PE separately and matched
   strikes. Fixed weights prevent selection changing with future activity.
6. OI participation: delta_OI = OI_now-OI_prior; growth = delta_OI/max(OI_prior,
   prior-calibrated minimum denominator). Aggregate within expiry and side.
   Interpret with premium/IV and price, never assume call OI growth is bearish
   or put OI growth bullish. OI is secondary confirmation until cadence and
   provenance pass. It cannot reveal dealer positions/gamma sign from this feed.
7. Liquidity: relative_spread=(ask-bid)/mid; displayed imbalance=
   (bid_qty-ask_qty)/(bid_qty+ask_qty), averaged over valid trailing observations.
   These are top-of-book measures, not executions. Wide spreads can trigger
   reduction of risk while simultaneously vetoing a new expensive long entry.
8. Hedge stress, measured on the combined position: trailing sum(abs(futures
   trades)), count of hedge sign reversals and actually incurred hedge costs.
   They describe damage already incurred and must earn any predictive value
   out of sample. Separate a genuine one-way move from repeated whipsaws.

Use empirical ranks from completed past training days, stratified by time of
day/DTE; lock reference sets and coefficients before each next test block.
No ranking using the entire day, full sample or eventual day's PnL. Missing
OI must not become zero OI. Missing prices make signals unavailable, not safe.

## Proposed policy families and actions

Normal: retain latest GS and its hard delta/exit controls.

Watch: sustained adverse executable GS movement or hedge stress accompanied
by rising RV/IV/breadth; flag without trading. This isolates whether warning
has information before adding transaction costs.

Directional danger: persistent signed momentum/breakout plus independently
confirming option repricing/variance expansion. Test buying an ATM/near-ATM
call for an uptrend or put for a downtrend with the owner's separate premium
risk limit. This is long gamma/vega and signed delta. Prefer unspread long
options for the first clean experiment; a debit spread changes gamma/vega
and limits crisis payoff, so is a separate candidate. OI confirms participation
when usable; it is not a mandatory gate or directional proof.

Nondirectional danger: high RV/reversals/hedge churn without stable direction.
Test reducing GS, closing GS, or a small balanced long straddle as separate
policies. A balanced long straddle is not a directional sleeve and may be an
expensive way to offset the exact short contracts already held.

Pure liquidity stress: reduce exposure under existing execution controls;
do not assume a wide-spread long option is an attractive hedge entry.

The first comparison grid should use a small preregistered set: action=watch,
cut-half,close,add-long,cut-half-plus-long; confirmation=price-only,
price-plus-chain,price-plus-chain-plus-validated-OI. Do not treat the Cartesian
maximum as a deployable policy. Fix a low model/threshold budget, chronological
selection and an untouched next block before running. The concrete score,
cutoffs, persistence, budget, cooldown and exit parameters must be frozen in a
versioned replay specification before PnL is evaluated; this document does
not pretend those numbers are already optimized or safe for live use.

## How it can reduce hedge churn without removing delta control

Futures offset delta but do not remove short-option gamma. Buying convexity
or reducing the short options changes the cause of repeated delta drift.
Fewer hedges alone can leave a larger unhedged loss. Retain hard net-delta
limits; never disable required hedges on the strength of a regime prediction.

When a long option or GS reduction executes, recompute net option delta,
gamma and vega of BOTH books plus existing futures. A standalone GS hedge
loop must not ignore the long sleeve or automatically erase its intended
directional exposure. Test a permitted signed delta target within hard caps:
futures_target = permitted_portfolio_delta_target - total_option_delta.
Target direction is an explicit additional strategy risk, not free insurance.
Compare hard-neutral and bounded-directional target policies separately.

Gamma-neutralization sizing is a diagnostic: q_long = c*abs(Gamma_GS)/
Gamma_long, where c is a fixed partial-offset fraction. Apply a hard premium
cap and integer lot constraints; if no affordable lot exists, skip. This is
different from setting every long position to an arbitrary fixed multiplier.
Funding netting, broker execution and combined book caps need explicit replay.

## Required evaluation before claiming a complement

Replay the latest GS source, entries, original LUT penalties (including09:16),
exits and costs. Compare GS alone, reduced GS alone, long sleeve alone and
combined GS+sleeve at a common risk/capital convention. Attribute option PnL,
futures PnL and hedge costs separately; lower futures costs alone are not alpha.
Use delayed fresh ask/bid fills, conservative multi-leg execution, actual lot
sizes and sensitivity to extra spread/slippage. Missing exits are fatal.

Report closed-day AND intraday marked-to-liquidation DD, worst loss/tail loss,
hedge counts/notional/cost, maximum net delta/gamma/vega, premium capital and
margin needs, signal coverage, false alarms, warning lead time and missed
stress events. Include profitable GS days: avoid optimizing solely on losers.
Protection bought after the loss or after IV became expensive can hurt net PnL.

Chronological training and test blocks with day-level purging; evaluate the
actual eventual GS PnL only as a future training label, not an input. Compare
simple rule/logistic baselines before neural nets. The existing history has
already been researched and is not an untouched holdout. A new forward paper
period is needed. Diwali/Muhurat has its verified session calendar; do not
invent morning data or shift its timestamps.

## Initial signal recipe to preregister for replay

This is a concrete candidate, not a measured winner. Compute every minute
from completed one-minute bars. Use a five-minute price/variance window;
until that history is available, the monitor cannot trade and GS retains its
existing risk controls. Keep the hard GS stop active throughout.

- Price direction: signed five-minute momentum and a break of the preceding
  five-minute range excluding the current bar. Five-minute efficiency must
  exceed its prior-reference70th percentile.
- Expansion: five-minute RV exceeds its prior-reference80th percentile.
- Chain confirmation: same-contract basket IV change is positive and above
  its prior-reference80th percentile, with positive IV changes on at least60%
  of valid matched near-ATM CE/PE pairs. At least7 of9 fixed strike pairs
  must be usable; both sides valid at each compared endpoint, last source
  observation no more than3seconds old. Use the median across pairs so a
  corrupt deep-wing quote does not drive the signal.
- Trigger: price direction AND (expansion OR chain confirmation), agreeing
  for two consecutive completed minute decisions. It need not wait for GS
  to hit an arbitrary daily PnL loss. Log OI confirmation separately; it
  cannot override contradictory price or missing fast inputs.
- Rank references: previous60 completed sessions, matched by DTE bucket and
  clock band +/-15minutes. At least20 distinct prior sessions are required;
  insufficient reference coverage means unavailable, not a substituted
  full-history percentile. These history lengths and ranks are initial
  research choices, to be locked before replay, not empirically optimized.
- First action comparison: buy the directional ATM option targeting a25%
  offset of the magnitude of current GS negative gamma, capped by the separate
  approved premium budget and integer lots. Separately compare cutting GS
  by half and the combined action. These are distinct policies, not an
  instruction to apply all three. Reset portfolio Greeks after actual fills.
- Whipsaw case: elevated RV and hedge reversals without a persistent signed
  breakout does not authorize a directional long. Compare GS reduction
  first. A balanced long straddle remains a separate policy.

This signal recipe alone is not a complete executable backtest. Before
replay, freeze the target exposure caps, premium budget, signal rearm/cooldown,
long exit/timeout and combined-book hedge handling in the versioned execution
specification. Do not select these after looking at future PnL. In particular,
an early GS loss before the five-minute warmup cannot be prevented by this
detector, and persistence introduces a further confirmation delay.

## Observed input feasibility

Read-only audit of09:15–10:30 prefixes on the three already-fixed audit dates
and the explicitly ex-post2021-03-25 worst-GS stress date covers18 fixed
contracts per date. Median time between changes in observed OI values was
181seconds (2021-01-04),180seconds (2024-02-29),60seconds (2026-10-13), and
181seconds (2021-03-25). Roughly97–99% of consecutive OI observations were
unchanged. This is observed value-change cadence, not certified publication
latency or proof that every unchanged value was stale.

Volume decreases frequently within the same contract in each sample. Do not
assume cumulative traded volume and blindly difference it. Confirm whether
the vendor field is interval volume, trade size or another aggregation before
using a volume surge or volume-weighted feature. The current candidate does
not require Volume. The four samples contain no fractional-second timestamps.
None of these feasibility findings proves a predictive or tradable edge.

## Primary references

- [OIC: open interest has both a buyer and seller](https://www.optionseducation.org/referencelibrary/faq/general-information)
- [OIC: gamma and changing delta](https://prd-web.optionseducation.org/advancedconcepts/gamma)
- [NSE: levels of real-time market data](https://www.nseindia.com/static/market-data/real-time-data-subscription)
- [NSE F&O market-feed specification, including OI message](https://nsearchives.nseindia.com/web/sites/default/files/inline-files/Real%20time-FO-L1L2-V1.7.pdf)
