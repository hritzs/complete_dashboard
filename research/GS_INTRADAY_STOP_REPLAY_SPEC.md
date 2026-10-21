# GS intraday reconstruction and first stop comparison — 2026-10-21

Frozen before new path/stop results. Benchmark: all1430 dates and794 selected
trades of `output/logic_comparison_20261021`, New book. Lower DD must be assessed
against the SAME-date baseline, not a fixed full-history ceiling. Preserve
LUT penalties, selected entries, original dates, event treatment, fees and NW.
No live edits, .env changes or shared-input writes.

## Scope and limitations

Instrument the immutable engine in memory, capturing its second-by-second CW
positions, mark PnL and cost path. Extract only its already-selected entry;
entry reselection and optimization are prohibited. NW is traced with its own
minute hedge execution convention and intervening underlying/option marks.
Every terminal gross/net PnL, cost, exit, hedge count/notional must reconcile
where the original record exposes it. Source file fingerprints must match.
No missing exit, trace or reconciliation failure may become zero PnL.

The existing source uses future30second option averages,3second futures
averages, some backfills and synthetic underlying hedges. Thus reconstructed
intraday DD is a SOURCE-MODEL liquidation-mark proxy, not certified executable
account DD. NW source hedge fills are at its underlying mark with no spread;
report this separately. A future causal execution baseline remains required
before predictive alpha or live stop recommendations can be claimed.

New regime signals use strictly completed underlying bars ending at least
2seconds before the current decision, never final-day classifications.
Stop trigger semantics deliberately remain legacy semantics for this matched
comparison; do not relabel them causal or pretend futures averages are real fills.

## Candidate list (no sizing change and no outcome-selected parameters)

1. Baseline unchanged latest GS.
2. CW stop0.75 times original gross stop distance.
3. CW stop0.875 times original gross stop distance.
4. CW regime exit, keeping original hard stop.
5. CW stop0.875 plus regime exit.

NW is retained unchanged in every candidate in this first comparison; its
stop/mark convention differs and must be researched separately. This is a
CW-overlay whole-book comparison, not evidence that NW risk was improved.
No model fitting, threshold optimization, changing TP, reentry or long sleeve.

CW entry, hedge path and original exits remain identical until an overlay
exits earlier. This permits an exact PREFIX replay: close the actual current
option and futures positions using the source's existing exit conventions,
book accrued hedge costs, stop all later hedge trades and retain the terminal
cash for the rest of the day. Do not clip a final daily loss or keep future
futures PnL after an early exit. Original fees are preserved, not made cheaper
because the policy had a tighter stop. No price-only approximation to GS PnL.

Stop candidates are evaluated on the source minute grid and source gross
trigger PnL. If the baseline has already exited, no later signal/action exists.
Overlay exits at the baseline exit time simply use the original terminal cash.
No second-size optimization or averaging to size1 is involved.

## Fixed underlying regime detector

Use complete one-minute bar closing U values at least2seconds old, constructed
from available past observations only. Require20minutes of observed history
with no bar close older than3seconds. No interpolation/backward filling.
Let R5=sum(last5 squared log returns), R15=sum(previous15 squared log returns)/3,
E5=abs(last close-close5bars earlier)/sum(abs(last5 close differences)).
Zero denominator/no observed movement means no direction, never an infinite E.

Directional expansion: R5>1.5*R15, E5>=0.60, and latest close outside the prior
five closes' high/low, with the sign agreeing with five-minute momentum.
Whipsaw expansion: R5>2.0*R15 and E5<=0.30.
Require the same dangerous regime (and same direction for trends) at two
consecutive decision minutes. Otherwise reset persistence. A missing or
stale input is Unavailable and cannot trigger a new overlay action.
Current PnL, future loss labels, OI and volume are not detector inputs here.
These are initial research thresholds, not optimized or certified regimes.
Warmup/confirmation delays and no-signal days must be reported.

## Reports and decision rule

Save original frozen baseline, every candidate's terminal trade/daily PnL,
entry/exit/reason, source mark equity and DD, daily closing equity/DD, costs,
MAE/MFE of its actual holding path, episode duration and period totals.
Global intraday DD includes prior sessions' intraday peaks and per-day terminal
cash. Flat/no-trade dates do not reset peaks. Include the initial equity0
before entry. The actual exit's source-model cash replaces the pre-exit mark;
preserve both trigger/decision mark and terminal cash when they differ.

Report All_dates,2022plus,2023plus,2025plus and yearly/quarterly/monthly/FY splits
for ALL five fixed candidates. Later years were already researched, so these
are retrospective blocks, not untouched forward tests. Do not select the
highest historical ratio and describe it as validated. Compare both net PnL
and global intraday/closed-day DD; identify when a lower DD comes at a large
profit cost. Candidate-specific maximum DD is recomputed from its whole path.

Persist errors and checkpoints atomically. Resume only on matching code/spec/
input hashes. Pilot early/late/event CW, DTE0/1 and NW trades before full run.
No neural prediction or calibrated SL probability is trained on this legacy
execution path. The next gate remains a causal quote/position/cash replay,
then censored first-hit targets and chronological calibrated prediction.
