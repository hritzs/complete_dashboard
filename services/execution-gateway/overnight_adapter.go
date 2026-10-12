package main

// The only link between the overnight module (internal/overnight) and this
// gateway: an adapter that lets the module use the gateway's existing
// GreekSoft session -- no second login, no second socket. When the module
// moves to its own project, this file is what gets replaced.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	greeksoftbroker "execution-gateway/internal/brokers/greeksoft"
	"execution-gateway/internal/overnight"
	"execution-gateway/internal/trading"
)

var overnightSvc *overnight.Service

func overnightHandler(w http.ResponseWriter, r *http.Request) {
	if overnightSvc == nil {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusForbidden)
		}
		_, _ = w.Write([]byte(`{"enabled":false,"error":"Overnight is switched off (set OVERNIGHT_ENABLED=1 and restart)"}`))
		return
	}
	if r.URL.Query().Get("probe") != "" { // the UI asking whether to show the tab
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true}`))
		return
	}
	overnightSvc.Handler(w, r)
}

type overnightBroker struct {
	factory trading.BrokerFactory
	symbol  string
}

func overnightAccount() string {
	for _, k := range []string{"OVERNIGHT_ACCOUNT_ID", "SBUILD_ACCOUNT_ID", "GREEK_ACCOUNT_ID"} {
		if a := strings.TrimSpace(os.Getenv(k)); a != "" {
			return a
		}
	}
	return "147"
}

func (b *overnightBroker) exec() (*greeksoftbroker.Executor, error) {
	user := strings.TrimSpace(os.Getenv("OVERNIGHT_USER_ID"))
	if user == "" {
		user = "U001"
	}
	e, err := b.factory.GetExecutor(user, "GREEKSOFT", overnightAccount())
	if err != nil {
		return nil, err
	}
	g, ok := e.(*greeksoftbroker.Executor)
	if !ok || g.Client == nil {
		return nil, fmt.Errorf("GreekSoft executor not available")
	}
	return g, nil
}

func (b *overnightBroker) Place(ctx context.Context, r overnight.PlaceReq) (string, error) {
	e, err := b.exec()
	if err != nil {
		return "", err
	}
	px := r.Price
	// IntentID goes to the broker as "tag" (unique per order), TradeUID as
	// "userTag" (one per overnight position).
	res, err := e.ExecuteOrderIntent(ctx, trading.OrderIntent{
		IntentID: r.Tag, OrderUID: r.Tag, TradeUID: r.UserTag, Token: r.Token, Symbol: b.symbol,
		ExchangeSegment: trading.ResolveExchangeSegment(b.symbol, ""), Side: r.Side, Quantity: r.Qty, LotSize: r.LotSize,
		OrderType: "LIMIT", LimitPrice: &px, ProductType: "NRML", Phase: "OVERNIGHT",
		BrokerName: "GREEKSOFT", AccountID: overnightAccount(),
	})
	if err != nil {
		return "", err
	}
	if res == nil || strings.TrimSpace(res.BrokerOrderID) == "" {
		reason := "no order id returned"
		if res != nil && res.RawResponse != "" {
			reason = res.RawResponse
		}
		return "", fmt.Errorf("broker did not accept the order: %s", reason)
	}
	return strings.TrimSpace(res.BrokerOrderID), nil
}

func (b *overnightBroker) Modify(ctx context.Context, orderID string, price float64, qty, lotSize int64) error {
	e, err := b.exec()
	if err != nil {
		return err
	}
	return e.ModifyOrderPrice(ctx, orderID, price, qty, int(lotSize))
}

func (b *overnightBroker) Cancel(ctx context.Context, orderID string) error {
	e, err := b.exec()
	if err != nil {
		return err
	}
	return e.CancelOrder(ctx, orderID)
}

// Orders reads the broker's REST order book: filled quantity, average
// traded price and the rejection text all come from it.
func (b *overnightBroker) Orders(ctx context.Context) ([]overnight.Order, error) {
	e, err := b.exec()
	if err != nil {
		return nil, err
	}
	book, err := e.BookOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]overnight.Order, 0, len(book))
	for _, o := range book {
		status := "OPEN"
		switch o.Status {
		case "FILLED", "REJECTED", "CANCELLED":
			status = o.Status
		}
		out = append(out, overnight.Order{ID: o.ID, Status: status, Qty: o.Qty, Filled: o.Filled, Price: o.Price, AvgFill: o.AvgFill,
			Reason: strings.TrimSpace(o.Raw + " " + o.Reason)})
	}
	return out, nil
}

// FillPrice: the order book already carries the average traded price
// (Order.AvgFill), so there is nothing more to ask.
func (b *overnightBroker) FillPrice(context.Context, string) (float64, bool) { return 0, false }

func (b *overnightBroker) Positions(ctx context.Context) (map[int64]int64, error) {
	e, err := b.exec()
	if err != nil {
		return nil, err
	}
	pos, err := e.GetBrokerPositions(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	for _, p := range pos {
		out[p.ExchToken] += p.NetQty
	}
	return out, nil
}

func (b *overnightBroker) MinuteCloses(ctx context.Context, token int64, date string) (map[int64]float64, error) {
	e, err := b.exec()
	if err != nil {
		return nil, err
	}
	return e.MinuteCloses(ctx, token, date)
}

func (b *overnightBroker) Limits(ctx context.Context, token int64) (int64, int64, error) {
	e, err := b.exec()
	if err != nil {
		return 0, 0, err
	}
	f, err := e.GetFreezeQty(ctx, token)
	if err != nil {
		return 0, 0, err
	}
	return f.LotSize, f.MaxOrderQty, nil
}

// overnightEnabled: OVERNIGHT_ENABLED=0 switches the whole module off (no
// minute-end computation, no subscriptions, tab hidden). On by default.
func overnightEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OVERNIGHT_ENABLED")))
	return !(v == "0" || v == "false" || v == "no" || v == "off")
}

func startOvernight(ctx context.Context, factory trading.BrokerFactory) {
	if !overnightEnabled() {
		log.Printf("[OVERNIGHT] switched off (OVERNIGHT_ENABLED=0)")
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(os.Getenv("OVERNIGHT_SYMBOL")))
	if symbol == "" {
		symbol = "NIFTY"
	}
	overnightSvc = overnight.New(&overnightBroker{factory: factory, symbol: symbol})
	trading.ExternalHoldings = overnightSvc.HeldQty // keeps the intraday position check quiet about carried legs
	overnightSvc.Start(ctx)
	log.Printf("[OVERNIGHT] module started (%s, account %s)", symbol, overnightAccount())
}
