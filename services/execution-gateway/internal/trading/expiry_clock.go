package trading

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"
)

// Expiry clock (owner, 2026-11-06): show, on the Portfolio and Muhurat tabs, the
// time to expiry the chain greeks -- and so every hedge -- actually use, with how
// it is made up, next to the normal-day figure and the LUT's own DTE.
//
// The chain greeks are computed in the C++ feed decoder
// (services/feed-decoder/src/chain_builder.cpp, calculate_dte); the gateway never
// receives that number, so feedDTEDays reproduces it exactly:
//   days  = calendar days from today's midnight to the expiry day's midnight;
//   today = a normal day: 1 before 09:15, 0 from 15:40, else minutes left to 15:40 / 385;
//           a listed special session (the whole trading day): min(minutes left to its close, its length) / its length;
//   DTE   = max(0.000001, days + today), in days; the greeks take DTE / 365 years.
// The LUT (lutRawDTE) uses the same 09:15-15:40 day and the same Muhurat rule.
// Keep feedDTEDays in step with chain_builder.cpp (TestFeedDTEMatchesTheDecoder).

const (
	feedDayStartMin = 9*60 + 15
	feedDayEndMin   = 15*60 + 40
	feedDayMinutes  = feedDayEndMin - feedDayStartMin // 385, the same day as the LUT (owner, 2026-11-06)
)

// feedDTEDays: the decoder's time to expiry in days at now, with its parts.
func feedDTEDays(now, expiry time.Time) (dte, days, today, minutesLeft float64, special bool) {
	loc := now.Location()
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	e := expiry.In(loc)
	e0 := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, loc)
	days = math.Round(e0.Sub(d0).Hours() / 24)
	cur := float64(now.Hour()*60+now.Minute()) + float64(now.Second())/60
	if ss, _, _, ok := specialSessionOn(now); ok {
		open, _ := time.Parse("15:04", ss.Open)
		cl, _ := time.Parse("15:04", ss.Close)
		closeMin := float64(cl.Hour()*60 + cl.Minute())
		length := closeMin - float64(open.Hour()*60+open.Minute())
		minutesLeft = math.Max(0, math.Min(closeMin-cur, length))
		if length > 0 {
			today = minutesLeft / length // the session is the whole day
		}
		special = true
	} else {
		today, minutesLeft = feedNormalToday(cur)
	}
	return math.Max(0.000001, days+today), days, today, minutesLeft, special
}

// feedNormalToday: the decoder's share of a normal day still to come at cur (minutes).
func feedNormalToday(cur float64) (today, minutesLeft float64) {
	switch {
	case cur < feedDayStartMin:
		return 1, feedDayMinutes
	case cur >= feedDayEndMin:
		return 0, 0
	default:
		return (feedDayEndMin - cur) / feedDayMinutes, feedDayEndMin - cur
	}
}

// ExpiryClockRow is one expiry's time to expiry now.
type ExpiryClockRow struct {
	Expiry       string  `json:"expiry"`
	Days         float64 `json:"calendar_days"`  // calendar days to the expiry day
	Today        float64 `json:"today_fraction"` // today's part of a day still to come
	MinutesLeft  float64 `json:"minutes_left"`
	DTE          float64 `json:"dte_days"`         // used by the chain greeks and every hedge
	Years        float64 `json:"dte_years"`        // DTE / 365, the T in the greeks
	NormalDTE    float64 `json:"normal_day_dte"`   // the same clock time on a normal day
	LUTDTE       float64 `json:"lut_dte"`          // the LUT's own (same clock; a check)
	DiffVsNormal float64 `json:"diff_vs_normal"`   // DTE - normal-day DTE (days)
	// ATM IV and greeks from the ATM straddle's prices (OTM leg's IV, Black-76 on
	// the synthetic future), on the feed's clock (what hedging uses) and, for
	// comparison, on a normal day's clock at the same time; FeedIV = the feed's
	// own ATM IV (average of its CE / PE IV) as a cross-check.
	Future     float64 `json:"future,omitempty"`
	ATM        float64 `json:"atm,omitempty"`
	CE         float64 `json:"ce,omitempty"`
	PE         float64 `json:"pe,omitempty"`
	IV         float64 `json:"iv,omitempty"`
	CEDelta    float64 `json:"ce_delta,omitempty"`
	PEDelta    float64 `json:"pe_delta,omitempty"`
	Gamma      float64 `json:"gamma,omitempty"`
	IVNormal   float64 `json:"iv_normal_clock,omitempty"`
	CEDeltaN   float64 `json:"ce_delta_normal_clock,omitempty"`
	GammaN     float64 `json:"gamma_normal_clock,omitempty"`
	FeedIV     float64 `json:"feed_iv,omitempty"`
}

// withIV fills the row's ATM IV and greeks from that expiry's chain.
func (r *ExpiryClockRow) withIV(c *OptionChainSnapshot) {
	if c == nil {
		return
	}
	F := lutUnderlying(c)
	if F <= 0 {
		return
	}
	r.Future, r.ATM = F, math.Round(F/lutStrikeStep)*lutStrikeStep
	row, err := FindRowByStrike(*c, int(r.ATM))
	if err != nil || row == nil || row.CELtp <= 0 || row.PELtp <= 0 {
		return
	}
	r.CE, r.PE = row.CELtp, row.PELtp
	if row.CEIV > 0 && row.PEIV > 0 {
		r.FeedIV = (row.CEIV + row.PEIV) / 2 / 100
	}
	greeks := func(dte float64) (iv, ced, ped, g float64) {
		T := math.Max(dte/365.0, 1e-5)
		if iv = simStrikeIV(F, r.ATM, T, r.CE, r.PE); iv > 0 {
			_, ced, g = simBlack76(true, F, r.ATM, T, iv)
			_, ped, _ = simBlack76(false, F, r.ATM, T, iv)
		}
		return
	}
	r.IV, r.CEDelta, r.PEDelta, r.Gamma = greeks(r.DTE)
	r.IVNormal, r.CEDeltaN, _, r.GammaN = greeks(r.NormalDTE)
}

// ExpiryClock is GET /api/clock's payload.
type ExpiryClock struct {
	Success    bool             `json:"success"`
	Error      string           `json:"error,omitempty"`
	Now        string           `json:"now"`
	Symbol     string           `json:"symbol"`
	Clock      string           `json:"clock"` // what today's part is made of
	Session    *SpecialSession  `json:"session,omitempty"`
	DayMinutes int              `json:"day_minutes"`
	Rows       []ExpiryClockRow `json:"rows"`
}

func expiryClockRow(now time.Time, expiry string) (ExpiryClockRow, bool) {
	exp, err := lutParseExpiry(expiry)
	if err != nil {
		return ExpiryClockRow{}, false
	}
	dte, days, today, left, _ := feedDTEDays(now, exp)
	cur := float64(now.Hour()*60+now.Minute()) + float64(now.Second())/60
	nt, _ := feedNormalToday(cur)
	normal := math.Max(0.000001, days+nt)
	return ExpiryClockRow{Expiry: expiry, Days: days, Today: today, MinutesLeft: left, DTE: dte, Years: dte / 365,
		NormalDTE: normal, LUTDTE: lutRawDTE(now, exp), DiffVsNormal: dte - normal}, true
}

func (s *Service) expiryClock(now time.Time, symbol string) ExpiryClock {
	out := ExpiryClock{Success: true, Now: now.Format("2006-01-02 15:04:05"), Symbol: symbol, DayMinutes: feedDayMinutes,
		Clock: "normal day 09:15-15:40: today's part = minutes left to 15:40 / 385 (1 before 09:15, 0 from 15:40)"}
	if ss, _, _, ok := specialSessionOn(now); ok {
		out.Session = &ss
		out.Clock = "special session " + ss.Name + " " + ss.Open + "-" + ss.Close + ": the session is the whole day -- today's part = minutes left to " + ss.Close + " / the session's length (1 at " + ss.Open + ", 0 at " + ss.Close + ")"
	}
	if s.Snapshot == nil {
		out.Success, out.Error = false, "no market data"
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	c, err := s.Snapshot.GetOptionChain(ctx, symbol, "")
	cancel()
	if err != nil || c == nil {
		out.Success, out.Error = false, "option chain not available"
		return out
	}
	exps := []string{c.Expiry}
	for _, e := range c.AvailableExpiries {
		if len(exps) >= 3 {
			break
		}
		if !strings.EqualFold(e, c.Expiry) {
			exps = append(exps, e)
		}
	}
	for i, e := range exps {
		r, ok := expiryClockRow(now, e)
		if !ok {
			continue
		}
		ch := c
		if i > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			ch, _ = s.Snapshot.GetOptionChain(ctx, symbol, e)
			cancel()
		}
		r.withIV(ch)
		out.Rows = append(out.Rows, r)
	}
	return out
}

// ExpiryClockHandler: GET /api/clock?symbol=NIFTY.
func (h *Handlers) ExpiryClockHandler(w http.ResponseWriter, r *http.Request) {
	sym := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if sym == "" {
		sym = "NIFTY"
	}
	lutJSON(w, http.StatusOK, h.Service.expiryClock(time.Now().In(lutIST()), sym))
}
