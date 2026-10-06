package trading

// Paper-trade simulation off the stored minute-end data (PAPER ONLY -- this
// file never sends an order). A straddle sold at a chosen minute's first
// tick (ATM, at that minute's prices) is marked minute by minute with the
// same minute-end rules as live trades: SL / TP per straddle (bps of the
// future), exit time, and the hedge rule (decideHedge, hedging at the ATM).
//
// Prices per minute, best source first:
//   chain -- the whole chain recorded at that minute (exact LTP + greeks)
//   import-- minute prices imported from another recorder (exact LTP)
//   ltp   -- the LUT minute's ATM CE/PE LTP (exact, ATM strike only)
//   bs    -- Black-Scholes from that minute's future, DTE and ATM build IV
//            (reconstructed: used for a strike that was not ATM / not recorded)

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PaperSimConfig is one simulated entry.
type PaperSimConfig struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"` // "09:16" auto / "start now" / "actual trade ..."
	Entry       string  `json:"entry"`  // HH:MM
	Lots        int     `json:"lots"`
	SLBps       float64 `json:"sl_bps"` // default 14
	TPBps       float64 `json:"tp_bps"` // 0 = the LUT's TP at the entry minute
	ExitTime    string  `json:"exit_time"`
	StraddleDiv float64 `json:"straddle_div"`
	HedgeDiv    float64 `json:"hedge_div"`
	HedgeMinBps float64 `json:"hedge_min_bps"`
	Hedge       bool    `json:"hedge"`          // legacy: true = HedgeMode "lots"
	HedgeMode   string  `json:"hedge_mode"`     // synthetic (default) / lots / off
	View        string  `json:"view,omitempty"` // "" current expiry / "next"
	CreatedAt   string  `json:"created_at,omitempty"`
}

type PaperSimLeg struct {
	Role       string  `json:"role"` // BUILD / HEDGE
	Strike     float64 `json:"strike"`
	OptionType string  `json:"option_type"`
	Qty        int64   `json:"qty"`             // signed: short < 0
	QtyF       float64 `json:"qty_f,omitempty"` // synthetic future: fractional contracts
	AvgPrice   float64 `json:"avg_price"`
	Mark       float64 `json:"mark"`
	Delta      float64 `json:"delta"` // per unit
	Gamma      float64 `json:"gamma"`
	Theta      float64 `json:"theta"` // per day
	Vega       float64 `json:"vega"`  // per 1 vol point
	IV         float64 `json:"iv"`
	Source     string  `json:"source"`
	Realized   float64 `json:"realized"`
	PnL        float64 `json:"pnl"`
}

type PaperSimPoint struct {
	Time            string        `json:"time"`
	Future          float64       `json:"future"`
	ATM             float64       `json:"atm"`
	ATMStraddle     float64       `json:"atm_straddle"`
	ATMCE           float64       `json:"atm_ce"`
	ATMPE           float64       `json:"atm_pe"`
	Strike          float64       `json:"strike"`   // the position's strike
	QuoteTs         string        `json:"quote_ts"` // when these prices were recorded
	CE              float64       `json:"ce"`       // position-strike marks
	PE              float64       `json:"pe"`
	Straddle        float64       `json:"straddle"`
	PnL             float64       `json:"pnl"`
	OptionPnL       float64       `json:"option_pnl"` // the sold straddle
	HedgePnL        float64       `json:"hedge_pnl"`  // hedge legs
	PnLPerStraddle  float64       `json:"pnl_per_straddle"`
	NetDelta        float64       `json:"net_delta"` // whole position (qty-weighted)
	NetGamma        float64       `json:"net_gamma"`
	NetTheta        float64       `json:"net_theta"`     // per day
	NetVega         float64       `json:"net_vega"`      // per 1 vol point
	HedgeQty        int64         `json:"hedge_qty"`     // open ATM-straddle hedge qty (lots mode, signed sum)
	HedgeFut        float64       `json:"hedge_fut"`     // synthetic-future hedge, contracts (signed, fractional)
	HedgeFutPerQty  float64       `json:"hedge_fut_qty"` // the same per 1 qty of the straddle
	HedgeTarget     float64       `json:"hedge_target"`  // per qty: where a breach would set it now
	PointsOut       float64       `json:"points_out"`
	PointsAllowed   float64       `json:"points_allowed"` // effective (after the bps floor)
	HedgeFloor      float64       `json:"hedge_floor"`
	HedgeCheck      string        `json:"hedge_check"` // decideHedge action this minute
	HedgeLotsNeeded int64         `json:"hedge_lots_needed"`
	Source          string        `json:"source"` // worst price source used this minute
	Event           string        `json:"event,omitempty"`
	Legs            []PaperSimLeg `json:"legs,omitempty"`
}

type PaperSimHedge struct {
	Time        string  `json:"time"`
	Strike      float64 `json:"strike"`
	CESide      string  `json:"ce_side"`
	PESide      string  `json:"pe_side"`
	Lots        int64   `json:"lots"`
	Qty         int64   `json:"qty"`
	CEPrice     float64 `json:"ce_price"`
	PEPrice     float64 `json:"pe_price"`
	DeltaBefore float64 `json:"delta_before"`
	DeltaAfter  float64 `json:"delta_after"`
	PointsOut   float64 `json:"points_out"`
	Allowed     float64 `json:"allowed"`
	Future      float64 `json:"future"`
	Mode        string  `json:"mode"`          // synthetic / lots
	Kind        string  `json:"kind"`          // ENTRY_NEUTRAL / RISK_BREACH / ATM_LOTS
	OldHedge    float64 `json:"old_hedge_qty"` // synthetic: per qty of the straddle
	NewHedge    float64 `json:"new_hedge_qty"`
	FutQty      float64 `json:"fut_contracts"` // synthetic: contracts traded
	Source      string  `json:"source"`
}

type PaperSimResult struct {
	Config         PaperSimConfig  `json:"config"`
	EntryTime      string          `json:"entry_time"`
	LUTAnswer      string          `json:"lut_answer"` // what the LUT said at the entry minute
	Strike         float64         `json:"strike"`
	Qty            int64           `json:"qty"`
	CEEntry        float64         `json:"ce_entry"`
	PEEntry        float64         `json:"pe_entry"`
	EntryStraddle  float64         `json:"entry_straddle"`
	EntryFuture    float64         `json:"entry_future"`
	EntrySource    string          `json:"entry_source"`
	SLPoints       float64         `json:"sl_points"`
	TPPoints       float64         `json:"tp_points"`
	TPBps          float64         `json:"tp_bps"`
	Status         string          `json:"status"` // OPEN / EXITED / NO DATA
	ExitTime       string          `json:"exit_time,omitempty"`
	ExitReason     string          `json:"exit_reason,omitempty"`
	PnL            float64         `json:"pnl"`
	OptionPnL      float64         `json:"option_pnl"`
	HedgePnL       float64         `json:"hedge_pnl"`
	PnLPerStraddle float64         `json:"pnl_per_straddle"`
	LotSize        int64           `json:"lot_size"`
	MinStraddle    float64         `json:"min_straddle"`
	MaxStraddle    float64         `json:"max_straddle"`
	Live           *PaperSimPoint  `json:"live,omitempty"` // valued on the market right now
	MaxPnL         float64         `json:"max_pnl"`
	MinPnL         float64         `json:"min_pnl"`
	Legs           []PaperSimLeg   `json:"legs"`
	Series         []PaperSimPoint `json:"series"`
	Hedges         []PaperSimHedge `json:"hedges"`
	Reconstructed  int             `json:"reconstructed_minutes"` // minutes priced (partly) by BS
	Notes          []string        `json:"notes,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// simMinute is one minute of stored data.
type simMinute struct {
	HHMM          int
	Time          string
	Future        float64
	ATM           float64
	IV            float64 // ATM build IV (raw, fraction)
	DTE           float64 // raw days
	TPBps         float64
	Answer        string
	lutCE, lutPE  float64
	lutK          float64
	chain         map[float64]lutChainRow
	chainATM      float64
	chainFuture   float64
	Expiry        string
	Ts            string // exact time the minute's prices were recorded
	hasLUT        bool
	hasChainSpots bool
	imported      bool // every chain row of this minute came from an import
}

// lutLoadSimDay merges the LUT minutes and the recorded chain minutes.
func lutLoadSimDay(day string) []simMinute { return lutLoadSimSet(day, "") }

// lutLoadSimSet loads a view: "" / "current" = the current expiry (LUT
// minutes + recorded / imported chain), "next" = the next weekly expiry
// (its own recorded / imported chain only).
func lutLoadSimSet(day, set string) []simMinute {
	if set == "current" {
		set = ""
	}
	byT := map[string]*simMinute{}
	get := func(t string) *simMinute {
		if m := byT[t]; m != nil {
			return m
		}
		m := &simMinute{Time: t, HHMM: lutHHMM(t)}
		byT[t] = m
		return m
	}
	var mins []LUTEvaluation
	if set == "" {
		mins, _ = lutLoadDay(day)
	}
	for _, e := range mins {
		if e.Underlying <= 0 {
			continue
		}
		m := get(e.Time)
		m.hasLUT = true
		m.Ts = e.Time + ":00 (first tick)"
		m.Future, m.lutK, m.lutCE, m.lutPE = e.Underlying, e.Strike, e.CELTP, e.PELTP
		m.ATM, m.IV, m.DTE, m.TPBps = e.Strike, e.BuildIV, e.RawDTE, e.TPBps
		switch {
		case e.Skip != "":
			m.Answer = "SKIP"
		case e.Allowed:
			m.Answer = "YES"
		default:
			m.Answer = "NO"
		}
	}
	for t, c := range lutLoadChainSet(day, set) {
		if c.Future <= 0 {
			continue
		}
		m := get(t)
		m.Expiry = c.Expiry
		if c.Ts != "" { // exact second the chain was recorded / imported
			m.Ts = c.Ts
			if c.Src != "" {
				m.Ts += " (imported)"
			}
		}
		// DTE from the chain's own expiry at this minute (session 09:15-15:40).
		if m.DTE <= 0 && c.Expiry != "" {
			if exp, err := lutParseExpiry(c.Expiry); err == nil {
				if at, err := time.ParseInLocation("2006-01-02 15:04", day+" "+t, lutIST()); err == nil {
					m.DTE = lutRawDTE(at, exp)
				}
			}
		}
		m.chain = map[float64]lutChainRow{}
		for _, r := range c.Rows {
			m.chain[r.K] = r
		}
		m.chainATM, m.chainFuture, m.hasChainSpots = c.ATM, c.Future, true
		m.imported = true
		for _, r := range c.Rows {
			if r.Src == "" {
				m.imported = false
			}
		}
		if !m.hasLUT {
			m.Future = c.Future
			m.ATM = math.Round(c.Future/lutStrikeStep) * lutStrikeStep
		}
	}
	out := make([]simMinute, 0, len(byT))
	for _, m := range byT {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HHMM < out[j].HHMM })
	// Fill IV / DTE for chain-only minutes from the ATM row (or neighbours).
	for i := range out {
		m := &out[i]
		if m.IV <= 0 && m.chain != nil {
			if r, ok := m.chain[m.ATM]; ok && r.CEIV+r.PEIV > 0 {
				m.IV = (r.CEIV + r.PEIV) / 2 / 100
			} else if ok && m.DTE > 0 {
				isCall := m.Future < m.ATM
				px := r.PE
				if isCall {
					px = r.CE
				}
				if iv, ok2 := lutImpliedVol(isCall, px, m.Future, m.ATM, math.Max(m.DTE/365, 1e-5)); ok2 {
					m.IV = iv
				}
			}
		}
		if m.DTE <= 0 || m.IV <= 0 {
			for d := 1; d < len(out); d++ {
				for _, j := range []int{i - d, i + d} {
					if j < 0 || j >= len(out) {
						continue
					}
					n := out[j]
					if m.DTE <= 0 && n.DTE > 0 {
						// raw DTE falls 1/385 per session minute (09:15-15:40)
						m.DTE = n.DTE - float64(simMinutesBetween(n.HHMM, m.HHMM))/385.0
					}
					if m.IV <= 0 && n.IV > 0 {
						m.IV = n.IV
					}
				}
				if m.DTE > 0 && m.IV > 0 {
					break
				}
			}
		}
	}
	for i := range out {
		m := &out[i]
		if m.TPBps <= 0 && m.IV > 0 && m.DTE > 0 {
			idx := int(math.Ceil(m.DTE)) - 1
			if idx < 0 {
				idx = 0
			}
			if idx > 6 {
				idx = 6
			}
			m.TPBps = lutTPBps(idx, m.IV)
		}
		if m.Answer == "" {
			m.Answer = "—"
		}
	}
	return out
}

func simMinutesBetween(a, b int) int { return (b/100*60 + b%100) - (a/100*60 + a%100) }

// simBlack76 returns price, delta, gamma (r = 0, on the future).
func simBlack76(isCall bool, F, K, T, iv float64) (float64, float64, float64) {
	if F <= 0 || K <= 0 || T <= 0 || iv <= 0 {
		return 0, 0, 0
	}
	sq := math.Sqrt(T)
	d1 := (math.Log(F/K) + 0.5*iv*iv*T) / (iv * sq)
	gamma := math.Exp(-0.5*d1*d1) / math.Sqrt(2*math.Pi) / (F * iv * sq)
	if isCall {
		return lutBSPrice(true, F, K, T, iv), lutNormCDF(d1), gamma
	}
	return lutBSPrice(false, F, K, T, iv), lutNormCDF(d1) - 1, gamma
}

// simPrice prices one leg at a minute: chain > LUT ATM LTP > BS.
func simPrice(m simMinute, K float64, opt string) (px, delta, gamma float64, src string) {
	isCall := opt == "CE"
	// Our own first-tick ATM price beats an imported (other recorder,
	// possibly mid-minute) row for the same strike.
	if r, ok := m.chain[K]; ok && r.Src != "" && m.hasLUT && K == m.lutK && m.lutCE > 0 && m.lutPE > 0 {
		goto lutATM
	}
	if r, ok := m.chain[K]; ok {
		px, delta, gamma = r.PE, r.PEDelta, r.PEGamma
		if isCall {
			px, delta, gamma = r.CE, r.CEDelta, r.CEGamma
		}
		if px > 0 {
			if delta == 0 || gamma == 0 { // greeks missing: BS at this price's IV
				T := math.Max(m.DTE/365, 1e-5)
				if iv, ok := lutImpliedVol(isCall, px, m.Future, K, T); ok {
					_, delta, gamma = simBlack76(isCall, m.Future, K, T, iv)
				}
			}
			if r.Src != "" {
				return px, delta, gamma, "import"
			}
			return px, delta, gamma, "chain"
		}
	}
lutATM:
	T := math.Max(m.DTE/365, 1e-5)
	if m.hasLUT && K == m.lutK {
		px = m.lutPE
		if isCall {
			px = m.lutCE
		}
		if px > 0 {
			iv, ok := lutImpliedVol(isCall, px, m.Future, K, T)
			if !ok {
				iv = m.IV
			}
			_, delta, gamma = simBlack76(isCall, m.Future, K, T, iv)
			return px, delta, gamma, "ltp"
		}
	}
	px, delta, gamma = simBlack76(isCall, m.Future, K, T, m.IV)
	return px, delta, gamma, "bs"
}

func simWorst(a, b string) string {
	rank := map[string]int{"chain": 0, "import": 0, "ltp": 1, "bs": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func paperDefaults(c PaperSimConfig) PaperSimConfig {
	if c.Lots <= 0 {
		c.Lots = 1
	}
	if c.SLBps <= 0 {
		c.SLBps = lutSLBps
	}
	if strings.TrimSpace(c.ExitTime) == "" {
		c.ExitTime = "15:40"
	}
	if c.StraddleDiv <= 0 {
		c.StraddleDiv = 4
	}
	if c.HedgeDiv <= 0 {
		c.HedgeDiv = 57
	}
	if c.HedgeMinBps <= 0 {
		c.HedgeMinBps = defaultHedgeMinThresholdBps
	}
	switch c.HedgeMode {
	case "synthetic", "lots", "off":
	case "":
		c.HedgeMode = "off" // legacy configs: Hedge=true meant the ATM-lots rule
		if c.Hedge {
			c.HedgeMode = "lots"
		}
	default:
		c.HedgeMode = "synthetic"
	}
	return c
}

// simGreeks prices a leg with all four greeks (per unit; theta per day,
// vega per 1 vol point), from the best price source.
func simGreeks(m simMinute, K float64, opt string) (px, delta, gamma, theta, vega, iv float64, src string) {
	px, delta, gamma, src = simPrice(m, K, opt)
	T := math.Max(m.DTE/365, 1e-5)
	iv = m.IV
	if px > 0 {
		if v, ok := lutImpliedVol(opt == "CE", px, m.Future, K, T); ok {
			iv = v
		}
	}
	if m.Future > 0 && iv > 0 {
		sq := math.Sqrt(T)
		d1 := (math.Log(m.Future/K) + 0.5*iv*iv*T) / (iv * sq)
		pdf := math.Exp(-0.5*d1*d1) / math.Sqrt(2*math.Pi)
		theta = -(m.Future * pdf * iv) / (2 * sq) / 365
		vega = m.Future * pdf * sq / 100
	}
	return
}

type simLeg struct {
	role     string // BUILD / HEDGE
	K        float64
	opt      string
	qty      int64
	avg      float64
	realized float64
}

type simBook struct {
	legs map[string]*simLeg
	// Synthetic-future hedge (fractional contracts, delta 1 each).
	futQty, futAvg, futRealized float64
}

// fillFut trades dq synthetic-future contracts (+ buy / - sell) at px.
func (b *simBook) fillFut(dq, px float64) {
	if dq == 0 {
		return
	}
	if b.futQty == 0 || (b.futQty > 0) == (dq > 0) {
		tot := math.Abs(b.futQty) + math.Abs(dq)
		b.futAvg = (b.futAvg*math.Abs(b.futQty) + px*math.Abs(dq)) / tot
		b.futQty += dq
		return
	}
	closing := math.Min(math.Abs(b.futQty), math.Abs(dq))
	if b.futQty > 0 {
		b.futRealized += (px - b.futAvg) * closing
	} else {
		b.futRealized += (b.futAvg - px) * closing
	}
	b.futQty += dq
	if math.Abs(b.futQty) < 1e-9 {
		b.futQty, b.futAvg = 0, 0
	} else if (b.futQty > 0) == (dq > 0) {
		b.futAvg = px
	}
}

func (b *simBook) fill(role string, k float64, o, side string, qn int64, px float64) {
	key := fmt.Sprintf("%s|%.0f%s", role, k, o)
	l := b.legs[key]
	if l == nil {
		l = &simLeg{role: role, K: k, opt: o}
		b.legs[key] = l
	}
	signed := qn
	if side == "SELL" {
		signed = -qn
	}
	if l.qty == 0 || (l.qty > 0) == (signed > 0) {
		tot := math.Abs(float64(l.qty)) + math.Abs(float64(signed))
		l.avg = (l.avg*math.Abs(float64(l.qty)) + px*math.Abs(float64(signed))) / tot
		l.qty += signed
		return
	}
	closing := math.Min(math.Abs(float64(l.qty)), math.Abs(float64(signed)))
	if l.qty < 0 {
		l.realized += (l.avg - px) * closing
	} else {
		l.realized += (px - l.avg) * closing
	}
	l.qty += signed
	if l.qty == 0 {
		l.avg = 0
	} else if (l.qty > 0) == (signed > 0) {
		l.avg = px
	}
}

// value marks the book on one minute.
func (b *simBook) value(m simMinute, q int64) PaperSimPoint {
	pt := PaperSimPoint{Time: m.Time, Future: m.Future, ATM: m.ATM, Source: "chain"}
	for _, l := range b.legs {
		px, d, g, th, vg, iv, src := simGreeks(m, l.K, l.opt)
		pl := l.realized
		if l.qty != 0 {
			pl += (px - l.avg) * float64(l.qty)
			pt.Source = simWorst(pt.Source, src)
		}
		if l.role == "HEDGE" {
			pt.HedgePnL += pl
			pt.HedgeQty += l.qty
		} else {
			pt.OptionPnL += pl
		}
		pt.NetDelta += float64(l.qty) * d
		pt.NetGamma += float64(l.qty) * g
		pt.NetTheta += float64(l.qty) * th
		pt.NetVega += float64(l.qty) * vg
		pt.Legs = append(pt.Legs, PaperSimLeg{Role: l.role, Strike: l.K, OptionType: l.opt, Qty: l.qty, AvgPrice: l.avg, Mark: px,
			Delta: d, Gamma: g, Theta: th, Vega: vg, IV: iv, Source: src, Realized: l.realized, PnL: pl})
	}
	if b.futQty != 0 || b.futRealized != 0 {
		fp := b.futRealized + b.futQty*(m.Future-b.futAvg)
		pt.HedgePnL += fp
		pt.NetDelta += b.futQty
		pt.HedgeFut = b.futQty
		pt.Legs = append(pt.Legs, PaperSimLeg{Role: "HEDGE", OptionType: "FUT", QtyF: b.futQty, AvgPrice: b.futAvg, Mark: m.Future,
			Delta: 1, Source: "synthetic", Realized: b.futRealized, PnL: fp})
	}
	pt.PnL = pt.OptionPnL + pt.HedgePnL
	if q > 0 {
		pt.PnLPerStraddle = pt.PnL / float64(q)
		pt.HedgeFutPerQty = b.futQty / float64(q)
	}
	sort.Slice(pt.Legs, func(i, j int) bool {
		a, c := pt.Legs[i], pt.Legs[j]
		if a.Role != c.Role {
			return a.Role < c.Role
		}
		if a.Strike != c.Strike {
			return a.Strike < c.Strike
		}
		return a.OptionType < c.OptionType
	})
	return pt
}

// RunPaperSim simulates one entry over the day's minutes. live (optional)
// is the market right now: an open position is also valued on it.
func RunPaperSim(mins []simMinute, c PaperSimConfig, lotSize int64, live *simMinute) PaperSimResult {
	c = paperDefaults(c)
	res := PaperSimResult{Config: c, Status: "NO DATA"}
	entry := lutHHMM(c.Entry)
	exitAt := lutHHMM(c.ExitTime)
	i0 := -1
	for i, m := range mins {
		if m.HHMM >= entry && m.Future > 0 {
			i0 = i
			break
		}
	}
	if i0 < 0 {
		res.Error = fmt.Sprintf("no stored minute at or after %s", c.Entry)
		return res
	}
	m0 := mins[i0]
	if gap := simMinutesBetween(entry, m0.HHMM); gap > 2 {
		res.Error = fmt.Sprintf("no stored data at %s for this expiry (first data after it: %s)", c.Entry, m0.Time)
		return res
	}
	if m0.HHMM != entry {
		res.Notes = append(res.Notes, fmt.Sprintf("no data at %s -- entered at the next stored minute %s", c.Entry, m0.Time))
	}
	if lotSize <= 0 {
		lotSize = 65
	}
	res.LotSize = lotSize
	q := int64(c.Lots) * lotSize
	K := m0.ATM
	if K <= 0 {
		K = math.Round(m0.Future/lutStrikeStep) * lutStrikeStep
	}
	ce, _, _, s1 := simPrice(m0, K, "CE")
	pe, _, _, s2 := simPrice(m0, K, "PE")
	res.EntryTime, res.LUTAnswer, res.Strike, res.Qty = m0.Time, m0.Answer, K, q
	res.CEEntry, res.PEEntry, res.EntryStraddle, res.EntryFuture = ce, pe, ce+pe, m0.Future
	res.EntrySource = simWorst(s1, s2)
	res.TPBps = c.TPBps
	if res.TPBps <= 0 {
		res.TPBps = m0.TPBps
	}
	res.SLPoints = m0.Future * c.SLBps / 10000
	res.TPPoints = m0.Future * res.TPBps / 10000
	res.MinStraddle, res.MaxStraddle = ce+pe, ce+pe

	book := &simBook{legs: map[string]*simLeg{}}
	book.fill("BUILD", K, "CE", "SELL", q, ce)
	book.fill("BUILD", K, "PE", "SELL", q, pe)

	// hedgeCheck runs the live minute-end hedge rule on a valued point.
	hedgeCheck := func(m simMinute, pt *PaperSimPoint) hedgeDecision {
		atmCE, _, _, _ := simPrice(m, m.ATM, "CE")
		atmPE, _, _, _ := simPrice(m, m.ATM, "PE")
		pt.ATMStraddle = atmCE + atmPE
		allowed := math.Min((atmCE+atmPE)/c.StraddleDiv, m.Future*m.IV/c.HedgeDiv)
		out := 0.0
		if pt.NetGamma != 0 {
			out = math.Abs(pt.NetDelta / pt.NetGamma)
		}
		d := decideHedge(hedgeDecisionInput{PointsOut: out, PointsAllowed: allowed, Spot: m.Future, NetDelta: pt.NetDelta,
			LotSize: lotSize, TradeLots: int64(c.Lots), MinThresholdBps: c.HedgeMinBps})
		pt.PointsOut, pt.PointsAllowed, pt.HedgeFloor, pt.HedgeCheck, pt.HedgeLotsNeeded = out, d.EffectiveAllowed, d.Floor, d.Action, d.Lots
		if !d.Hedge {
			pt.HedgeLotsNeeded = 0
		}
		if c.HedgeMode != "lots" {
			// Synthetic future: re-neutralise the whole delta whenever the
			// position is further out than allowed (no lot rounding).
			pt.HedgeLotsNeeded = 0
			pt.HedgeCheck = "OK"
			if out > d.EffectiveAllowed && math.Abs(pt.NetDelta) > 1e-9 {
				pt.HedgeCheck = "RISK_BREACH"
			}
			pt.HedgeTarget = (pt.HedgeFut - pt.NetDelta) / float64(q)
		}
		return d
	}
	posMarks := func(m simMinute, pt *PaperSimPoint) {
		pt.CE, _, _, _ = simPrice(m, K, "CE")
		pt.PE, _, _, _ = simPrice(m, K, "PE")
		pt.Straddle = pt.CE + pt.PE
		pt.ATMCE, _, _, _ = simPrice(m, m.ATM, "CE")
		pt.ATMPE, _, _, _ = simPrice(m, m.ATM, "PE")
		pt.ATMStraddle = pt.ATMCE + pt.ATMPE
		pt.Strike = K
		pt.QuoteTs = m.Ts
		if pt.QuoteTs == "" {
			pt.QuoteTs = m.Time
		}
	}

	res.Status = "OPEN"
	for i := i0; i < len(mins); i++ {
		m := mins[i]
		if m.Future <= 0 {
			continue
		}
		pt := book.value(m, q)
		posMarks(m, &pt)
		res.MinStraddle = math.Min(res.MinStraddle, pt.Straddle)
		res.MaxStraddle = math.Max(res.MaxStraddle, pt.Straddle)
		if pt.Source == "bs" {
			res.Reconstructed++
		}
		if i == i0 {
			pt.Event = fmt.Sprintf("ENTRY sell %.0f CE %.2f + PE %.2f = %.2f", K, ce, pe, ce+pe)
			if c.HedgeMode == "synthetic" && math.Abs(pt.NetDelta) > 1e-9 {
				// Build delta-neutral: the synthetic hedge takes the
				// straddle's entry delta at the entry future.
				d0 := pt.NetDelta
				book.fillFut(-d0, m.Future)
				after := book.value(m, q)
				posMarks(m, &after)
				res.Hedges = append(res.Hedges, PaperSimHedge{Time: m.Time, Mode: "synthetic", Kind: "ENTRY_NEUTRAL", Qty: int64(math.Round(-d0)), FutQty: -d0,
					OldHedge: 0, NewHedge: book.futQty / float64(q), CEPrice: m.Future, DeltaBefore: d0, DeltaAfter: after.NetDelta,
					Future: m.Future, Source: pt.Source})
				after.Event = fmt.Sprintf("%s; delta-neutral build: %+.4f fut per qty @%.2f (delta %+.4f -> %+.4f)", pt.Event, book.futQty/float64(q), m.Future, d0, after.NetDelta)
				pt = after
			}
			hedgeCheck(m, &pt)
			res.Series = append(res.Series, pt)
			continue
		}
		exit := ""
		if pt.PnLPerStraddle <= -m.Future*c.SLBps/10000 {
			exit = "SL"
		} else if res.TPBps > 0 && pt.PnLPerStraddle >= m.Future*res.TPBps/10000 {
			exit = "TP"
		} else if exitAt > 0 && m.HHMM >= exitAt {
			exit = "TIME"
		}
		d := hedgeCheck(m, &pt)
		if exit == "" && c.HedgeMode == "synthetic" && pt.HedgeCheck == "RISK_BREACH" {
			old := book.futQty
			dq := -pt.NetDelta // bring the whole position to delta 0
			book.fillFut(dq, m.Future)
			after := book.value(m, q)
			res.Hedges = append(res.Hedges, PaperSimHedge{Time: m.Time, Mode: "synthetic", Kind: "RISK_BREACH", Strike: 0, Qty: int64(math.Round(dq)), FutQty: dq,
				OldHedge: old / float64(q), NewHedge: book.futQty / float64(q), CEPrice: m.Future, PEPrice: 0,
				DeltaBefore: pt.NetDelta, DeltaAfter: after.NetDelta, PointsOut: pt.PointsOut, Allowed: pt.PointsAllowed, Future: m.Future, Source: pt.Source})
			ev := fmt.Sprintf("HEDGE synthetic %+.2f contracts @%.2f (%+.4f -> %+.4f per qty): delta %+.2f -> %+.2f", dq, m.Future, old/float64(q), book.futQty/float64(q), pt.NetDelta, after.NetDelta)
			keep := pt
			pt = after
			posMarks(m, &pt)
			pt.ATMStraddle, pt.PointsOut, pt.PointsAllowed, pt.HedgeFloor, pt.HedgeCheck, pt.HedgeTarget =
				keep.ATMStraddle, keep.PointsOut, keep.PointsAllowed, keep.HedgeFloor, keep.HedgeCheck, keep.HedgeTarget
			pt.Event = ev
		}
		if exit == "" && c.HedgeMode == "lots" && d.Hedge && d.Lots > 0 {
			if ceSide, peSide, ok := hedgeSidesFromSignedDelta(pt.NetDelta); ok {
				hq := d.Lots * lotSize
				cpx, _, _, cs := simPrice(m, m.ATM, "CE")
				ppx, _, _, ps := simPrice(m, m.ATM, "PE")
				book.fill("HEDGE", m.ATM, "CE", ceSide, hq, cpx)
				book.fill("HEDGE", m.ATM, "PE", peSide, hq, ppx)
				after := book.value(m, q)
				res.Hedges = append(res.Hedges, PaperSimHedge{Time: m.Time, Mode: "lots", Kind: "ATM_LOTS", Strike: m.ATM, CESide: ceSide, PESide: peSide, Lots: d.Lots, Qty: hq,
					CEPrice: cpx, PEPrice: ppx, DeltaBefore: pt.NetDelta, DeltaAfter: after.NetDelta, PointsOut: pt.PointsOut, Allowed: pt.PointsAllowed,
					Future: m.Future, Source: simWorst(cs, ps)})
				ev := fmt.Sprintf("HEDGE %d lot(s) %s CE / %s PE @%.0f: delta %+.1f -> %+.1f", d.Lots, ceSide, peSide, m.ATM, pt.NetDelta, after.NetDelta)
				keep := pt
				pt = after
				posMarks(m, &pt)
				pt.ATMStraddle, pt.PointsOut, pt.PointsAllowed, pt.HedgeFloor, pt.HedgeCheck, pt.HedgeLotsNeeded =
					keep.ATMStraddle, keep.PointsOut, keep.PointsAllowed, keep.HedgeFloor, keep.HedgeCheck, keep.HedgeLotsNeeded
				pt.Event = ev
			}
		}
		if exit != "" {
			for _, l := range book.legs {
				if l.qty == 0 {
					continue
				}
				px, _, _, _ := simPrice(m, l.K, l.opt)
				side, qn := "BUY", -l.qty
				if l.qty > 0 {
					side, qn = "SELL", l.qty
				}
				book.fill(l.role, l.K, l.opt, side, qn, px)
			}
			book.fillFut(-book.futQty, m.Future)
			fin := book.value(m, q)
			posMarks(m, &fin)
			fin.Event = strings.TrimSpace(pt.Event + " EXIT " + exit)
			fin.HedgeCheck = "EXITED"
			res.Series = append(res.Series, fin)
			res.Status, res.ExitTime, res.ExitReason = "EXITED", m.Time, exit
			break
		}
		res.Series = append(res.Series, pt)
	}
	if n := len(res.Series); n > 0 {
		last := res.Series[n-1]
		res.PnL, res.PnLPerStraddle = last.PnL, last.PnLPerStraddle
		res.OptionPnL, res.HedgePnL = last.OptionPnL, last.HedgePnL
		res.MaxPnL, res.MinPnL = last.PnL, last.PnL
		for _, p := range res.Series {
			res.MaxPnL = math.Max(res.MaxPnL, p.PnL)
			res.MinPnL = math.Min(res.MinPnL, p.PnL)
		}
		res.Legs = last.Legs
	}
	for i := range res.Series { // per-leg detail only on the result / live (payload)
		res.Series[i].Legs = nil
	}
	// Live: the open position valued on the market right now (between
	// minute ends); the hedge check shows what the next minute end would do.
	if res.Status == "OPEN" && live != nil && live.Future > 0 {
		lp := book.value(*live, q)
		posMarks(*live, &lp)
		hedgeCheck(*live, &lp) // display only: hedges are decided on minute data points, never on a live tick
		lp.HedgeCheck = "PREVIEW " + lp.HedgeCheck
		res.Live = &lp
		res.Status = "RUNNING"
	}
	if res.Reconstructed > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d minute(s) priced partly by Black-Scholes from the ATM IV (strike not ATM / chain not recorded then)", res.Reconstructed))
	}
	return res
}

// simMinuteFromChain turns the live chain into a minute for valuation.
func simMinuteFromChain(chain *OptionChainSnapshot, now time.Time) *simMinute {
	if chain == nil {
		return nil
	}
	m := &simMinute{HHMM: now.Hour()*100 + now.Minute(), Time: now.Format("15:04:05"), Ts: now.Format("15:04:05.000") + " (live)", Future: lutUnderlying(chain), chain: map[float64]lutChainRow{}}
	if m.Future <= 0 {
		return nil
	}
	m.ATM = math.Round(m.Future/lutStrikeStep) * lutStrikeStep
	for _, r := range chain.Chain {
		if r.CELtp <= 0 && r.PELtp <= 0 {
			continue
		}
		m.chain[r.Strike] = lutChainRow{K: r.Strike, CE: r.CELtp, PE: r.PELtp, CEDelta: r.CEDelta, PEDelta: r.PEDelta, CEGamma: r.CEGamma, PEGamma: r.PEGamma, CEIV: r.CEIV, PEIV: r.PEIV}
	}
	if exp, err := lutParseExpiry(chain.Expiry); err == nil {
		m.DTE = lutRawDTE(now, exp)
	}
	if r, ok := m.chain[m.ATM]; ok && m.DTE > 0 {
		T := math.Max(m.DTE/365, 1e-5)
		if iv, ok := lutImpliedVol(m.Future < m.ATM, map[bool]float64{true: r.CE, false: r.PE}[m.Future < m.ATM], m.Future, m.ATM, T); ok {
			m.IV = iv
		}
	}
	return m
}

// --- hidden simulations (removed from the view for a day) ------------------

func paperHiddenFile(day string) string { return filepath.Join(lutDataDir(), day+"_paper_hidden.json") }

func loadPaperHidden(day string) map[string]bool {
	out := map[string]bool{}
	if b, err := os.ReadFile(paperHiddenFile(day)); err == nil {
		var ids []string
		if json.Unmarshal(b, &ids) == nil {
			for _, id := range ids {
				out[id] = true
			}
		}
	}
	return out
}

func savePaperHidden(day string, h map[string]bool) error {
	ids := []string{}
	for id := range h {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, _ := json.MarshalIndent(ids, "", "  ")
	return sbWriteAtomic(paperHiddenFile(day), b)
}

// --- started paper trades (Start now) --------------------------------------

var paperMu sync.Mutex

func paperFile(day string) string { return filepath.Join(lutDataDir(), day+"_paper.json") }

func loadPaperStarts(day string) []PaperSimConfig {
	var out []PaperSimConfig
	if b, err := os.ReadFile(paperFile(day)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func savePaperStarts(day string, cs []PaperSimConfig) error {
	b, _ := json.MarshalIndent(cs, "", "  ")
	return sbWriteAtomic(paperFile(day), b)
}

// PaperActual is one real trade of the day, for comparison.
type PaperActual struct {
	TradeUID    string           `json:"trade_uid"`
	Status      string           `json:"status"`
	EntryTime   string           `json:"entry_time"`
	Strike      float64          `json:"strike"`
	CEEntry     float64          `json:"ce_entry"`
	PEEntry     float64          `json:"pe_entry"`
	Qty         int64            `json:"qty"`
	RealizedPnL float64          `json:"realized_pnl"`
	Hedges      []OrderExecution `json:"hedges"`
	Exits       []OrderExecution `json:"exits"`
	SimID       string           `json:"sim_id"`
}

func (s *Service) paperActuals(ctx context.Context, day string) []PaperActual {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return nil
	}
	from, err := time.ParseInLocation("2006-01-02", day, lutIST())
	if err != nil {
		return nil
	}
	sums, err := pg.TradeSummaries(ctx, from, from.Add(24*time.Hour))
	if err != nil {
		return nil
	}
	var out []PaperActual
	for _, t := range sums {
		a := PaperActual{TradeUID: t.TradeUID, Status: t.Status, Strike: t.Strike, RealizedPnL: t.RealizedPnL}
		var ceV, peV float64
		var ceQ, peQ int64
		for _, e := range t.Executions {
			if e.FilledQty <= 0 {
				continue
			}
			switch e.Kind {
			case "ENTRY":
				if a.EntryTime == "" {
					a.EntryTime = e.Time.In(lutIST()).Format("15:04")
				}
				if e.Leg == "CE" {
					ceV += e.AvgPrice * float64(e.FilledQty)
					ceQ += e.FilledQty
				} else if e.Leg == "PE" {
					peV += e.AvgPrice * float64(e.FilledQty)
					peQ += e.FilledQty
				}
				if a.Strike == 0 {
					a.Strike = e.Strike
				}
			case "HEDGE":
				a.Hedges = append(a.Hedges, e)
			case "EXIT":
				a.Exits = append(a.Exits, e)
			}
		}
		if a.EntryTime == "" {
			continue
		}
		if ceQ > 0 {
			a.CEEntry = ceV / float64(ceQ)
		}
		if peQ > 0 {
			a.PEEntry = peV / float64(peQ)
		}
		a.Qty = ceQ
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntryTime < out[j].EntryTime })
	return out
}

// PaperSimHandler: GET /api/paper/sim?day=&lots=&hedge=1
func (h *Handlers) PaperSimHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	day := strings.TrimSpace(q.Get("day"))
	if day == "" {
		day = sbToday()
	}
	lots := 1
	fmt.Sscanf(q.Get("lots"), "%d", &lots)
	if lots < 1 {
		lots = 1
	}
	size := 0 // >0: every simulation re-run at this many lots (size multiplier)
	fmt.Sscanf(q.Get("size"), "%d", &size)
	hedgeMode := strings.TrimSpace(q.Get("hedge"))
	switch hedgeMode {
	case "0":
		hedgeMode = "off"
	case "1", "":
		hedgeMode = "synthetic"
	}
	hedge := hedgeMode == "lots"
	// Default: think in ONE straddle (qty 1 per leg, no lots); size is a
	// plain quantity multiplier. Only the ATM-lots hedge mode uses lots.
	unit := hedgeMode != "lots"
	lotSize := int64(65)
	lutEng.mu.Lock()
	if lutEng.lotSize > 0 {
		lotSize = int64(lutEng.lotSize)
	}
	lutEng.mu.Unlock()

	view := strings.ToLower(strings.TrimSpace(q.Get("expiry")))
	if view != "next" {
		view = ""
	}
	mins := lutLoadSimSet(day, view)
	var cfgs []PaperSimConfig
	for _, t := range []string{"09:16", "09:17", "09:18", "09:19"} {
		cfgs = append(cfgs, PaperSimConfig{ID: "AUTO-" + t, Source: "sell at " + t, Entry: t, Lots: lots, Hedge: hedge, HedgeMode: hedgeMode})
	}
	paperMu.Lock()
	starts := loadPaperStarts(day)
	paperMu.Unlock()
	for _, c := range starts {
		if c.View != view {
			continue
		}
		c.Hedge, c.HedgeMode = hedge, hedgeMode
		cfgs = append(cfgs, c)
	}
	var actual []PaperActual
	if view == "" { // real trades are on the current expiry
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		actual = h.Service.paperActuals(ctx, day)
		cancel()
	}
	for i := range actual {
		id := "ACTUAL-" + actual[i].TradeUID
		actual[i].SimID = id
		l := lots
		if lotSize > 0 && actual[i].Qty > 0 {
			l = int(actual[i].Qty / lotSize)
		}
		cfgs = append(cfgs, PaperSimConfig{ID: id, Source: "actual trade " + actual[i].TradeUID, Entry: actual[i].EntryTime, Lots: l, Hedge: hedge, HedgeMode: hedgeMode})
	}
	var live *simMinute
	if day == sbToday() && h.Service.Snapshot != nil {
		lctx, lcancel := context.WithTimeout(r.Context(), 2*time.Second)
		if chain, err := h.Service.Snapshot.GetOptionChain(lctx, lutSymbol, ""); err == nil {
			if view == "next" {
				nx := lutNextExpiry(chain)
				chain = nil
				if nx != "" {
					if nc, nerr := h.Service.Snapshot.GetOptionChain(lctx, lutSymbol, nx); nerr == nil {
						chain = nc
					}
				}
			}
			live = simMinuteFromChain(chain, time.Now().In(lutIST()))
		}
		lcancel()
	}
	runLot := lotSize
	if unit {
		runLot = 1
		if size <= 0 {
			size = 1
		}
	}
	paperMu.Lock()
	hidden := loadPaperHidden(day)
	paperMu.Unlock()
	hiddenN := 0
	var sims []PaperSimResult
	for _, c := range cfgs {
		if hidden[view+"|"+c.ID] {
			hiddenN++
			continue
		}
		if size > 0 {
			c.Lots = size
		}
		sims = append(sims, RunPaperSim(mins, c, runLot, live))
	}
	src := map[string]int{}
	for _, m := range mins {
		switch {
		case m.chain != nil && m.imported && m.hasLUT:
			src["lut_atm_plus_import"]++
		case m.chain != nil && m.imported:
			src["import_only"]++
		case m.chain != nil:
			src["chain"]++
		case m.hasLUT:
			src["lut_atm_only"]++
		}
	}
	first, last := "", ""
	if len(mins) > 0 {
		first, last = mins[0].Time, mins[len(mins)-1].Time
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "day": day, "view": map[bool]string{true: "next", false: "current"}[view == "next"], "expiry": func() string {
			for i := len(mins) - 1; i >= 0; i-- {
				if mins[i].Expiry != "" {
					return mins[i].Expiry
				}
			}
			return ""
		}(), "lot_size": lotSize, "unit": unit, "qty": func() int64 {
			if unit {
				return int64(size)
			}
			return int64(size) * lotSize
		}(), "minutes": len(mins), "first": first, "last": last,
		"minute_sources": src, "sims": sims, "actual": actual, "hidden": hiddenN, "live_at": func() string {
			if live != nil {
				return live.Time
			}
			return ""
		}(),
	})
}

// PaperStartHandler: POST /api/paper/start {"entry": "HH:MM" (blank = latest
// stored minute), "lots", "sl_bps", "tp_bps", "exit_time"} -- a PAPER entry.
func (h *Handlers) PaperStartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var c PaperSimConfig
	_ = json.NewDecoder(r.Body).Decode(&c)
	day := sbToday()
	if c.View != "next" {
		c.View = ""
	}
	if strings.TrimSpace(c.Entry) == "" {
		mins := lutLoadSimSet(day, c.View)
		if len(mins) == 0 {
			lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "no stored minute today yet"})
			return
		}
		c.Entry = mins[len(mins)-1].Time
	}
	if lutHHMM(c.Entry) == 0 {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "entry must be HH:MM"})
		return
	}
	c = paperDefaults(c)
	c.ID = "PAPER-" + strings.ReplaceAll(c.Entry, ":", "") + "-" + time.Now().In(lutIST()).Format("150405")
	c.Source = "start now @ " + c.Entry
	if c.View == "next" {
		c.Source += " (next expiry)"
	}
	c.CreatedAt = time.Now().In(lutIST()).Format("15:04:05")
	paperMu.Lock()
	defer paperMu.Unlock()
	cs := append(loadPaperStarts(day), c)
	if err := savePaperStarts(day, cs); err != nil {
		lutJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	log.Printf("[PAPER] started paper trade %s at %s (lots %d, hedge %s) -- simulated, never executed", c.ID, c.Entry, c.Lots, c.HedgeMode)
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "paper": c})
}

// PaperRemoveHandler: POST /api/paper/remove {"id", "view", "day"} -- a
// Start-now paper trade is deleted; the 09:16-09:19 / actual-trade
// simulations are hidden for that day and view. {"restore": true} un-hides
// every hidden one of the view.
func (h *Handlers) PaperRemoveHandler(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID      string `json:"id"`
		View    string `json:"view"`
		Day     string `json:"day"`
		Restore bool   `json:"restore"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	id, day := strings.TrimSpace(b.ID), strings.TrimSpace(b.Day)
	if day == "" {
		day = sbToday()
	}
	view := ""
	if strings.EqualFold(strings.TrimSpace(b.View), "next") {
		view = "next"
	}
	paperMu.Lock()
	defer paperMu.Unlock()
	if b.Restore || !strings.HasPrefix(id, "PAPER-") {
		hd := loadPaperHidden(day)
		if b.Restore {
			for k := range hd {
				if strings.HasPrefix(k, view+"|") {
					delete(hd, k)
				}
			}
			log.Printf("[PAPER] restored hidden simulations (day %s, view %q)", day, view)
		} else if id != "" {
			hd[view+"|"+id] = true
			log.Printf("[PAPER] hid simulation %s (day %s, view %q)", id, day, view)
		}
		if err := savePaperHidden(day, hd); err != nil {
			lutJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}
	cs := loadPaperStarts(day)
	out := cs[:0]
	for _, c := range cs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	log.Printf("[PAPER] removed paper trade %s", id)
	if err := savePaperStarts(day, out); err != nil {
		lutJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}
