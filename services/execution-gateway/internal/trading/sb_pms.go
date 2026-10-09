package trading

// PMS for the straddle-target build: the position is ONLY what OMS has
// verified as filled, applied lot by lot as each fill is confirmed. Every
// decision (next tranche, hedge, SL/TP/time, exit) reads from here.

import (
	"math"
	"sort"
	"time"
)

// PMSFill is one verified fill pushed by OMS.
type PMSFill struct {
	Time       string  `json:"time"`
	Token      int64   `json:"token"`
	Strike     float64 `json:"strike"`
	OptionType string  `json:"option_type"` // CE / PE
	Side       string  `json:"side"`        // SELL / BUY
	Qty        int64   `json:"qty"`
	Price      float64 `json:"price"`
	Role       string  `json:"role"` // BUILD / HEDGE / EXIT
	Tranche    int     `json:"tranche"`
}

// PMSLeg is the net position on one token.
type PMSLeg struct {
	Token      int64   `json:"token"`
	Strike     float64 `json:"strike"`
	OptionType string  `json:"option_type"`
	Qty        int64   `json:"qty"` // signed: short < 0
	AvgPrice   float64 `json:"avg_price"`
	Realized   float64 `json:"realized"`
	Mark       float64 `json:"mark"`
	Delta      float64 `json:"delta"` // option delta (per unit)
	Gamma      float64 `json:"gamma"`
	PnL        float64 `json:"pnl"` // realized + unrealized
}

// PMSView is the position valued on one chain snapshot.
type PMSView struct {
	Legs            []PMSLeg `json:"legs"`
	BuildCE         int64    `json:"build_ce"` // contracts sold by the build
	BuildPE         int64    `json:"build_pe"`
	BuildAvgCE      float64  `json:"build_avg_ce"` // average CE sold by the build
	BuildAvgPE      float64  `json:"build_avg_pe"`
	BuildStraddle   float64  `json:"build_straddle"`   // avg CE + avg PE (what the build sold the straddle at)
	FilledStraddles float64  `json:"filled_straddles"` // (build CE + build PE) / 2
	NetDelta        float64  `json:"net_delta"`
	NetGamma        float64  `json:"net_gamma"`
	PnL             float64  `json:"pnl"`
	PnLPerStraddle  float64  `json:"pnl_per_straddle"` // per FILLED straddle (not the target)
	Flat            bool     `json:"flat"`
	MissingMarks    int      `json:"missing_marks"`
}

// PMS is the position book (not safe for concurrent use; the runner owns it).
type PMS struct {
	legs    map[int64]*PMSLeg
	fills   []PMSFill
	buildCE int64
	buildPE int64
	ceValue float64 // build SELL value (qty x price)
	peValue float64
}

func NewPMS() *PMS { return &PMS{legs: map[int64]*PMSLeg{}} }

// ApplyFill books one verified fill (average cost; realized on reduction).
func (p *PMS) ApplyFill(f PMSFill) {
	if f.Qty <= 0 || f.Price <= 0 {
		return
	}
	if f.Time == "" {
		f.Time = time.Now().In(lutIST()).Format("15:04:05.000")
	}
	p.fills = append(p.fills, f)
	l := p.legs[f.Token]
	if l == nil {
		l = &PMSLeg{Token: f.Token, Strike: f.Strike, OptionType: f.OptionType}
		p.legs[f.Token] = l
	}
	signed := f.Qty
	if f.Side == "SELL" {
		signed = -f.Qty
	}
	switch {
	case l.Qty == 0 || (l.Qty > 0) == (signed > 0): // open / add
		total := math.Abs(float64(l.Qty)) + math.Abs(float64(signed))
		l.AvgPrice = (l.AvgPrice*math.Abs(float64(l.Qty)) + f.Price*math.Abs(float64(signed))) / total
		l.Qty += signed
	default: // reduce (and possibly flip)
		closing := int64(math.Min(math.Abs(float64(l.Qty)), math.Abs(float64(signed))))
		if l.Qty < 0 {
			l.Realized += (l.AvgPrice - f.Price) * float64(closing)
		} else {
			l.Realized += (f.Price - l.AvgPrice) * float64(closing)
		}
		l.Qty += signed
		if l.Qty == 0 {
			l.AvgPrice = 0
		} else if (l.Qty > 0) == (signed > 0) { // flipped
			l.AvgPrice = f.Price
		}
	}
	if f.Role == "BUILD" && f.Side == "SELL" {
		if f.OptionType == "CE" {
			p.buildCE += f.Qty
			p.ceValue += float64(f.Qty) * f.Price
		} else {
			p.buildPE += f.Qty
			p.peValue += float64(f.Qty) * f.Price
		}
	}
}

// BuildAverages returns the average price the build sold each leg at
// (0 for a leg with no build fills yet).
func (p *PMS) BuildAverages() (ce, pe float64) {
	if p.buildCE > 0 {
		ce = p.ceValue / float64(p.buildCE)
	}
	if p.buildPE > 0 {
		pe = p.peValue / float64(p.buildPE)
	}
	return ce, pe
}

// Fills returns a copy of every verified fill.
func (p *PMS) Fills() []PMSFill { return append([]PMSFill(nil), p.fills...) }

// Marks returns the last known mark / greeks of every leg (saved with the run).
func (p *PMS) Marks() []PMSLeg {
	out := make([]PMSLeg, 0, len(p.legs))
	for _, l := range p.legs {
		out = append(out, PMSLeg{Token: l.Token, Mark: l.Mark, Delta: l.Delta, Gamma: l.Gamma})
	}
	return out
}

// SetMarks restores saved marks / greeks after the fills were replayed.
func (p *PMS) SetMarks(ms []PMSLeg) {
	for _, m := range ms {
		if l := p.legs[m.Token]; l != nil {
			l.Mark, l.Delta, l.Gamma = m.Mark, m.Delta, m.Gamma
		}
	}
}

// View values the position on a chain: shorts marked at the ask (cost to
// buy back), longs at the bid; greeks from the chain row of each token.
func (p *PMS) View(chain *OptionChainSnapshot) PMSView {
	v := PMSView{BuildCE: p.buildCE, BuildPE: p.buildPE, Flat: true}
	v.BuildAvgCE, v.BuildAvgPE = p.BuildAverages()
	if v.BuildAvgCE > 0 && v.BuildAvgPE > 0 {
		v.BuildStraddle = v.BuildAvgCE + v.BuildAvgPE
	}
	v.FilledStraddles = float64(p.buildCE+p.buildPE) / 2
	for _, l := range p.legs {
		c := *l
		if c.Qty != 0 {
			v.Flat = false
		}
		var row *OptionChainRow
		isCE := false
		if chain != nil {
			row, isCE = lutRowForToken(chain, c.Token)
		}
		if row != nil {
			ltp, bid, ask := row.PELtp, row.PEBid, row.PEAsk
			c.Delta, c.Gamma = row.PEDelta, row.PEGamma
			if isCE {
				ltp, bid, ask = row.CELtp, row.CEBid, row.CEAsk
				c.Delta, c.Gamma = row.CEDelta, row.CEGamma
			}
			switch {
			case c.Qty < 0 && ask > 0:
				c.Mark = ask
			case c.Qty > 0 && bid > 0:
				c.Mark = bid
			default:
				c.Mark = ltp
			}
			// Remember them: once the ATM moves far enough this strike can
			// leave the chain window, and the leg still counts.
			if c.Mark > 0 {
				l.Mark = c.Mark
			}
			l.Delta, l.Gamma = c.Delta, c.Gamma
		} else if c.Qty != 0 {
			v.MissingMarks++
			if c.Mark <= 0 { // last known mark/greeks, else cost
				c.Mark = c.AvgPrice
			}
		}
		c.PnL = c.Realized
		if c.Qty != 0 {
			c.PnL += (c.Mark - c.AvgPrice) * float64(c.Qty)
		}
		v.NetDelta += float64(c.Qty) * c.Delta
		v.NetGamma += float64(c.Qty) * c.Gamma
		v.PnL += c.PnL
		v.Legs = append(v.Legs, c)
	}
	if v.FilledStraddles > 0 {
		v.PnLPerStraddle = v.PnL / v.FilledStraddles
	}
	sort.Slice(v.Legs, func(i, j int) bool {
		if v.Legs[i].Strike != v.Legs[j].Strike {
			return v.Legs[i].Strike < v.Legs[j].Strike
		}
		return v.Legs[i].OptionType < v.Legs[j].OptionType
	})
	return v
}

// lutRowForToken finds the chain row holding token (isCE tells which leg).
func lutRowForToken(chain *OptionChainSnapshot, token int64) (*OptionChainRow, bool) {
	for i := range chain.Chain {
		r := &chain.Chain[i]
		if r.CEToken == token {
			return r, true
		}
		if r.PEToken == token {
			return r, false
		}
	}
	return nil, false
}
