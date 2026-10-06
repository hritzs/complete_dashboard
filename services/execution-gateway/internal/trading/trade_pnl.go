package trading

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// OrderExecution is one order of a trade as executed at the broker.
type OrderExecution struct {
	OrderID       int64     `json:"order_id"`
	Time          time.Time `json:"time"`
	Kind          string    `json:"kind"` // ENTRY | HEDGE | EXIT | WING | OTHER
	Leg           string    `json:"leg"`  // CE | PE
	Strike        float64   `json:"strike"`
	Side          string    `json:"side"`
	Quantity      int64     `json:"quantity"`
	FilledQty     int64     `json:"filled_qty"`
	AvgPrice      float64   `json:"avg_price"`
	Status        string    `json:"status"`
	BrokerOrderID string    `json:"broker_order_id"`

	intentID string
	phase    string
	token    int64
}

// legKey identifies one instrument for PnL accounting: option type plus
// token. Keying on option type alone pooled an ATM hedge (a different
// strike's CE/PE) into the straddle's own CE/PE average cost, which
// misstates realized PnL whenever the two aren't both flat. Token 0
// (legacy rows, tests) falls back to the plain option type.
func legKey(leg string, token int64) string {
	if token <= 0 {
		return leg
	}
	return leg + ":" + strconv.FormatInt(token, 10)
}

// LegPnL is the average-cost accounting of one option leg of a trade.
type LegPnL struct {
	Leg         string  `json:"leg"`
	Token       int64   `json:"token,omitempty"`
	Strike      float64 `json:"strike,omitempty"`
	SoldQty     int64   `json:"sold_qty"`
	BoughtQty   int64   `json:"bought_qty"`
	SoldAvg     float64 `json:"sold_avg"`
	BoughtAvg   float64 `json:"bought_avg"`
	NetShortQty int64   `json:"net_short_qty"`
	Realized    float64 `json:"realized_pnl"`
}

// classifyExecutionKind says what an order was for. It prefers the persisted
// phase (BUILD/HEDGE/SQF/PSQF, written since intents started carrying it) and
// falls back to the intent id's shape for older rows, whose phase column is
// the "PRIMARY" default: BUI_... entry, HDG... hedge, ...SQF... exit.
func classifyExecutionKind(phase, intentID string) string {
	switch strings.ToUpper(strings.TrimSpace(phase)) {
	case "BUILD":
		return "ENTRY"
	case "HEDGE":
		return "HEDGE"
	case "SQF", "PSQF":
		return "EXIT"
	case wingPhase:
		return "WING"
	}
	id := strings.ToUpper(strings.TrimSpace(intentID))
	switch {
	case strings.HasPrefix(id, "BUI"):
		return "ENTRY"
	case strings.HasPrefix(id, "HDG"):
		return "HEDGE"
	case strings.Contains(id, "SQF"):
		return "EXIT"
	}
	return "OTHER"
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// computeLegPnL aggregates executions per option leg. Only orders that
// actually filled at a known price count.
//
// This is computed from the ORDERS table on purpose: a fill is currently
// written twice (once by the reconciler under the exchange trade id, once by
// the gateway under GREEKSOFT:<order id>), so summing the fills table
// doubles quantities and PnL. Each order row carries its own filled
// quantity and average price, so nothing is counted twice.
func computeLegPnL(execs []OrderExecution) map[string]*LegPnL {
	return computeLegPnLWhere(execs, func(e OrderExecution) bool { return !isWingExecution(e) })
}

// computeWingLegPnL is computeLegPnL for the wing orders only -- reported
// separately, never part of the trade's realized PnL.
func computeWingLegPnL(execs []OrderExecution) map[string]*LegPnL {
	return computeLegPnLWhere(execs, isWingExecution)
}

func isWingExecution(e OrderExecution) bool {
	return strings.EqualFold(strings.TrimSpace(e.phase), wingPhase)
}

// pnlPerStraddle is PnL per unit of the trade's original size (lots x lot
// size), the same normalization the live monitor uses. 0 if size unknown.
func pnlPerStraddle(pnl float64, lots, lotSize int) float64 {
	if lots <= 0 || lotSize <= 0 {
		return 0
	}
	return round2(pnl / float64(lots*lotSize))
}

func computeLegPnLWhere(execs []OrderExecution, keep func(OrderExecution) bool) map[string]*LegPnL {
	type acc struct {
		leg         string
		token       int64
		strike      float64
		buyQ, sellQ int64
		buyV, sellV float64
	}
	legs := map[string]*acc{}
	for _, e := range execs {
		if e.FilledQty <= 0 || e.AvgPrice <= 0 || e.Leg == "" {
			continue
		}
		if !keep(e) {
			continue
		}
		k := legKey(e.Leg, e.token)
		a := legs[k]
		if a == nil {
			a = &acc{leg: e.Leg, token: e.token, strike: e.Strike}
			legs[k] = a
		}
		switch strings.ToUpper(e.Side) {
		case "BUY":
			a.buyQ += e.FilledQty
			a.buyV += float64(e.FilledQty) * e.AvgPrice
		case "SELL":
			a.sellQ += e.FilledQty
			a.sellV += float64(e.FilledQty) * e.AvgPrice
		}
	}

	out := map[string]*LegPnL{}
	for k, a := range legs {
		p := &LegPnL{Leg: a.leg, Token: a.token, Strike: a.strike, SoldQty: a.sellQ, BoughtQty: a.buyQ, NetShortQty: a.sellQ - a.buyQ}
		if a.sellQ > 0 {
			p.SoldAvg = round2(a.sellV / float64(a.sellQ))
		}
		if a.buyQ > 0 {
			p.BoughtAvg = round2(a.buyV / float64(a.buyQ))
		}
		if matched := minInt64(a.sellQ, a.buyQ); matched > 0 {
			p.Realized = round2((a.sellV/float64(a.sellQ) - a.buyV/float64(a.buyQ)) * float64(matched))
		}
		out[k] = p
	}
	return out
}

func totalRealizedPnL(legs map[string]*LegPnL) float64 {
	var t float64
	for _, l := range legs {
		t += l.Realized
	}
	return round2(t)
}

func closeReasonForStatus(status string) string {
	switch status {
	case "CLOSED_SL":
		return "STOP LOSS"
	case "CLOSED_TP":
		return "TAKE PROFIT"
	case "CLOSED_TIME":
		return "TIME EXIT"
	case "CLOSEDSQF", "CLOSED_SQF", "CLOSED", "CLOSED_MANUAL":
		return "MANUAL SQUARE-OFF"
	case sbStatusClosed:
		return "STRADDLE BUILD EXIT"
	}
	return ""
}
