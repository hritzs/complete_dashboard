package trading

// Manual what-if inputs for the LUT entry check, and the "sell zone" scan.
// Every manual field overrides one live value (nil = use live); clearing
// them all hands control back to the live feed. A what-if is display only:
// it never records a minute and never makes a paper entry (live minutes keep
// being recorded from the live chain while it is on).

import (
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

// LUTManual: nil = live value.
type LUTManual struct {
	Time     string   `json:"time,omitempty"`     // HH:MM evaluation minute (picks the table), 09:16-13:30
	Future   *float64 `json:"future,omitempty"`   // underlying (synthetic future)
	CELTP    *float64 `json:"ce_ltp,omitempty"`   // ATM CE price
	PELTP    *float64 `json:"pe_ltp,omitempty"`   // ATM PE price
	Straddle *float64 `json:"straddle,omitempty"` // ATM straddle (used when CE/PE are blank)
	BuildIV  *float64 `json:"build_iv,omitempty"` // raw build IV, e.g. 0.17 (used when no prices)
	DTE      *float64 `json:"dte,omitempty"`      // raw calendar days to expiry at the minute

	// Daily inputs (today's row of the daily CSV).
	PrevStraddle *float64 `json:"prev_straddle,omitempty"`
	PrevFuture   *float64 `json:"prev_future,omitempty"`
	YesterdayIV  *float64 `json:"yesterday_iv,omitempty"`
	IDVPure      *float64 `json:"idv_pure,omitempty"`
	IDVWithPrev  *float64 `json:"idv_with_prev,omitempty"`
	NormAdjChg   *float64 `json:"norm_adj_chg,omitempty"`
}

func (m LUTManual) fields() []string {
	var out []string
	add := func(name string, v *float64) {
		if v != nil {
			out = append(out, fmt.Sprintf("%s=%g", name, *v))
		}
	}
	if m.Time != "" {
		out = append(out, "time="+m.Time)
	}
	add("future", m.Future)
	add("CE", m.CELTP)
	add("PE", m.PELTP)
	add("straddle", m.Straddle)
	add("build IV", m.BuildIV)
	add("DTE", m.DTE)
	add("prev straddle", m.PrevStraddle)
	add("prev future", m.PrevFuture)
	add("yday IV", m.YesterdayIV)
	add("IDV pure", m.IDVPure)
	add("IDV with prev", m.IDVWithPrev)
	add("norm adj IV chg", m.NormAdjChg)
	return out
}

func (m LUTManual) active() bool { return len(m.fields()) > 0 }

// applyDaily overrides the daily inputs; weighted IDV is recomputed.
func (m LUTManual) applyDaily(in LUTDailyInputs, ok bool, errText string) (LUTDailyInputs, bool, string) {
	touched := false
	set := func(dst *float64, v *float64) {
		if v != nil {
			*dst, touched = *v, true
		}
	}
	set(&in.PrevStraddle, m.PrevStraddle)
	set(&in.PrevFuture, m.PrevFuture)
	set(&in.YesterdayIV, m.YesterdayIV)
	set(&in.IDVPure, m.IDVPure)
	set(&in.IDVWithPrev, m.IDVWithPrev)
	set(&in.NormalizedAdjChg, m.NormAdjChg)
	set(&in.FinalAdjChg, m.NormAdjChg) // a manual change stands for both
	if !touched {
		return in, ok, errText
	}
	in.WeightedIDV = lutWeights[0]*in.IDVPure + lutWeights[1]*in.IDVWithPrev + lutWeights[2]*in.YesterdayIV
	if in.PrevStraddle <= 0 || in.PrevFuture <= 0 || in.YesterdayIV <= 0 || in.IDVPure <= 0 || in.IDVWithPrev <= 0 {
		msg := "daily inputs incomplete -- fill prev straddle, prev future, yesterday IV, IDV pure and IDV with prev"
		if errText != "" {
			msg += " (daily CSV: " + errText + ")"
		}
		return in, false, msg
	}
	return in, true, ""
}

func (m LUTManual) validate(now time.Time) error {
	if m.Time != "" {
		t, err := ParseClockTodayIST(m.Time, now)
		if err != nil {
			return fmt.Errorf("manual time: %w", err)
		}
		if hm := t.Hour()*100 + t.Minute(); hm < 916 || hm > lutLastScan {
			return fmt.Errorf("manual time must be 09:16-13:30")
		}
	}
	for name, v := range map[string]*float64{"future": m.Future, "CE": m.CELTP, "PE": m.PELTP, "straddle": m.Straddle, "build IV": m.BuildIV, "DTE": m.DTE} {
		if v != nil && *v <= 0 {
			return fmt.Errorf("manual %s must be > 0 (leave blank for live)", name)
		}
	}
	if m.BuildIV != nil && *m.BuildIV > 2 {
		return fmt.Errorf("build IV is a fraction (0.17 = 17%%)")
	}
	return nil
}

// lutStraddleIV solves the IV whose BS ATM straddle (CE + PE at K) equals x.
func lutStraddleIV(x, S, K, T float64) (float64, bool) {
	f := func(iv float64) float64 { return lutBSPrice(true, S, K, T, iv) + lutBSPrice(false, S, K, T, iv) - x }
	lo, hi := 0.001, 3.0
	if f(lo) > 0 || f(hi) < 0 {
		return 0, false
	}
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if f(mid) < 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2, true
}

// lutCtx is one evaluation (live or what-if) with everything that fed it.
type lutCtx struct {
	In       LUTDailyInputs
	InOK     bool
	InErr    string
	S, K     float64
	U0916    float64
	OGSource string
	NormOG   float64
	EvalHHMM int
	Preview  bool
	RawDTE   float64
	BusDays  int
	Row      *OptionChainRow
	M        LUTMinuteInput
	Ev       *LUTEvaluation
	Err      string
	IsEvent  bool // event day: no entry before 09:18
	// for re-evaluating against another table set (with)
	Time         string
	ManualPx     bool
	ManualActive bool
}

// evaluate runs the LUT for one set of inputs (man empty = live). Caller
// holds e.mu.
func (e *lutEngine) evaluate(now time.Time, hhmm int, chain *OptionChainSnapshot, cfg LUTConfig, man LUTManual) lutCtx {
	var c lutCtx
	c.In, c.InOK, c.InErr = man.applyDaily(e.inputs, e.inputsOK, e.inputsErr)
	if chain != nil {
		c.S = lutUnderlying(chain)
	}
	if man.Future != nil {
		c.S = *man.Future
	}

	// Opening gap off the 09:16 future: tab override > captured > provisional.
	switch {
	case cfg.Underlying0916 > 0:
		c.U0916, c.OGSource = cfg.Underlying0916, "set on the tab"
	case e.u0916 > 0:
		c.U0916, c.OGSource = e.u0916, e.ogSource
	case man.Future != nil:
		c.U0916, c.OGSource = c.S, "provisional: manual future (09:16 not captured yet)"
	default:
		c.U0916, c.OGSource = c.S, "provisional: live future (09:16 not reached yet)"
	}
	c.NormOG = LUTNormOG(c.U0916, c.In.PrevFuture, c.In.YesterdayIV)

	if c.S <= 0 {
		c.Err = "no future yet (no live prices before 09:15) -- enter a manual future to check"
		return c
	}
	if !e.tablesLoaded || !c.InOK {
		return c
	}

	// Evaluation minute.
	c.EvalHHMM = hhmm
	switch {
	case man.Time != "":
		if t, err := ParseClockTodayIST(man.Time, now); err == nil {
			c.EvalHHMM, c.Preview = t.Hour()*100+t.Minute(), true
		}
	case hhmm < 916:
		c.EvalHHMM, c.Preview = 916, true
	case hhmm > lutRecordUntil:
		c.EvalHHMM, c.Preview = lutRecordUntil, true
	}
	evalAt := now
	if man.Time != "" || hhmm < 916 { // DTE at that minute today
		evalAt = time.Date(now.Year(), now.Month(), now.Day(), c.EvalHHMM/100, c.EvalHHMM%100, 0, 0, now.Location())
	}

	// DTE / business days.
	expStr := cfg.Expiry
	if chain != nil {
		expStr = chain.Expiry
	} else if expStr == "" {
		expStr = e.expiry
	}
	open := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, now.Location())
	if man.DTE != nil {
		c.RawDTE = *man.DTE
		c.BusDays = lutBusDays(now, int(math.Ceil(c.RawDTE)))
	} else if exp, err := lutParseExpiry(expStr); err == nil {
		c.RawDTE = lutRawDTE(evalAt, exp)
		c.BusDays = lutBusDays(now, int(math.Ceil(lutRawDTE(open, exp))))
	} else {
		c.Err = "no expiry yet (no chain) -- set the Expiry box or a manual DTE"
		return c
	}

	// Prices at the ATM strike: manual CE/PE > manual straddle > manual IV > live.
	c.K = math.Round(c.S/lutStrikeStep) * lutStrikeStep
	if chain != nil {
		c.Row, _ = FindRowByStrike(*chain, int(c.K))
	}
	m := LUTMinuteInput{HHMM: c.EvalHHMM, Underlying: c.S, RawDTE: c.RawDTE, BusDays: c.BusDays, PrevIV: e.lastIV}
	if c.Row != nil {
		var ceCarried, peCarried bool
		m.CELTP, ceCarried = e.carryLTP(now, c.Row.CEToken, c.Row.CELtp)
		m.PELTP, peCarried = e.carryLTP(now, c.Row.PEToken, c.Row.PELtp)
		live := !c.Preview && !man.active() && c.EvalHHMM == now.Hour()*100+now.Minute()
		if (ceCarried || peCarried) && live && e.carryLogged != c.EvalHHMM {
			e.carryLogged = c.EvalHHMM // once per minute, not every 100ms tick
			log.Printf("[LUT] %02d:%02d K=%.0f LTP 0 on the feed -- previous second used: CE %.2f%s PE %.2f%s",
				c.EvalHHMM/100, c.EvalHHMM%100, c.K, m.CELTP, carriedTag(ceCarried), m.PELTP, carriedTag(peCarried))
		}
	}
	T := math.Max(c.RawDTE/365.0, 1e-5)
	manualPx := true
	switch {
	case man.CELTP != nil || man.PELTP != nil:
		if man.CELTP != nil {
			m.CELTP = *man.CELTP
		}
		if man.PELTP != nil {
			m.PELTP = *man.PELTP
		}
	case man.Straddle != nil:
		iv, ok := lutStraddleIV(*man.Straddle, c.S, c.K, T)
		if !ok {
			c.Err = fmt.Sprintf("manual straddle %.2f is not a valid ATM straddle for future %.2f, DTE %.3f", *man.Straddle, c.S, c.RawDTE)
			return c
		}
		m.CELTP, m.PELTP = lutBSPrice(true, c.S, c.K, T, iv), lutBSPrice(false, c.S, c.K, T, iv)
	case man.BuildIV != nil:
		m.CELTP, m.PELTP = lutBSPrice(true, c.S, c.K, T, *man.BuildIV), lutBSPrice(false, c.S, c.K, T, *man.BuildIV)
	default:
		manualPx = false
	}
	c.M = m
	c.IsEvent = e.isEvent
	c.Time, c.ManualPx, c.ManualActive = now.Format("15:04:05"), manualPx, man.active()
	return c.with(e.set)
}

// with evaluates the same minute (same prices, inputs, gap, DTE) against
// another table set. Only valid on a ctx that reached the evaluation.
func (c lutCtx) with(set LUTSet) lutCtx {
	ev := EvaluateLUTMinute(set, c.In, c.NormOG, c.IsEvent, c.M)
	ev.Time = c.Time
	ev.Preview = c.Preview || c.ManualActive
	if c.Row == nil && !c.ManualPx && ev.Skip == "" {
		ev.Skip, ev.Allowed = fmt.Sprintf("strike %.0f not in chain", c.K), false
	}
	c.Ev = &ev
	return c
}

// LUTRange is one continuous ATM-straddle range where the LUT says YES.
type LUTRange struct {
	StraddleFrom float64 `json:"straddle_from"`
	StraddleTo   float64 `json:"straddle_to"`
	IVFrom       float64 `json:"build_iv_from"` // raw build IV at the ends
	IVTo         float64 `json:"build_iv_to"`
	AdjIVFrom    float64 `json:"adj_iv_from"`
	AdjIVTo      float64 `json:"adj_iv_to"`
	TPFrom       float64 `json:"tp_bps_from"`
	TPTo         float64 `json:"tp_bps_to"`
}

// LUTSellRange: with this future, minute, DTE, gap and daily inputs, the
// ATM straddle levels at which the LUT would sell.
type LUTSellRange struct {
	Table    string     `json:"table"`
	Time     string     `json:"time"`
	Future   float64    `json:"future"`
	Strike   float64    `json:"strike"`
	RawDTE   float64    `json:"raw_dte"`
	Straddle float64    `json:"straddle"` // current (live or manual) ATM straddle
	InZone   bool       `json:"in_zone"`
	From     float64    `json:"from"`
	To       float64    `json:"to"`
	Step     float64    `json:"step"`
	Ranges   []LUTRange `json:"ranges"`
	Skip     string     `json:"skip,omitempty"`
}

// lutScanSellRange scans the ATM straddle from 40% to 160% of yesterday's
// straddle (everything else fixed) and returns the YES ranges.
func lutScanSellRange(set LUTSet, c lutCtx, stageHHMM int) LUTSellRange {
	m := c.M
	m.HHMM, m.PrevIV = stageHHMM, 0
	stage := lutStage(stageHHMM)
	r := LUTSellRange{Table: lutStageNames[stage], Time: fmt.Sprintf("%02d:%02d", stageHHMM/100, stageHHMM%100),
		Future: m.Underlying, Strike: c.K, RawDTE: m.RawDTE}
	if c.Ev != nil {
		r.Straddle = c.Ev.Straddle
	}
	prev := c.In.PrevStraddle
	if prev <= 0 || m.Underlying <= 0 || m.RawDTE <= 0 {
		r.Skip = "inputs not ready"
		return r
	}
	T := math.Max(m.RawDTE/365.0, 1e-5)
	r.From, r.To = math.Round(prev*0.4), math.Round(prev*1.6)
	r.Step = math.Max(0.05, math.Round((r.To-r.From)/300/0.05)*0.05)
	var cur *LUTRange
	for x := r.From; x <= r.To+1e-9; x += r.Step {
		iv, ok := lutStraddleIV(x, m.Underlying, c.K, T)
		if !ok {
			cur = nil
			continue
		}
		mm := m
		mm.CELTP, mm.PELTP = lutBSPrice(true, m.Underlying, c.K, T, iv), lutBSPrice(false, m.Underlying, c.K, T, iv)
		ev := EvaluateLUTMinute(set, c.In, c.NormOG, c.IsEvent, mm)
		if ev.Skip != "" {
			r.Skip = ev.Skip
			cur = nil
			continue
		}
		if !ev.Allowed {
			cur = nil
			continue
		}
		if cur == nil {
			r.Ranges = append(r.Ranges, LUTRange{StraddleFrom: x, IVFrom: ev.BuildIV, AdjIVFrom: ev.AdjBuildIV, TPFrom: ev.TPBps})
			cur = &r.Ranges[len(r.Ranges)-1]
		}
		cur.StraddleTo, cur.IVTo, cur.AdjIVTo, cur.TPTo = x, ev.BuildIV, ev.AdjBuildIV, ev.TPBps
	}
	if len(r.Ranges) > 0 {
		r.Skip = ""
	}
	// The scan steps are about a point wide: pin each band's ends to the
	// 0.05 tick by bisection between the last YES and the first NO sample.
	yes := func(x float64) (LUTEvaluation, bool) {
		iv, ok := lutStraddleIV(x, m.Underlying, c.K, T)
		if !ok {
			return LUTEvaluation{}, false
		}
		mm := m
		mm.CELTP, mm.PELTP = lutBSPrice(true, m.Underlying, c.K, T, iv), lutBSPrice(false, m.Underlying, c.K, T, iv)
		ev := EvaluateLUTMinute(set, c.In, c.NormOG, c.IsEvent, mm)
		return ev, ev.Skip == "" && ev.Allowed
	}
	edge := func(in, out float64) (float64, LUTEvaluation, bool) {
		var at LUTEvaluation
		found := false
		for i := 0; i < 12 && math.Abs(in-out) > 0.05+1e-9; i++ {
			mid := math.Round((in+out)/2/0.05) * 0.05
			if mid == in || mid == out {
				break
			}
			if ev, ok := yes(mid); ok {
				in, at, found = mid, ev, true
			} else {
				out = mid
			}
		}
		return in, at, found
	}
	for i := range r.Ranges {
		g := &r.Ranges[i]
		if x, ev, ok := edge(g.StraddleFrom, g.StraddleFrom-r.Step); ok {
			g.StraddleFrom, g.IVFrom, g.AdjIVFrom, g.TPFrom = x, ev.BuildIV, ev.AdjBuildIV, ev.TPBps
		}
		if g.StraddleTo+r.Step <= r.To+1e-9 { // not the top of the scan
			if x, ev, ok := edge(g.StraddleTo, g.StraddleTo+r.Step); ok {
				g.StraddleTo, g.IVTo, g.AdjIVTo, g.TPTo = x, ev.BuildIV, ev.AdjBuildIV, ev.TPBps
			}
		}
	}
	for _, g := range r.Ranges {
		if r.Straddle >= g.StraddleFrom-lutBandTol && r.Straddle <= g.StraddleTo+lutBandTol {
			r.InZone = true
		}
	}
	return r
}

// lutBandTol: a straddle this close to a band's end counts as inside
// (half a 0.05 tick, plus rounding).
const lutBandTol = 0.03

// LUTSellLevel is one opening minute (09:16 .. 09:20, each has its own
// table) with the ATM straddle levels at which the LUT says YES there.
//
//   - Before the open the inputs are yesterday's close: yesterday's closing
//     future (gap 0 unless a manual future is set) and the ATM straddle
//     re-priced to today's 09:16 -- an estimate of where a trade starts.
//   - From 09:15 they are the live future and, once 09:16 is captured, the
//     real opening gap.
//   - A minute already recorded shows the levels as they were AT that
//     minute (its own recorded future, DTE and gap), its recorded straddle
//     and the answer the LUT gave.
type LUTSellLevel struct {
	LUTSellRange
	Basis    string  `json:"basis"`
	SellFrom float64 `json:"sell_from"` // lowest ATM straddle with a YES (0 = none)
	SellTo   float64 `json:"sell_to"`   // top of that first YES band
	Above    bool    `json:"above"`     // that band runs to the top of the scan: "SellFrom and above"
	Need     float64 `json:"need"`      // points the straddle is away from the nearest YES band (0 = inside)
	Done     bool    `json:"done"`      // the minute is recorded
	Allowed  bool    `json:"allowed"`   // recorded answer
	Note     string  `json:"note,omitempty"`
}

// lutSellLevels builds the 09:16 .. 09:20 rows from the shown evaluation.
// Caller holds e.mu.
// recordedOnly (after the opening window): a minute that was not recorded
// gets no level -- the live future no longer says anything about it.
func (e *lutEngine) lutSellLevels(now time.Time, shown lutCtx, expStr string, fixedDTE bool, basis string, recordedOnly bool) []LUTSellLevel {
	return lutSellLevelsFor(e.set, e.minutes, now, shown, expStr, fixedDTE, basis, recordedOnly)
}

// lutSellLevelsFor is lutSellLevels for any table set and its recorded minutes.
func lutSellLevelsFor(set LUTSet, minutes []LUTEvaluation, now time.Time, shown lutCtx, expStr string, fixedDTE bool, basis string, recordedOnly bool) []LUTSellLevel {
	exp, expErr := lutParseExpiry(expStr)
	recorded := map[string]*LUTEvaluation{}
	for i := range minutes {
		recorded[minutes[i].Time] = &minutes[i]
	}
	out := make([]LUTSellLevel, 0, 5)
	for hh := 916; hh <= 920; hh++ {
		c := shown
		lv := LUTSellLevel{Basis: basis}
		if rec := recorded[fmt.Sprintf("%02d:%02d", hh/100, hh%100)]; rec != nil && rec.Underlying > 0 && rec.RawDTE > 0 {
			// As it was at that minute.
			ev := *rec
			c.Ev, c.K, c.NormOG = &ev, rec.Strike, rec.NormOG
			c.M.Underlying, c.M.RawDTE = rec.Underlying, rec.RawDTE
			lv.Basis, lv.Done, lv.Allowed, lv.Note = "recorded at that minute", true, rec.Allowed, rec.Skip
		} else if recordedOnly {
			lv.LUTSellRange = LUTSellRange{Table: lutStageNames[lutStage(hh)], Time: fmt.Sprintf("%02d:%02d", hh/100, hh%100), Skip: "minute not recorded"}
			out = append(out, lv)
			continue
		} else if !fixedDTE && expErr == nil {
			at := time.Date(now.Year(), now.Month(), now.Day(), hh/100, hh%100, 0, 0, now.Location())
			c.M.RawDTE = lutRawDTE(at, exp) // each minute's own time to expiry
		}
		lv.LUTSellRange = lutScanSellRange(set, c, hh)
		if n := len(lv.Ranges); n > 0 {
			lv.SellFrom, lv.SellTo = lv.Ranges[0].StraddleFrom, lv.Ranges[0].StraddleTo
			lv.Above = lv.SellTo >= lv.To-lv.Step
			if !lv.InZone && lv.Straddle > 0 {
				best := math.Inf(1)
				for _, g := range lv.Ranges {
					for _, edge := range []float64{g.StraddleFrom, g.StraddleTo} {
						if d := edge - lv.Straddle; math.Abs(d) < math.Abs(best) {
							best = d
						}
					}
				}
				lv.Need = math.Round(best*100) / 100
			}
		}
		out = append(out, lv)
	}
	return out
}

// lutLevelsText is the one-line form of the rows, for the log.
func lutLevelsText(levels []LUTSellLevel) string {
	parts := make([]string, 0, len(levels))
	for _, l := range levels {
		switch {
		case len(l.Ranges) == 0:
			parts = append(parts, l.Time+" no YES level")
		case l.Above:
			parts = append(parts, fmt.Sprintf("%s >= %.2f", l.Time, l.SellFrom))
		case len(l.Ranges) == 1:
			parts = append(parts, fmt.Sprintf("%s %.2f-%.2f", l.Time, l.SellFrom, l.SellTo))
		default:
			parts = append(parts, fmt.Sprintf("%s %.2f-%.2f (+%d more band(s))", l.Time, l.SellFrom, l.SellTo, len(l.Ranges)-1))
		}
	}
	return strings.Join(parts, " | ")
}
