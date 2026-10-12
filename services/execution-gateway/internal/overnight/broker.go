// Package overnight is the positional (carried overnight) NIFTY monthly
// module: delta-picked CE / PE bought over a set time, carried for days,
// exited / partially exited / rolled in full-size chunks.
//
// It is self-contained on purpose (own state, own files, own HTTP handler,
// no import of the intraday trading package) so the folder can be lifted
// into a separate project. Everything it knows about the market comes from
// GreekSoft: 1-minute candle closes over REST for every minute-end number,
// and the bridge's Apollo websocket ticks for live display and order prices.
package overnight

import (
	"context"
	"time"
)

// Order is one row of the broker's order book, reduced to what the engine
// needs to know whether its order is done.
type Order struct {
	ID      string
	Status  string // OPEN | FILLED | REJECTED | CANCELLED
	Qty     int64
	Filled  int64
	Price   float64 // the order's limit price
	Reason  string  // broker's rejection text
	AvgFill float64 // average traded price when the book carries it (else 0)
}

// PlaceReq is one LIMIT order. Tag is unique per order, UserTag is the same
// for every order of one overnight position: together they separate these
// orders from every other strategy in the broker's order book.
type PlaceReq struct {
	Tag, UserTag string
	Token        int64 // exchange token
	Side         string
	Qty, LotSize int64
	Price        float64
}

// Broker is everything the module asks of the broker session. The gateway
// passes an adapter over its existing GreekSoft session (no second login).
type Broker interface {
	Place(ctx context.Context, r PlaceReq) (orderID string, err error)
	Modify(ctx context.Context, orderID string, price float64, qty, lotSize int64) error
	Cancel(ctx context.Context, orderID string) error
	Orders(ctx context.Context) ([]Order, error)
	// FillPrice is the average traded price of one order, when the broker
	// can tell; ok=false means "use the limit price".
	FillPrice(ctx context.Context, orderID string) (price float64, ok bool)
	// Positions: net quantity per exchange token (signed).
	Positions(ctx context.Context) (map[int64]int64, error)
	// MinuteCloses: the day's 1-minute candle closes keyed by the bar's
	// unix time (a finished bar is stamped at its end). date is YYYYMMDD.
	MinuteCloses(ctx context.Context, token int64, date string) (map[int64]float64, error)
	// Limits: lot size and the largest quantity one order may carry.
	Limits(ctx context.Context, token int64) (lotSize, maxOrderQty int64, err error)
}

func ist() *time.Location {
	if loc, err := time.LoadLocation("Asia/Kolkata"); err == nil {
		return loc
	}
	return time.FixedZone("IST", 5*3600+1800)
}

// inSession: the broker takes orders 09:15-15:40 IST on weekdays.
func inSession(now time.Time) bool {
	now = now.In(ist())
	if wd := now.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	hm := now.Hour()*100 + now.Minute()
	return hm >= 915 && hm < 1540
}

// inMarket: candles form 09:15-15:30 IST.
func inMarket(now time.Time) bool {
	now = now.In(ist())
	if wd := now.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	hm := now.Hour()*100 + now.Minute()
	return hm >= 915 && hm <= 1530
}
