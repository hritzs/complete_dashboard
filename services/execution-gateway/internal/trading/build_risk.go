package trading

import (
	"fmt"
	"strings"
	"time"
)

// BuildRiskConfig is the subset of an automated/config build's settings that
// the monitor actually honours. It is applied to the trade's MonitorConfig
// when the trade is created -- before any order is sent and before the
// monitor starts -- so an automated entry is never left unprotected.
//
// Previously the automation form's exit time, SL bps, divisors and buffers
// were accepted by the API and then silently dropped: the built trade got
// hardcoded defaults with no SL and no exit time.
type BuildRiskConfig struct {
	ExitTime    string  // HH:MM or HH:MM:SS, IST today -> SquareOffHardTime (real, verified TIME exit)
	SlBps       float64 // -> SLPnLBpsOfSpot (real, verified SL exit); 0 leaves SL unset
	TpBps       float64 // -> TPPnLBpsOfSpot (real, verified TP exit); 0 leaves TP unset
	BuyBuffer   float64
	SellBuffer  float64
	HedgeDiv    float64
	StraddleDiv float64
}

// ParseClockTodayIST parses "HH:MM" or "HH:MM:SS" as a time today in IST.
func ParseClockTodayIST(value string, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.Time{}, fmt.Errorf("load Asia/Kolkata timezone: %w", err)
	}
	value = strings.TrimSpace(value)
	var parsed time.Time
	for _, layout := range []string{"15:04:05", "15:04"} {
		if parsed, err = time.ParseInLocation(layout, value, loc); err == nil {
			break
		}
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q: expected HH:MM or HH:MM:SS", value)
	}
	now = now.In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, loc), nil
}

// Validate checks the config against the entry time, so a bad exit time is
// rejected when the build is SCHEDULED rather than after real orders are out.
func (r *BuildRiskConfig) Validate(entryAt time.Time) error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(r.ExitTime) != "" {
		exitAt, err := ParseClockTodayIST(r.ExitTime, entryAt)
		if err != nil {
			return fmt.Errorf("exit_time: %w", err)
		}
		if !exitAt.After(entryAt) {
			return fmt.Errorf("exit_time %s is not after entry_time %s",
				exitAt.Format("15:04:05"), entryAt.In(exitAt.Location()).Format("15:04:05"))
		}
	}
	for name, v := range map[string]float64{
		"sl_bps": r.SlBps, "tp_bps": r.TpBps, "buy_buffer": r.BuyBuffer, "sell_buffer": r.SellBuffer,
		"hedge_div": r.HedgeDiv, "straddle_div": r.StraddleDiv,
	} {
		if v < 0 {
			return fmt.Errorf("%s must be >= 0", name)
		}
	}
	return nil
}

// applyBuildRiskConfig writes the non-zero settings into cfg. Zero values
// leave the defaults alone (the form sends 0 for "not set").
func applyBuildRiskConfig(cfg *MonitorConfig, r *BuildRiskConfig, now time.Time) error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(r.ExitTime) != "" {
		exitAt, err := ParseClockTodayIST(r.ExitTime, now)
		if err != nil {
			return fmt.Errorf("exit_time: %w", err)
		}
		cfg.SquareOffHardTime = exitAt
	}
	if r.SlBps > 0 {
		cfg.SLPnLBpsOfSpot = r.SlBps
	}
	if r.TpBps > 0 {
		cfg.TPPnLBpsOfSpot = r.TpBps
	}
	if r.BuyBuffer > 0 {
		cfg.BuyBuffer = r.BuyBuffer
	}
	if r.SellBuffer > 0 {
		cfg.SellBuffer = r.SellBuffer
	}
	if r.HedgeDiv > 0 {
		cfg.HedgeDiv = r.HedgeDiv
	}
	if r.StraddleDiv > 0 {
		cfg.StraddleDiv = r.StraddleDiv
	}
	return nil
}

// notAppliedBuildFields lists automation-form fields the API accepts that no
// monitor implements yet, so the response can say so instead of implying they
// took effect.
func notAppliedBuildFields(req ConfigBuildRequest) []string {
	var out []string
	add := func(cond bool, name string) {
		if cond {
			out = append(out, name)
		}
	}
	add(req.Idv != 0, "idv")
	add(req.IdvDivisor != 0, "idv_divisor")
	add(req.StraddleFilter != 0, "straddle_filter")
	add(req.RollStraddleDiv != 0, "roll_straddle_div")
	add(strings.TrimSpace(req.SlStartTime) != "", "sl_start_time")
	add(strings.TrimSpace(req.HedgeStartTime) != "", "hedge_start_time")
	add(strings.TrimSpace(req.RollStartTime) != "", "roll_start_time")
	return out
}
