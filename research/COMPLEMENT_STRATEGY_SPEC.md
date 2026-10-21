# Parallel directional long-convexity study — 2026-10-21

Owner: permit trading alongside GammaShort, with a separate risk limit.
Research only: no live, .env or shared-data changes. This specification is
written before calculating the new strategies' historical PnL. Prior research
has already examined these years; they are not an untouched final holdout.

## What the data supports

Three sampled09:15–09:25 sessions (2021-01-04,2024-02-29,2026-10-13) have
600 CW timestamps each and zero subsecond rows. Time_dt dates are1900-01-01;
bind time-of-day to the trade date from the directory. Columns include top
bid/ask/quantities and IV/BidIV/AskIV, not full depth or aggressor side.
Do not call a top-quote imbalance actual signed trade flow. This is a
second/minute signal study, not an order-event or queue-execution backtest.

The existing simulate_legs helper immediately delta-neutralizes the trade and
uses legacy fills. It is unsuitable unchanged for this directional study.
Earlier short-entry percentile/delay filters did not establish profit alpha.
Those results do not establish that buying rejected shorts will be profitable.

## Fixed first screen: six paths, no historical winner selection

One independent first entry per policy per day; same1430 saved research dates.
Opening range is09:15–09:30. Scan once a minute09:31–10:30. Use only records
available before that decision. Treat a row labelled t as available t+1second,
because the source has second buckets and within-second provenance is unknown.
Require the underlying quote<=3seconds old at decision.

Select the earliest expiry observed before09:30 whose calendar expiry is at
least two days after the trade date. Use that expiry's synthetic underlying.
Do not substitute another expiry's underlying. The expiry choice is fixed all
day. This avoids buying a near-zero-time expiry-day option in the first screen.

Direction: last underlying exceeds opening high+10% of opening range, or is
below opening low-10% of that range. Require positive range, at least240 valid
seconds in the previous300seconds, and efficiency ratio>=0.30:

    ER = abs(last underlying - first underlying) / sum(abs(secondly changes))

Five-minute net direction must agree with the breakout. These fixed thresholds
are hypotheses, not optimized parameters or estimates of a profitable edge.
ATM strike is round(last known underlying/50)*50, chosen before execution.

Two entry gates: breakout alone; breakout plus purchased-IV percentile<=0.50.
The IV gate compares the mean ATM CE/PE AskIV at the decision to earlier valid
same-strike observations in the preceding300seconds (minimum60). Midrank ties
give0.5. Filter IV to decimal0.01–3.00; invalid data cannot trigger this gate.
This percentile does not by itself prove options are cheap in expected PnL.

Three structures for each gate:

1. Directional single: buy one ATM call on up signal / put on down signal.
2. Tilted straddle: up buy2calls+1put; down buy1call+2puts, same strike/expiry.
3. Balanced long straddle: buy1call+1put, a same-signal direction-neutral control.

All option quantities are positive; no futures hedge. Record entry delta,
gamma and vega, including the tilt's actual sign. Compare the directional
structures against the balanced control at the same premium budget.

## Risk, execution and exits

Use100 normalized premium-at-risk units per executed research position,
determined from entry asks, without future normalization or size boosts.
Fractional research quantities are allowed and explicitly flagged; this is
not an executable whole-lot or account-capital allocation. No account-level
risk percentage is inferred. Gross long-option loss is bounded by premium;
modeled fees add to that bound. A stop is not a guaranteed fill price.

Enter using fresh, uncrossed, positive-quantity CE/PE top quotes with source
labels at least1second after the decision; row availability is label+1second.
Limit entry attempts to5seconds after decision; no fill resumes the next
minute's scan. For multi-leg trades require matched source-second labels.
Pay the ask plus1basis point of option premium; exit at bid minus1basis point.
Do not use LTP fills, future averages, backward fills or future strike choice.
Displayed quantity is checked for positivity; fractional normalized units are
not certified executable capacity. Multi-leg fills remain a modeled atomic fill.

Exit at15minutes after entry availability, or on a30% premium-loss trigger,
whichever happens first. Evaluate liquidation at fresh bid quotes; execute
the exit on a fresh synchronized quote at least1second after the trigger.
Record overshoot and delayed exits. Never erase an entered position because
the desired exit quote is missing: unresolved exits are errors, not zero PnL.

Additional modeled round-trip fee is1 normalized unit per option leg per
standard25000/U quantity, scaled by actual entry quantities. Report net and
double-fee stress; this is a declared research drag, not a verified brokerage,
statutory-tax or exchange-fee schedule. GS LUT tax13/5/2/2/6 is unrelated and
unchanged. Existing GS results retain their legacy execution assumptions.

## Evaluation

Report every policy, trade, no-trade reason, actual entry/exit/strike/expiry,
premium, quantity, fees, greeks, gross/net, worst day, global daily DD and
quarter/year totals. Compare latest GS alone, each complement alone and the
combined daily path, with GS outcomes obtained from the separate matched
latest-logic replay. Do not compare different dates or add strategy DDs.

Report PnL on GS losing days and the worst GS decile (descriptive ex-post
diagnostics only, never entry features), correlations, premium budget and
all-calendar-day exposure. Profitable combined PnL alone does not establish
standalone alpha or equal-risk superiority. Same-strike/expiry GS offsets can
simply reduce net short risk; disclose this and compare exposure controls
before claiming diversification alpha. Additional premium budget is additional
capital; no free-hedge or equal-capital claim is permitted.

This first screen uses fixed rules, not a neural net or fitted classifier.
A later annual walk-forward logistic baseline and small neural model can use
completed historical labels only; split by whole days, purge overlapping
holding periods, freeze parameters before each scored year and include costs.
Do not randomly split millions of correlated tick rows into train/test.

Sources for structural option exposures, not evidence that the signals work:
[OIC long call](https://www.optionseducation.org/strategies/all-strategies/long-call),
[OIC long straddle](https://www.optionseducation.org/strategies/all-strategies/long-straddle),
[OIC gamma](https://www.optionseducation.org/advancedconcepts/gamma).

## Separate next short-entry hypothesis

A future GS entry test can arm the original LUT signal, wait for directional
instability to settle and then require fresh premium richness before selling.
It must replay the chronological CW/NW priority, current-time LUT, actual
entry/exit and costs. Existing saved one-minute counterfactuals cannot stand
in for that state machine. The first screen above does not claim to test it.
