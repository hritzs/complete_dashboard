# Latest GammaShort versus research baseline — 2026-10-21

User-confirmed scope: latest GammaShort entry, hedging and exits, not a new
sizing search. Snapshot `7DResume_0.75_1.2.py` SHA256
`d4a69c4dfe1c2a99066725495ed1247806fe081a64cb02be99e6328dde92bb9b`
is the latest active shared implementation at inspection. Shared filenames
changed during inspection; replay only the immutable local copies.

Compare three books at flat size1 on the same1430 frozen research dates
(2021-01-01 through2026-10-14, excluding the original missing/skipped dates):

1. Archived research results, retained unchanged as a vintage reference.
2. Archived code freshly replayed on snapshotted current daily/event inputs
   and the latest production tables.
3. Latest snapshotted code on the exact same inputs and tables as2.

Thus3 versus2 isolates implementation differences;2 versus1 also captures
input/table vintage differences. All five CW tables at the two original
locations are byte-identical; NW tables currently differ (old location24 YES,
latest location23 YES). Use the frozen **latest23-cell NW table in both fresh
books**, do not silently compare different table versions or date ranges.

Replay the versions' actual entry/hedge/exit calculations. Keep source-version
grid differences visible rather than hide them: old EOD grid switch2026-06-03,
new2026-08-03; old entry-anchored/forward minute marks versus new clock/backward
minute marks; DTE1/2 minute-start residual flips side in old code versus same
side in new; old daily ADJ uses Final_IV_Chg when present versus new recomputes
the gap/non-gap selection. Record any other source differences and costs.

No live code, .env or shared files are changed. Disable only unused diagnostic
outputs/fixed-entry alternatives for performance. New source's extractor may
prune cells that its fixed LUT rejects and stop after the first successful CW
YES; the original main sweep only needs that first CW hit plus its independent
NW check. Verify pruned versus unpruned selected trades on representative
early-CW/late-CW/NW days before the full run. Do not prune or alter entry data,
hedge maths, marks, exits, costs or the NW priority rule.

Both fresh engines receive the same in-memory option rows of the two nearest
expiries, preserving row order and source columns. Verify native-loader parity
on pilot days. Persist per-date paired results, source tick fingerprints,
errors and timing; checkpoint/resume only with matching code/input hashes.
Treat unexpected exceptions as errors, not zero-PnL/no-trade observations.

Report full/matched-entry/changed-entry/no-trade transitions, exact dates,
CW/NW attribution, entry and hedge costs, worst day, net and full-path maximum
DD, quarter/year/month/FY totals, equity/DD charts and complete daily/trade
Excel comparisons. Do not add year-level DDs or compare different windows.

Earlier sizing/model results were fitted on old outcomes. They do not become
validated on this new engine automatically. First establish the matched flat
baseline. Any policy transfer or refit needs explicit labelling and separate
dated evaluation; do not reuse old PnL scaling as a new-logic replay.

This is a version comparison, not a repair/certification of every source
availability/fill assumption. The source engines retain their own backfills,
30-second option and3-second futures execution assumptions and fixed LUTs.
