# GS position adjustments instead of stopping out — 2026-10-21

Frozen before any adjustment result. Owner request: instead of stopping out
early, adjust the total position (buy wings / other strikes, roll, option
hedges, long gamma, sell more) and see whether the TOTAL position earns more
or loses less. Owner choices: all four families; trigger = the regime signal;
adding risk allowed up to a fixed cap.

Benchmark: the frozen latest-GS book of handoff 4.22 / 4.24 (1,430 dates, 794
trades), its exact per-second source-model CW paths and its saved per-minute
regime signals (`output/gs_intraday_stops/paths`, `signals`). Same entries,
same base size 1, same base hedging, SL, TP and exit time in every candidate.
NW trades are unchanged in every candidate (CW overlays only). No live edits,
.env changes or shared-input writes.

## Trigger (fixed, from 4.24, no refit)

The FIRST confirmed regime signal after entry in the saved 4.24 signal file:
`Directional_expansion` (with its direction) or `Volatile_whipsaw`, confirmed
at two consecutive decision minutes, from completed underlying bars at least
2 s old. Decision second t* = that minute boundary. If the base trade has
already exited at or before t*, nothing happens. One adjustment per trade.

Execution: the first second s in [t*+2, t*+5] where every needed contract has a
finite quote in the day's tick file (no backfill). Missing -> no adjustment,
counted and reported, never treated as zero PnL silently. Buys pay the ask,
sells receive the bid; closing reverses (longs sell at bid, shorts buy at
ask). Strike = current ATM: round(U(s)/50)*50 from the path's underlying.

## Fixed candidates (sizes are per unit of the base straddle)

0. Baseline: unchanged latest GS.
1. Wings (iron fly): any signal. Buy 1.0 CE at ATM+2% and 1.0 PE at ATM-2%
   (rounded to the 50 grid), current-week expiry.
2. Roll / re-center: any signal. Close the whole base position at t* (the
   path's prefix cash at t*, as 4.24) and open a NEW GS trade at the ATM of
   the decision minute through the unchanged engine (same SL/TP bps, DTE row,
   event scaling, hedging and costs). Its own exits apply; size 1.
3. Option hedge, buy tested side: directional only. Buy 0.5 ATM option on the
   side of the move (CE up, PE down), current week.
4. Option hedge, sell untested side (adds risk): directional only. Sell 0.5
   ATM option on the other side (PE up, CE down), current week.
5. Long gamma calendar: any signal. Buy 0.5 next-week ATM straddle.
6. Sell more (adds risk): whipsaw only. Sell 0.5 more ATM straddle, current
   week (premium collected into the chop).

Risk cap: total short option units per side never exceed 1.5 x the base;
candidates 4 and 6 sit at the cap. Added legs (1, 3-6) are NOT delta-hedged by
the base engine; the base trade's own futures hedging continues unchanged.
Added legs are closed at the base trade's exit second e (its SL / TP / time):
the first second in [e, e+5] with a finite raw quote for every added contract;
if none, the last quote at or before e no older than 60 s (flagged as a stale
exit and counted); otherwise the day is an error, never zero. Intraday marks
of added legs use the last quote at or before each second at the liquidation
side. Candidate 2 replaces the base trade instead: base prefix cash at t* plus
the new engine trade from the decision minute row to its own exit; size and
event scaling as the base; its entry cost is the engine's own DTE-row entry
cost and hedge cost from its own hedge notional.

## Costs

Base costs unchanged. Added option legs: 0.25 normalized units per unit per
round trip (twice GS's per-leg entry cost rate, conservative), plus the real
bid/ask crossing. Also reported at double fees. Units: points x 25000 / entry
underlying x (1/3 on event days), additive with the base book.

## Reports and decision rule

All candidates on all dates and the 2022+, 2023+, 2025+ blocks and
yearly/quarterly/monthly/FY splits: net, daily-close DD, intraday DD (base
source-model mark plus the added legs marked at their liquidation side each
second), worst day, trades adjusted, adjustment PnL alone, fees, triggers by
regime, and missing-quote skips. Candidate-specific DD from its whole path.
Retrospective: these years were already researched, so no winner is selected
or called validated; nothing is adopted. A result that beats baseline on BOTH
net and DD in every block is a hypothesis for a causal quote/position/cash
replay and forward paper trading, nothing more.

Independent verification re-prices every added leg from the tick files and
re-derives each candidate's daily PnL. Pilot early/late/event/DTE0/1 trades
before the full run. Atomic checkpoints; resume only on matching input hashes.
