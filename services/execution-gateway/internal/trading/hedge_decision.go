package trading

import "math"

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

	crossed := in.PointsOut > d.EffectiveAllowed || in.ForceRegardless
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

	lots := hedgeLotsFloor(in.NetDelta, in.LotSize)
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
