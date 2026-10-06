package trading

// Entry-at-straddle authorizer (stage 1 of the depth BUILD design).
//
// Pure decision logic: given fresh L1-L5 bid books for the ATM CE and PE,
// their deltas, the entry straddle target and the verified position (from
// PMS), it decides the next SELL tranche:
//
//	fillable depth (levels that keep the pair above target)
//	  -> 50% participation -> lot floor              = per-leg capacity
//	  -> smaller capacity = anchor
//	  -> partner leg sized so the position AFTER this tranche is delta
//	     neutral (corrects any imbalance left by earlier partial fills)
//	  -> both legs >= 1 lot (never one-sided)
//	  -> fit in the remaining quantity (anchor steps down a lot at a time)
//	  -> ONE weighted-price test: VWAP_CE + VWAP_PE > target
//
// It never places orders. The OMS executes an authorized tranche; PMS
// records what really filled; the next tranche is planned from that.

import (
	"fmt"
	"math"
	"strings"
)

// DepthEntryInput is one cycle's inputs (all from the same snapshot).
type DepthEntryInput struct {
	CEBids         []DepthLevel // best first
	PEBids         []DepthLevel
	CEDelta        float64 // call delta (positive)
	PEDelta        float64 // put delta (negative)
	TargetStraddle float64 // sell only if the pair's weighted price is ABOVE this
	LotSize        int64
	Participation  float64 // e.g. 0.5
	MaxLevels      int     // e.g. 5
	RemainingQty   int64   // combined CE+PE still to build = 2 x straddles - (CE filled + PE filled)
	PositionDelta  float64 // verified current option delta (signed units)
}

// DepthEntryPlan is the decision plus every intermediate value.
type DepthEntryPlan struct {
	Authorized bool   `json:"authorized"`
	Reason     string `json:"reason"`

	LTPGate      float64 `json:"best_bid_straddle"` // CE best bid + PE best bid
	CEFillable   int64   `json:"ce_fillable"`
	PEFillable   int64   `json:"pe_fillable"`
	CELevelsUsed int     `json:"ce_levels_fillable"`
	PELevelsUsed int     `json:"pe_levels_fillable"`
	CEPart       float64 `json:"ce_participation"`
	PEPart       float64 `json:"pe_participation"`
	CECap        int64   `json:"ce_cap"`
	PECap        int64   `json:"pe_cap"`
	Anchor       string  `json:"anchor"`
	Tail         bool    `json:"tail"` // final <= 2 lots, sized to cut net delta

	CEQty         int64   `json:"ce_qty"`
	PEQty         int64   `json:"pe_qty"`
	TrancheDelta  float64 `json:"tranche_delta"`
	ResidualDelta float64 `json:"residual_delta"` // position delta after the tranche fills

	CEVWAP   float64 `json:"ce_vwap"`
	PEVWAP   float64 `json:"pe_vwap"`
	Weighted float64 `json:"weighted_straddle"`
	CEWorst  float64 `json:"ce_worst_price"` // lowest bid level reached = sell limit floor
	PEWorst  float64 `json:"pe_worst_price"`

	Steps []string `json:"steps"`
}

// fillableAgainst sums the quantity of the levels of one leg that keep the
// pair above target when combined with the other leg's best bid.
func fillableAgainst(levels []DepthLevel, otherBest, target float64, maxLevels int) (int64, int) {
	var qty int64
	used := 0
	for i := 0; i < len(levels) && i < maxLevels; i++ {
		l := levels[i]
		if l.Price <= 0 || l.Qty <= 0 || l.Price+otherBest <= target {
			break // book is price-ordered: deeper levels are worse
		}
		qty += l.Qty
		used = i + 1
	}
	return qty, used
}

// walkBids returns the VWAP and the worst price reached selling qty into
// the bid levels (ok=false if the levels don't hold qty).
func walkBids(levels []DepthLevel, qty int64, maxLevels int) (vwap, worst float64, ok bool) {
	remaining, value := qty, 0.0
	for i := 0; i < len(levels) && i < maxLevels && remaining > 0; i++ {
		l := levels[i]
		if l.Price <= 0 || l.Qty <= 0 {
			break
		}
		take := l.Qty
		if take > remaining {
			take = remaining
		}
		value += float64(take) * l.Price
		remaining -= take
		worst = l.Price
	}
	if remaining > 0 || qty <= 0 {
		return 0, 0, false
	}
	return value / float64(qty), worst, true
}

func floorLots(q float64, lot int64) int64 {
	if q <= 0 || lot <= 0 {
		return 0
	}
	return int64(math.Floor(q/float64(lot)+1e-9)) * lot
}

// PlanDepthEntry runs one cycle of the authorizer.
func PlanDepthEntry(in DepthEntryInput) DepthEntryPlan {
	p := DepthEntryPlan{}
	step := func(format string, a ...interface{}) { p.Steps = append(p.Steps, fmt.Sprintf(format, a...)) }
	fail := func(reason string) DepthEntryPlan {
		p.Authorized, p.Reason = false, reason
		step("RESULT: NO TRANCHE -- %s", reason)
		return p
	}

	if in.MaxLevels <= 0 {
		in.MaxLevels = 5
	}
	if in.Participation <= 0 || in.Participation > 1 {
		in.Participation = 0.5
	}
	if in.LotSize <= 0 {
		return fail("lot size unknown")
	}
	if in.TargetStraddle <= 0 {
		return fail("no entry straddle target")
	}
	if in.RemainingQty < in.LotSize {
		return fail(fmt.Sprintf("build complete: remaining %d is below one lot", in.RemainingQty))
	}
	if len(in.CEBids) == 0 || len(in.PEBids) == 0 || in.CEBids[0].Price <= 0 || in.PEBids[0].Price <= 0 {
		return fail("no CE/PE bid depth")
	}
	dCE, dPE := math.Abs(in.CEDelta), math.Abs(in.PEDelta)
	if dCE <= 0 || dPE <= 0 {
		return fail("missing CE/PE delta")
	}

	// 1. Gate on the best bids.
	p.LTPGate = in.CEBids[0].Price + in.PEBids[0].Price
	step("[GATE] best bids CE %.2f + PE %.2f = %.2f vs target %.2f", in.CEBids[0].Price, in.PEBids[0].Price, p.LTPGate, in.TargetStraddle)
	if p.LTPGate <= in.TargetStraddle {
		return fail(fmt.Sprintf("best-bid straddle %.2f not above target %.2f", p.LTPGate, in.TargetStraddle))
	}

	// 2. Fillable depth that keeps the pair above target.
	p.CEFillable, p.CELevelsUsed = fillableAgainst(in.CEBids, in.PEBids[0].Price, in.TargetStraddle, in.MaxLevels)
	p.PEFillable, p.PELevelsUsed = fillableAgainst(in.PEBids, in.CEBids[0].Price, in.TargetStraddle, in.MaxLevels)
	step("[DEPTH FILLABLE] CE %d (L1-L%d)  PE %d (L1-L%d)", p.CEFillable, p.CELevelsUsed, p.PEFillable, p.PELevelsUsed)

	// 3. 50% participation, lot floor.
	p.CEPart, p.PEPart = float64(p.CEFillable)*in.Participation, float64(p.PEFillable)*in.Participation
	p.CECap, p.PECap = floorLots(p.CEPart, in.LotSize), floorLots(p.PEPart, in.LotSize)
	step("[%.0f%% PARTICIPATION] CE %.1f  PE %.1f -> [LOT FLOOR] CE cap %d  PE cap %d", in.Participation*100, p.CEPart, p.PEPart, p.CECap, p.PECap)
	// Tail: the last <= 2 lots go on whichever legs cut the whole trade's
	// net delta the most (one leg alone allowed only here).
	if in.RemainingQty <= 2*in.LotSize {
		return planTail(in, p)
	}

	if p.CECap < in.LotSize || p.PECap < in.LotSize {
		return fail("less than one lot of capacity on a leg")
	}

	// 4. Anchor = smaller capacity; partner makes the position delta neutral.
	//    Short CE adds -dCE per unit, short PE adds +dPE per unit.
	anchorCE := p.CECap < p.PECap
	p.Anchor = "PE"
	anchorCap := p.PECap
	if anchorCE {
		p.Anchor, anchorCap = "CE", p.CECap
	}
	step("[ANCHOR] %s (cap %d); position delta before %.2f", p.Anchor, anchorCap, in.PositionDelta)

	for a := anchorCap; a >= in.LotSize; a -= in.LotSize {
		var qCE, qPE int64
		if anchorCE {
			qCE = a
			qPE = floorLots((dCE*float64(qCE)-in.PositionDelta)/dPE, in.LotSize)
		} else {
			qPE = a
			qCE = floorLots((in.PositionDelta+dPE*float64(qPE))/dCE, in.LotSize)
		}
		if qCE < in.LotSize || qPE < in.LotSize {
			if a == anchorCap {
				step("[DELTA NEUTRAL] partner rounds below one lot (CE %d / PE %d) -- not one-sided", qCE, qPE)
			}
			continue
		}
		if qCE > p.CECap || qPE > p.PECap {
			continue
		}
		if qCE+qPE > in.RemainingQty {
			continue
		}
		p.CEQty, p.PEQty = qCE, qPE
		break
	}
	if p.CEQty == 0 || p.PEQty == 0 {
		return fail("no two-sided delta-neutral pair fits capacity and remaining quantity")
	}
	p.TrancheDelta = -dCE*float64(p.CEQty) + dPE*float64(p.PEQty)
	p.ResidualDelta = in.PositionDelta + p.TrancheDelta
	step("[DELTA NEUTRAL] CE %d (%.3f) + PE %d (%.3f): tranche delta %+.2f, position after %+.2f; remaining %d -> %d",
		p.CEQty, dCE, p.PEQty, dPE, p.TrancheDelta, p.ResidualDelta, in.RemainingQty, in.RemainingQty-p.CEQty-p.PEQty)

	// 5. ONE weighted-price test on this exact pair.
	var ok1, ok2 bool
	p.CEVWAP, p.CEWorst, ok1 = walkBids(in.CEBids, p.CEQty, in.MaxLevels)
	p.PEVWAP, p.PEWorst, ok2 = walkBids(in.PEBids, p.PEQty, in.MaxLevels)
	if !ok1 || !ok2 {
		return fail("book does not hold the pair quantity")
	}
	p.Weighted = p.CEVWAP + p.PEVWAP
	step("[PRICE TEST] CE VWAP %.2f (worst %.2f) + PE VWAP %.2f (worst %.2f) = %.2f vs target %.2f",
		p.CEVWAP, p.CEWorst, p.PEVWAP, p.PEWorst, p.Weighted, in.TargetStraddle)
	if p.Weighted <= in.TargetStraddle {
		return fail(fmt.Sprintf("weighted straddle %.2f not above target %.2f -- wait for next tick", p.Weighted, in.TargetStraddle))
	}

	p.Authorized = true
	p.Reason = "authorized"
	step("[AUTH] SELL CE %d (limit floor %.2f) + SELL PE %d (limit floor %.2f)", p.CEQty, p.CEWorst, p.PEQty, p.PEWorst)
	return p
}

// String renders the plan's steps on one block (for logs).
func (p DepthEntryPlan) String() string { return strings.Join(p.Steps, "\n") }

// TrancheLotSequence is the order in which a tranche's lots are sent: one
// lot per order, CE and PE interleaved, with the larger leg's extra lots
// spread evenly through the sequence (not bunched at the end) so the
// position stays close to delta-neutral while the tranche is executing.
// E.g. 6 CE + 7 PE -> PE CE PE CE PE CE PE CE PE CE PE CE PE.
func TrancheLotSequence(ceLots, peLots int) []string {
	total := ceLots + peLots
	out := make([]string, 0, total)
	ce, pe := 0, 0
	for len(out) < total {
		// Send whichever leg is furthest behind its share of the tranche.
		ceBehind := float64(ce+1)/float64(maxInt(ceLots, 1)) <= float64(pe+1)/float64(maxInt(peLots, 1))
		switch {
		case ce >= ceLots:
			out, pe = append(out, "PE"), pe+1
		case pe >= peLots:
			out, ce = append(out, "CE"), ce+1
		case ceBehind && ceLots >= peLots:
			out, ce = append(out, "CE"), ce+1
		case !ceBehind && peLots > ceLots:
			out, pe = append(out, "PE"), pe+1
		case ceBehind:
			out, ce = append(out, "CE"), ce+1
		default:
			out, pe = append(out, "PE"), pe+1
		}
	}
	return out
}

// planTail sizes the final <= 2 lots to minimise |net delta|.
func planTail(in DepthEntryInput, p DepthEntryPlan) DepthEntryPlan {
	step := func(format string, a ...interface{}) { p.Steps = append(p.Steps, fmt.Sprintf(format, a...)) }
	fail := func(reason string) DepthEntryPlan {
		p.Authorized, p.Reason = false, reason
		step("RESULT: NO TRANCHE -- %s", reason)
		return p
	}
	p.Tail = true
	lots := int(in.RemainingQty / in.LotSize)
	dCE, dPE := math.Abs(in.CEDelta), math.Abs(in.PEDelta)
	type cand struct {
		ce, pe int64
		after  float64
	}
	var cands []cand
	for ce := 0; ce <= lots; ce++ {
		q := cand{ce: int64(ce) * in.LotSize, pe: int64(lots-ce) * in.LotSize}
		q.after = in.PositionDelta - dCE*float64(q.ce) + dPE*float64(q.pe)
		cands = append(cands, q)
	}
	// Best delta first.
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && math.Abs(cands[j].after) < math.Abs(cands[j-1].after); j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	step("[TAIL] %d lot(s) left; net delta %+.2f; options by resulting delta:", lots, in.PositionDelta)
	for _, c := range cands {
		step("[TAIL]   CE %d + PE %d -> delta %+.2f", c.ce, c.pe, c.after)
	}
	for _, c := range cands {
		if c.ce > p.CECap || c.pe > p.PECap {
			step("[TAIL] CE %d + PE %d: exceeds 50%% capacity (CE %d / PE %d)", c.ce, c.pe, p.CECap, p.PECap)
			continue
		}
		var ok bool
		p.CEVWAP, p.CEWorst, p.PEVWAP, p.PEWorst = 0, 0, 0, 0
		if c.ce > 0 {
			if p.CEVWAP, p.CEWorst, ok = walkBids(in.CEBids, c.ce, in.MaxLevels); !ok || p.CEVWAP+in.PEBids[0].Price <= in.TargetStraddle {
				step("[TAIL] CE %d: CE VWAP %.2f + PE best %.2f not above target", c.ce, p.CEVWAP, in.PEBids[0].Price)
				continue
			}
		}
		if c.pe > 0 {
			if p.PEVWAP, p.PEWorst, ok = walkBids(in.PEBids, c.pe, in.MaxLevels); !ok || p.PEVWAP+in.CEBids[0].Price <= in.TargetStraddle {
				step("[TAIL] PE %d: PE VWAP %.2f + CE best %.2f not above target", c.pe, p.PEVWAP, in.CEBids[0].Price)
				continue
			}
		}
		p.CEQty, p.PEQty = c.ce, c.pe
		p.TrancheDelta = c.after - in.PositionDelta
		p.ResidualDelta = c.after
		p.Weighted = p.CEVWAP + p.PEVWAP
		p.Authorized, p.Reason = true, "authorized (tail)"
		step("[AUTH TAIL] SELL CE %d (floor %.2f) + SELL PE %d (floor %.2f): net delta %+.2f -> %+.2f; build then complete",
			c.ce, p.CEWorst, c.pe, p.PEWorst, in.PositionDelta, c.after)
		return p
	}
	return fail("tail: no delta-reducing option is fillable above target this tick")
}
