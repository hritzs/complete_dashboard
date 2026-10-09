package trading

// Candle-close prices for the LUT minute: each option's LAST TRADE BEFORE
// the minute boundary by exchange trade time -- what a terminal's 1-minute
// candle shows (GreekSoft's 09:17 straddle = 09:16-09:17 candle close).
// The LTP at the minute's first tick instead depends on when the conflated
// broadcast last refreshed that option (~640ms apart, measured 2026-10-07)
// and read up to ~1.7pts off the candle close.

import (
	"fmt"
	"math"
	"time"
)

// lutCloseGrace: how long after a boundary to wait for the ATM legs'
// closes to become final (a trade with time >= the boundary seen) before
// recording with what is known -- the last trade so far, which is the
// close unless a pre-boundary trade is still in flight.
// 200 ms: the decoder publishes every 100 ms and feed latency is a few ms,
// so by then the last trade so far is the close (was 2 s: the minute was
// recorded ~0.5-2 s late, 2026-10-09).
const lutCloseGrace = 200 * time.Millisecond

// lutCloseBefore is one leg's last trade before boundary B (unix seconds).
// final: a later trade confirms nothing else came before B. ok=false: no
// trade-time data for the leg (older decoder) -- callers keep the LTP.
func lutCloseBefore(ltp float64, ltt int64, close float64, closeLTT, closeNext int64, b int64) (px float64, final, ok bool) {
	switch {
	case closeNext > 0 && closeLTT < b && b <= closeNext && close > 0:
		return close, true, true
	case ltt > 0 && ltt < b && ltp > 0:
		return ltp, false, true // nothing traded since B yet
	}
	return ltp, false, false
}

// lutCloseChain returns a copy of chain whose CE/PE LTPs are the closes
// before boundary b, with the synthetic future re-derived from those same
// closes, and whether it is ready to record: the strike at the synthetic
// future has both closes final, the grace period is over, or the feed
// carries no trade times at all (then it is the chain as is).
func lutCloseChain(chain *OptionChainSnapshot, b time.Time, now time.Time) (*OptionChainSnapshot, bool) {
	if chain == nil {
		return nil, false
	}
	bs := b.Unix()
	cp := *chain
	cp.Chain = make([]OptionChainRow, len(chain.Chain))
	copy(cp.Chain, chain.Chain)
	hasTimes := false
	final := map[float64]bool{}
	for i := range cp.Chain {
		r := &cp.Chain[i]
		ce, ceFinal, ceOK := lutCloseBefore(r.CELtp, r.CELTT, r.CEClose, r.CECloseLTT, r.CECloseNext, bs)
		pe, peFinal, peOK := lutCloseBefore(r.PELtp, r.PELTT, r.PEClose, r.PECloseLTT, r.PECloseNext, bs)
		if ceOK || peOK {
			hasTimes = true
		}
		r.CELtp, r.PELtp = ce, pe
		final[r.Strike] = ceFinal && peFinal
	}
	if !hasTimes {
		return chain, true
	}
	// Synthetic future from the closes at the strike nearest the live one
	// (put-call parity, the decoder's own formula: K + CE - PE).
	if S := lutUnderlying(chain); S > 0 {
		var best *OptionChainRow
		for i := range cp.Chain {
			r := &cp.Chain[i]
			if r.CELtp > 0 && r.PELtp > 0 && (best == nil || math.Abs(r.Strike-S) < math.Abs(best.Strike-S)) {
				best = r
			}
		}
		if best != nil {
			cp.SyntheticFuture = best.Strike + best.CELtp - best.PELtp
			cp.ATM = math.Round(cp.SyntheticFuture/lutStrikeStep) * lutStrikeStep
		}
	}
	K := math.Round(lutUnderlying(&cp)/lutStrikeStep) * lutStrikeStep
	return &cp, final[K] || now.Sub(b) >= lutCloseGrace
}

// MinuteClose is one minute's candle-close picture at the ATM: what every
// minute-end decision (LUT, hedge / SL / TP, Paper Sim) is based on.
type MinuteClose struct {
	Time     string  `json:"time"`     // HH:MM boundary
	ATM      float64 `json:"atm"`      // strike at the synthetic future from the closes
	CE       float64 `json:"ce"`       // ATM CE close (last trade before HH:MM:00)
	PE       float64 `json:"pe"`       // ATM PE close
	CETrade  string  `json:"ce_trade"` // exchange time of that CE trade (HH:MM:SS)
	PETrade  string  `json:"pe_trade"` // exchange time of that PE trade
	Straddle float64 `json:"straddle"` // CE + PE closes
	SynFut   float64 `json:"syn_fut"`  // ATM + CE - PE (from the closes)
	LiveCE   float64 `json:"live_ce"`  // the live LTPs at the same moment (to compare)
	LivePE   float64 `json:"live_pe"`
	LiveFut  float64 `json:"live_fut"`
	ReadyMs  int64   `json:"ready_ms"` // ms after the boundary it was taken

	Source string  `json:"source"`            // "GreekSoft" (its candle closes) or "feed" (our broadcast closes)
	FeedCE float64 `json:"feed_ce,omitempty"` // our feed closes the GreekSoft ones replaced
	FeedPE float64 `json:"feed_pe,omitempty"`

	// ATM straddle high / low inside the minute (100 ms samples of the
	// live ATM's CE + PE) and the day so far.
	StrLow    float64 `json:"str_low,omitempty"`
	StrHigh   float64 `json:"str_high,omitempty"`
	StrLowAt  string  `json:"str_low_at,omitempty"`
	StrHighAt string  `json:"str_high_at,omitempty"`
	DayLow    float64 `json:"day_low,omitempty"`
	DayHigh   float64 `json:"day_high,omitempty"`
	DayLowAt  string  `json:"day_low_at,omitempty"`
	DayHighAt string  `json:"day_high_at,omitempty"`
}

// minuteCloseOf summarises the closes chain cc (from lutCloseChain at
// boundary b) against the live chain at the ATM of the closes.
func minuteCloseOf(cc, live *OptionChainSnapshot, b time.Time) MinuteClose {
	m := MinuteClose{Time: b.Format("15:04"), ReadyMs: time.Since(b).Milliseconds()}
	if cc == nil {
		return m
	}
	m.ATM, m.SynFut = cc.ATM, cc.SyntheticFuture
	if live != nil {
		m.LiveFut = lutUnderlying(live)
	}
	bs := b.Unix()
	ts := func(t int64) string {
		if t <= 0 {
			return ""
		}
		return time.Unix(t, 0).In(b.Location()).Format("15:04:05")
	}
	for i := range cc.Chain {
		r := cc.Chain[i]
		if r.Strike != m.ATM {
			continue
		}
		m.CE, m.PE, m.Straddle = r.CELtp, r.PELtp, r.CELtp+r.PELtp
		// The close's own trade time: the pre-boundary trade (CloseLTT) once
		// a later trade is seen, else the LTP's trade time.
		if r.CECloseNext >= bs && r.CECloseLTT > 0 {
			m.CETrade = ts(r.CECloseLTT)
		} else {
			m.CETrade = ts(r.CELTT)
		}
		if r.PECloseNext >= bs && r.PECloseLTT > 0 {
			m.PETrade = ts(r.PECloseLTT)
		} else {
			m.PETrade = ts(r.PELTT)
		}
	}
	if live != nil {
		for _, r := range live.Chain {
			if r.Strike == m.ATM {
				m.LiveCE, m.LivePE = r.CELtp, r.PELtp
			}
		}
	}
	return m
}

func (m MinuteClose) String() string {
	src := fmt.Sprintf("feed (CE trade %s, PE trade %s)", m.CETrade, m.PETrade)
	if m.Source == "GreekSoft" {
		src = fmt.Sprintf("GreekSoft candles (our feed CE %.2f PE %.2f)", m.FeedCE, m.FeedPE)
	}
	return fmt.Sprintf("%s ATM %.0f | CE close %.2f + PE close %.2f = straddle %.2f | syn fut %.2f | source %s | straddle in minute low %.2f (%s) high %.2f (%s) | day low %.2f (%s) high %.2f (%s) | live LTP CE %.2f PE %.2f fut %.2f | taken +%dms",
		m.Time, m.ATM, m.CE, m.PE, m.Straddle, m.SynFut, src, m.StrLow, m.StrLowAt, m.StrHigh, m.StrHighAt,
		m.DayLow, m.DayLowAt, m.DayHigh, m.DayHighAt, m.LiveCE, m.LivePE, m.LiveFut, m.ReadyMs)
}

// lutStrHL is a straddle high / low with when each happened.
type lutStrHL struct {
	lo, hi     float64
	loAt, hiAt string
}

func (h *lutStrHL) add(v float64, at string) {
	if v <= 0 {
		return
	}
	if h.lo == 0 || v < h.lo {
		h.lo, h.loAt = v, at
	}
	if v > h.hi {
		h.hi, h.hiAt = v, at
	}
}

// trackStraddle samples the live ATM straddle (CE + PE LTP at the chain's
// ATM) into the current minute's and the day's high / low (09:15-15:40).
// Caller holds e.mu.
func (e *lutEngine) trackStraddle(now time.Time, chain *OptionChainSnapshot) {
	hm := now.Hour()*100 + now.Minute()
	if hm < 915 || hm > lutRecordUntil {
		return
	}
	if hm != e.strMin {
		e.strPrevMin, e.strPrev = e.strMin, e.strCur
		e.strMin, e.strCur = hm, lutStrHL{}
	}
	for _, r := range chain.Chain {
		if r.Strike == chain.ATM && r.CELtp > 0 && r.PELtp > 0 {
			at := now.Format("15:04:05.0")
			e.strCur.add(r.CELtp+r.PELtp, at)
			e.strDay.add(r.CELtp+r.PELtp, at)
			return
		}
	}
}

// lutPrevMinute is the HHMM of the minute before hhmm.
func lutPrevMinute(hhmm int) int {
	h, m := hhmm/100, hhmm%100
	if m == 0 {
		return (h-1)*100 + 59
	}
	return h*100 + m - 1
}
