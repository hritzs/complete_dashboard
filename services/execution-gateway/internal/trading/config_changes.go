package trading

import (
	"fmt"
	"time"
)

// ConfigChange is one recorded modification of a trade's settings. Kept in
// MonitorConfig (so it is persisted with the trade) and shown on the card.
type ConfigChange struct {
	Time  string `json:"time"` // HH:MM:SS IST
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func cfgNum(v float64) string {
	if v == 0 {
		return "off"
	}
	return fmt.Sprintf("%g", v)
}

func cfgClock(t time.Time) string {
	if t.IsZero() || t.Year() < 2000 {
		return "not set"
	}
	return t.In(lutIST()).Format("15:04:05")
}

func cfgBool(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func cfgPtr(p *float64) string {
	if p == nil {
		return "default"
	}
	return fmt.Sprintf("%g", *p)
}

// diffMonitorConfig lists every user-facing setting that differs.
func diffMonitorConfig(a, b MonitorConfig) []ConfigChange {
	type f struct{ name, from, to string }
	fields := []f{
		{"SL (bps of spot)", cfgNum(a.SLPnLBpsOfSpot), cfgNum(b.SLPnLBpsOfSpot)},
		{"TP (bps of spot)", cfgNum(a.TPPnLBpsOfSpot), cfgNum(b.TPPnLBpsOfSpot)},
		{"Exit time", cfgClock(a.SquareOffHardTime), cfgClock(b.SquareOffHardTime)},
		{"Straddle divisor", cfgNum(a.StraddleDiv), cfgNum(b.StraddleDiv)},
		{"Hedge divisor", cfgNum(a.HedgeDiv), cfgNum(b.HedgeDiv)},
		{"Hedge min (bps)", cfgPtr(a.HedgeMinThresholdBps), cfgPtr(b.HedgeMinThresholdBps)},
		{"Hedge delta threshold", cfgNum(a.HedgeThresholdDelta), cfgNum(b.HedgeThresholdDelta)},
		{"Hedge points floor", cfgNum(a.HedgePointsFloor), cfgNum(b.HedgePointsFloor)},
		{"SL points / lot", cfgNum(a.SLPointsPerLot), cfgNum(b.SLPointsPerLot)},
		{"Spot SL (bps)", cfgNum(a.SpotStopLossBps), cfgNum(b.SpotStopLossBps)},
		{"TP points / straddle", cfgNum(a.TakeProfitPointsPerStraddle), cfgNum(b.TakeProfitPointsPerStraddle)},
		{"Force 1-lot hedge test", cfgBool(a.ForceOneLotHedgeTest), cfgBool(b.ForceOneLotHedgeTest)},
		{"Hedge regardless of points", cfgBool(a.ForceHedgeRegardlessOfPoints), cfgBool(b.ForceHedgeRegardlessOfPoints)},
	}
	now := time.Now().In(lutIST()).Format("15:04:05")
	var out []ConfigChange
	for _, x := range fields {
		if x.from != x.to {
			out = append(out, ConfigChange{Time: now, Field: x.name, From: x.from, To: x.to})
		}
	}
	return out
}
