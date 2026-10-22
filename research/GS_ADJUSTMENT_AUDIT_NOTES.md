# Position-adjustment study: accounting and execution limits — 2026-10-22

Iteration22 is a saved retrospective experiment, not a recommended live policy.
Its frozen replay source/specification, trade checkpoints and mark arrays are
preserved. Pre-audit report artifacts are retained under the output provenance
folder; report annotations do not change PnL. Independent
verification checks raw quote prices and arithmetic; PASS does not certify
causal order execution or reduced future risk.

The specification carries legacy base/roll fills from iteration21. Additional
issues need a new, separately versioned causal replay before any adoption:

- The code fixes added-leg strikes using the underlying at the first path
  second at least2seconds after the trigger. Its ATM metadata is calculated
  again at the actual fill second. Those two selections can differ when a
  strike boundary is crossed. A live order requires a strike chosen using
  information available BEFORE sending the order. Independent verification
  must flag any disagreement rather than quietly change a stored trade.
- Raw added-leg quotes are filtered for positive bid/ask, without a crossed-
  quote or displayed-quantity check. No independent exchange/vendor latency
  or source timestamp availability has been established. Quoted prices are
  assumptions about fills, not evidence that these quantities could trade.
- Five exits use a recorded stale-quote fallback of up to60seconds (three
  Wings and two NWLongStraddle cases). That is a marked-value convention,
  not proof of an executable exit. Fresh exits may occur up to5seconds after
  base exit; the added-leg terminal cash is nevertheless posted at base exit
  in the stored path. Consequently the path is a source-model DD proxy.
- Rolling closes/reopens through the legacy engine at the trigger time,
  without the added-leg delay. Native future-average/backfill assumptions
  remain. Verification of roll prefix+new-trade cash is arithmetic parity,
  not an independent rerun under causal fills.
- Rolling also starts a fresh trade stop after realizing the old trade's
  PnL. A one-unit rolled position does not enforce a day-level loss limit.
  Any future live design must carry realized losses into the remaining daily
  risk budget rather than reset protection with every new position.
- Base futures hedging does not respond to the added option legs. A1.5x cap
  on short option units per side is not a cap on combined delta/gamma/vega,
  margin usage or maximum currency loss. Some alternatives deliberately add
  risk, so higher profit is not evidence of better risk control.
- Wings are selected around the ATM at adjustment, while the original short
  straddle retains its entry strike. This need not form a centered iron fly.
- The double-fee sensitivity doubles ONLY the additional option-leg model
  fee. It does not double all GS costs or stress roll execution costs.

Against unchanged GS, no saved adjustment improves BOTH profit and daily DD
over the complete history; none meets the owner's lower-DD objective while
also improving intraday proxy DD and preserving profit. Added selling raises
profit but also increases DD. Do not train/choose a live risk model from a
historical maximum in this table. The next gate is a causal quote/position/cash
baseline followed by past-only, censored SL-before-recovery labels and later-
period validation. Existing GS remains the benchmark; no live rule is changed.
