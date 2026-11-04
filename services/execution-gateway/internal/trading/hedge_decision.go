package trading

import (
	"fmt"
	"math"
	"sync/atomic"
)

// defaultHedgeMinThresholdBps is the reference system's default for
// hedge_min_threshold_bps.
const defaultHedgeMinThresholdBps = 8.0

// shortLegGreeks returns the POSITION Greeks of a short CE + short PE book.
// The trade record stores each leg as a short-side magnitude, so the raw
// per-unit option Greeks must be negated: a short call/put contributes the
// opposite of a long one. (The monitor used to skip this and report a short
// straddle's delta with the wrong sign, which flipped the direction of every
// hedge chosen from it. DeployStraddle already used the short-signed form.)
func shortLegGreeks(ce, pe *OptionChainRow, ceQty, peQty int) (delta, gamma, theta, vega float64) {
	cq, pq := float64(ceQty), float64(peQty)
	delta = -(ce.CEDelta*cq + pe.PEDelta*pq)
	gamma = -(ce.CEGamma*cq + pe.PEGamma*pq)
	theta = -(ce.CETheta*cq + pe.PETheta*pq)
	vega = -(ce.CEVega*cq + pe.PEVega*pq)
	return
}

// signedLegGreeksAndPNL returns one leg's contribution to the trade's
// aggregate delta/gamma/theta/vega and unrealized PnL, for a leg carried
// with a SIGNED quantity (negative = net short, positive = net long --
// trade_legs.current_quantity's own convention, see
// recomputeTradeLegFromPersistedFills). Unlike shortLegGreeks (which
// assumes both legs are short and negates unconditionally), this handles
// either direction: delta_contribution = rawDelta * signedQty already
// carries the correct sign for a short position (signedQty negative
// flips it), so no separate negation step is needed. Used to fold an
// additional leg -- e.g. a hedge at a different (live ATM) strike, which
// is part of the position's real net delta/PnL and must not be reported
// as if it didn't exist -- into the same totals as the original CE/PE
// legs (which is what shortLegGreeks/the original PnL formula already
// compute, and remain unchanged, for backward compatibility and because
// they're already covered by existing tests).
func signedLegGreeksAndPNL(rawDelta, rawGamma, rawTheta, rawVega, ltp, entryPrice float64, signedQty int64) (delta, gamma, theta, vega, pnl float64) {
	q := float64(signedQty)
	delta = rawDelta * q
	gamma = rawGamma * q
	theta = rawTheta * q
	vega = rawVega * q
	pnl = q * (ltp - entryPrice)
	return
}

// hedgeLotsFloor is the number of whole lots (of this scrip's own lot
// size) to hedge |netDelta| with: rounded DOWN (the lower lot multiple),
// so a hedge never overshoots -- the residual keeps the same sign and is
// always under one lot.
//
// Not ceiling or nearest: both can overshoot to the other side, and an
// overshoot can cross the minute-end trigger the other way so the next
// minute hedges back. Confirmed live: 2026-09-25 a 1-lot hedge on +3.35
// delta left -62.4 and was reversed a minute later (~Rs 192 of spread
// for nothing); 2026-09-28 nearest-lot hedged +180.6 with 3 lots (195),
// overshooting to about -14.
func hedgeLotsFloor(netDelta float64, lotSize int64) int64 {
	if lotSize <= 0 {
		return 0
	}
	return int64(math.Floor(math.Abs(netDelta) / float64(lotSize)))
}

// Hedge sizing (owner's rule, 2026-10-14):
//   - minute end: points_out > allowed -> hedge until points out is 25 % of
//     the (effective) points allowed, i.e. the hedge closes 75 % of the
//     allowed band. Points out = |delta / gamma| is proportional to delta,
//     so the share of the net delta hedged is 1 - 0.25 x allowed / out
//     (out 105, allowed 100, delta 105 -> hedge 80, leaving out 25);
//   - within the minute: points_out > 1.2 x allowed -> the complete delta.
//
// Lots are whole lots rounded down in both cases (hedgeLotsFloor).
const (
	hedgeLeaveBand = 0.25
	hedgeIntraMult = 1.2
	hedgeIntraFrac = 1.0
)

// hedgeRule is how a LUT table set hedges (owner, 2026-10-14): the old
// tables keep the old hedge, the new tables use the new one.
//   - old: a minute-end breach hedges the complete net delta; no
//     intra-minute hedge;
//   - new: a minute-end breach hedges until points out is 25 % of allowed;
//     intra-minute, points out > 1.2 x allowed hedges the complete delta.
//
// Live trades follow the set driving the day (lutDrivingRule); the Paper
// Sim follows the set chosen on the tab.
type hedgeRule struct {
	Key       string
	LeaveBand float64 // 0 = the complete delta at minute end
	Intra     bool
	// IntraMult: the intra-minute line, x effective allowed (0 = hedgeIntraMult).
	// IntraLeave: an intra-minute hedge stops at this share of allowed (0 = the
	// complete delta).
	IntraMult  float64
	IntraLeave float64
}

var (
	hedgeRuleOld = hedgeRule{Key: "old"}
	hedgeRuleNew = hedgeRule{Key: "new", LeaveBand: hedgeLeaveBand, Intra: true}
	// lutDrivingTables: the table set driving today (set by the LUT engine).
	lutDrivingTables atomic.Value
)

func hedgeRuleFor(tables string) hedgeRule {
	if tables == "new" {
		return hedgeRuleNew
	}
	return hedgeRuleOld
}

func lutDrivingRule() hedgeRule {
	k, _ := lutDrivingTables.Load().(string)
	return hedgeRuleFor(k)
}

// withHedgeLeavePct: hedge at minute end until points out = pct % of the
// allowed points, a Straddle Build rule or trade's own setting (owner,
// 2026-11-04: straddle / 3, IV / 38, leave 50 %; the LUT keeps its own 25 %).
// 0 = unchanged. The intra-minute line stays, on the complete delta.
func (r hedgeRule) withHedgeLeavePct(pct float64) hedgeRule {
	if pct <= 0 {
		return r
	}
	r.LeaveBand, r.Intra = math.Min(pct, 100)/100, true
	return r
}

// withIntra: a Straddle Build rule or trade's own intra-minute line and how far
// that hedge goes (owner, 2026-11-04). 0 = unchanged. The LUT keeps its own.
func (r hedgeRule) withIntra(mult, leavePct float64) hedgeRule {
	if mult > 0 {
		r.IntraMult, r.Intra = mult, true
	}
	if leavePct > 0 {
		r.IntraLeave, r.Intra = math.Min(leavePct, 100)/100, true
	}
	return r
}

// intraTrigger: points out above this x effective allowed hedges within the minute.
func (r hedgeRule) intraTrigger() float64 {
	if r.IntraMult > 0 {
		return r.IntraMult
	}
	return hedgeIntraMult
}

// intraFrac: the share of the net delta an intra-minute hedge takes.
func (r hedgeRule) intraFrac(pointsOut, effAllowed float64) float64 {
	if r.IntraLeave <= 0 {
		return hedgeIntraFrac
	}
	if pointsOut <= 0 || effAllowed <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, 1-r.IntraLeave*effAllowed/pointsOut))
}

// checkIntraSettings: the line is at least 1 x allowed, and a hedge past it must
// leave less than the line itself or it would never send anything.
func checkIntraSettings(mult, leavePct float64) error {
	if mult != 0 && mult < 1 {
		return fmt.Errorf("intra-minute trigger must be at least 1 x allowed")
	}
	if leavePct < 0 || leavePct > 100 {
		return fmt.Errorf("intra-minute leave %% must be 0 to 100")
	}
	if trigger := (hedgeRule{IntraMult: mult}).intraTrigger(); leavePct > 0 && leavePct/100 >= trigger {
		return fmt.Errorf("intra-minute leave %.4g%% must be below the trigger, %.4g x allowed", leavePct, trigger)
	}
	return nil
}

// applyTradeIntra sets an open trade's intra-minute line and hedge size (nil =
// unchanged, 0 = back to the standard). A sim-driven LUT trade refuses them.
func applyTradeIntra(tr *StoredTrade, mult, leavePct *float64) error {
	m, l := tr.Config.IntraMult, tr.Config.IntraLeavePct
	if mult != nil {
		m = *mult
	}
	if leavePct != nil {
		l = *leavePct
	}
	if err := checkIntraSettings(m, l); err != nil {
		return err
	}
	if owned, _ := simDriveOwns(tr.TradeUID); owned && (m > 0 || l > 0) {
		return fmt.Errorf("intra-minute settings do not apply to %s: its hedges come from its simulation", tr.TradeUID)
	}
	tr.Config.IntraMult, tr.Config.IntraLeavePct = m, l
	return nil
}

// applyTradeHedgeLeavePct sets an open trade's Leave % (0 = back to the
// standard rule). A sim-driven LUT trade takes its hedges from its simulation,
// so it refuses one.
func applyTradeHedgeLeavePct(tr *StoredTrade, pct float64) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("leave %% must be 0 to 100")
	}
	if owned, _ := simDriveOwns(tr.TradeUID); owned && pct > 0 {
		return fmt.Errorf("leave %% does not apply to %s: its hedges come from its simulation", tr.TradeUID)
	}
	tr.Config.HedgeLeavePct = pct
	return nil
}

// tradeHedgeRuleText says which hedging rule a trade is on, as its monitor or
// simulation applies it.
func tradeHedgeRuleText(tr StoredTrade, straddleDiv, ivDiv float64) string {
	rule := lutDrivingRule().withHedgeLeavePct(tr.Config.HedgeLeavePct).withIntra(tr.Config.IntraMult, tr.Config.IntraLeavePct)
	name := "Standard"
	if owned, _ := simDriveOwns(tr.TradeUID); owned {
		name = "LUT sim"
	} else if tr.Config.HedgeLeavePct > 0 || tr.Config.IntraMult > 0 || tr.Config.IntraLeavePct > 0 {
		name = "Own rule"
	}
	minute := "full delta"
	if rule.LeaveBand > 0 {
		minute = fmt.Sprintf("leave %.4g%% of allowed", rule.LeaveBand*100)
	}
	intra := "no intra-minute hedge"
	if rule.Intra {
		intra = fmt.Sprintf("past %.4g x allowed: full delta", rule.intraTrigger())
		if rule.IntraLeave > 0 {
			intra = fmt.Sprintf("past %.4g x allowed: leave %.4g%% of allowed", rule.intraTrigger(), rule.IntraLeave*100)
		}
	}
	return fmt.Sprintf("%s: min(straddle / %.4g, IV / %.4g), minute end %s, %s", name, straddleDiv, ivDiv, minute, intra)
}

// minuteFrac: the share of the net delta a minute-end hedge takes.
func (r hedgeRule) minuteFrac(pointsOut, effAllowed float64) float64 {
	if r.LeaveBand <= 0 {
		return 1
	}
	if pointsOut <= 0 || effAllowed <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, 1-r.LeaveBand*effAllowed/pointsOut))
}

// hedgeMinuteFracFor is the share of the net delta a minute-end hedge takes
// so that points out falls to hedgeLeaveBand x effective allowed (0 when
// it is already there).
func hedgeMinuteFracFor(pointsOut, effAllowed float64) float64 {
	if pointsOut <= 0 || effAllowed <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, 1-hedgeLeaveBand*effAllowed/pointsOut))
}

func resolveHedgeMinThresholdBps(cfg *float64) float64 {
	if cfg == nil {
		return defaultHedgeMinThresholdBps
	}
	return *cfg
}

type hedgeDecisionInput struct {
	PointsOut     float64
	PointsAllowed float64
	Spot          float64
	NetDelta      float64
	LotSize       int64
	TradeLots     int64

	MinThresholdBps float64

	// Frac: share of the net delta hedged (hedgeIntraFrac within the
	// minute). 0 = all of it. LeaveBand (minute end) overrides it: hedge
	// until points out = LeaveBand x effective allowed.
	Frac      float64
	LeaveBand float64
	// Mult: the trigger is points_out > Mult x effective_allowed (0 = 1).
	Mult float64

	// Test overrides (the forced hedge test).
	ForceOneLotTest bool
	TestPointsFloor float64
	ForceRegardless bool
}

type hedgeDecision struct {
	Hedge            bool
	Lots             int64
	Action           string
	EffectiveAllowed float64
	Floor            float64
}

// decideHedge is the minute-end hedge rule, following the reference
// system's HedgeMonitor:
//
//	effective_allowed = max(points_allowed, spot * min_threshold_bps / 10000)
//	hedge when points_out > effective_allowed
//
// The floor is bps of LIVE spot rather than a frozen constant (the code had
// hardcoded 19.5 points, i.e. 8 bps of a ~24,400 spot). Sizing then hedges
// hedgeLotsFloor(|net delta|) lots -- see its doc comment for why it
// rounds down -- capped at the trade's own lot count.
func decideHedge(in hedgeDecisionInput) hedgeDecision {
	d := hedgeDecision{Action: "OK"}

	if in.Spot > 0 && in.MinThresholdBps > 0 {
		d.Floor = in.Spot * in.MinThresholdBps / 10000.0
	}
	d.EffectiveAllowed = math.Max(in.PointsAllowed, d.Floor)

	if in.PointsAllowed <= 0 && !in.ForceRegardless {
		d.Action = "NO_ALLOWANCE_AVAILABLE"
		return d
	}

	mult := in.Mult
	if mult <= 0 {
		mult = 1
	}
	crossed := in.PointsOut > mult*d.EffectiveAllowed || in.ForceRegardless
	if !crossed {
		if in.PointsOut > in.PointsAllowed {
			d.Action = "BELOW_MIN_THRESHOLD"
		}
		return d
	}

	if in.ForceOneLotTest && in.PointsOut < in.TestPointsFloor {
		d.Action = "BELOW_TEST_FLOOR"
		return d
	}

	if in.LotSize <= 0 {
		d.Action = "NO_LOT_SIZE"
		return d
	}

	frac := in.Frac
	if frac <= 0 || frac > 1 {
		frac = 1
	}
	if in.LeaveBand > 0 {
		frac = math.Min(1, math.Max(0, 1-in.LeaveBand*d.EffectiveAllowed/in.PointsOut))
	}
	lots := hedgeLotsFloor(frac*in.NetDelta, in.LotSize)
	if in.ForceOneLotTest && lots == 0 {
		lots = 1
	}
	if lots == 0 {
		d.Action = "DELTA_BELOW_ONE_LOT"
		return d
	}
	if in.TradeLots > 0 && lots > in.TradeLots {
		lots = in.TradeLots
	}

	d.Hedge = true
	d.Lots = lots
	d.Action = "HEDGE_TRIGGERED"
	return d
}
