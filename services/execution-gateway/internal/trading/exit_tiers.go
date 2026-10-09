package trading

// Tiered exits: besides its main rule (MTMExitLevel / StraddleExitBelow) a
// trade may carry extra steps, e.g. MTM >= 1,00,000 -> 15%, >= 1,50,000 ->
// another 15%, or ATM straddle < 220 -> 25%, < 200 -> 25%. Every step is
// its own rule:
//
//   - its % is of the ORIGINAL position (not of what is left), closed once;
//     it remembers what it closed (ClosedQty) so it never repeats;
//   - MTM steps run lowest level first, each lot priced so the trade's
//     executable MTM stays >= THAT step's level (mtm_exit.go);
//     ATM-straddle steps run highest level first (the straddle decays);
//   - steps never close more than is open: each one's share is capped by
//     the open straddle (ruleRemaining), one exit runs per trade at a time
//     (runExitAsync + the trade lock), and every order passes the close
//     guard (close_guard.go). So all rules together -- steps, the main
//     rules, PSQF, SL / TP / exit time -- can never square off more than the
//     trade's position.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ExitTier is one extra step of a rule.
type ExitTier struct {
	Level     float64 `json:"level"`
	Pct       float64 `json:"pct"`                  // % of the ORIGINAL position (0 / 100 = all that is open)
	ClosedQty int64   `json:"closed_qty,omitempty"` // straddle contracts this step closed
}

// exitRule is one rule of a kind: the main one (idx -1) or a step.
type exitRule struct {
	idx    int
	level  float64
	pct    float64
	closed int64
}

func (r exitRule) name() string {
	if r.idx < 0 {
		return "main rule"
	}
	return fmt.Sprintf("step at %g", r.level)
}

// mtmRules: the MTM main rule and steps, lowest level first.
func mtmRules(c MonitorConfig) []exitRule {
	var out []exitRule
	if c.MTMExitLevel != nil {
		out = append(out, exitRule{-1, *c.MTMExitLevel, c.MTMExitPct, c.MTMExitClosedQty})
	}
	for i, t := range c.MTMExitTiers {
		out = append(out, exitRule{i, t.Level, t.Pct, t.ClosedQty})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].level < out[b].level })
	return out
}

// straddleRules: the ATM-straddle main rule and steps, highest level first.
func straddleRules(c MonitorConfig) []exitRule {
	var out []exitRule
	if c.StraddleExitBelow != nil {
		out = append(out, exitRule{-1, *c.StraddleExitBelow, c.StraddleExitPct, c.StraddleExitClosedQty})
	}
	for i, t := range c.StraddleExitTiers {
		out = append(out, exitRule{i, t.Level, t.Pct, t.ClosedQty})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].level > out[b].level })
	return out
}

// mtmRuleNow re-reads rule idx from the (fresh) config.
func mtmRuleNow(c MonitorConfig, idx int) (exitRule, bool) {
	for _, r := range mtmRules(c) {
		if r.idx == idx {
			return r, true
		}
	}
	return exitRule{}, false
}

// addMTMClosed / addStraddleClosed record what a rule closed.
func addMTMClosed(c *MonitorConfig, idx int, q int64) {
	if idx < 0 {
		c.MTMExitClosedQty += q
	} else if idx < len(c.MTMExitTiers) {
		c.MTMExitTiers[idx].ClosedQty += q
	}
}

func addStraddleClosed(c *MonitorConfig, idx int, q int64) {
	if idx < 0 {
		c.StraddleExitClosedQty += q
	} else if idx < len(c.StraddleExitTiers) {
		c.StraddleExitTiers[idx].ClosedQty += q
	}
}

// mergeTiers takes the steps a Modify sends; a step whose level and % are
// unchanged keeps what it already closed (so it never fires twice).
func mergeTiers(old, req []ExitTier) []ExitTier {
	out := make([]ExitTier, 0, len(req))
	used := make([]bool, len(old))
	for _, t := range req {
		n := ExitTier{Level: t.Level, Pct: t.Pct}
		for i, o := range old {
			if !used[i] && o.Level == t.Level && o.Pct == t.Pct {
				n.ClosedQty, used[i] = o.ClosedQty, true
				break
			}
		}
		out = append(out, n)
	}
	return out
}

// validTiers checks steps from a Modify (positive: straddle levels > 0).
func validTiers(ts []ExitTier, positive bool, name string) error {
	if len(ts) > 20 {
		return fmt.Errorf("%s: at most 20 steps", name)
	}
	for i, t := range ts {
		if math.IsNaN(t.Level) || math.IsInf(t.Level, 0) || (positive && t.Level <= 0) {
			return fmt.Errorf("%s step %d: invalid level %v", name, i+1, t.Level)
		}
		if math.IsNaN(t.Pct) || t.Pct < 0 || t.Pct > 100 {
			return fmt.Errorf("%s step %d: %% must be 0-100", name, i+1)
		}
	}
	return nil
}

// tiersLabel shows steps for the config diff ("-" when none).
func tiersLabel(ts []ExitTier) string {
	if len(ts) == 0 {
		return "-"
	}
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = fmt.Sprintf("%g · %.0f%%", t.Level, ruleShare(t.Pct))
	}
	return strings.Join(parts, ", ")
}
