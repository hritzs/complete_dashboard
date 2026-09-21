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
// floor(|net delta| / lot size) lots -- what a lot-granular synthetic can
// actually neutralize -- capped at the trade's own lot count. Below one lot
// of delta no hedge can improve the position (it would overshoot), so it
// is skipped rather than forced.
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

	lots := int64(math.Abs(in.NetDelta) / float64(in.LotSize))
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
