package greeksoft

import (
	"context"
	"fmt"
	"strings"

	"execution-gateway/internal/trading"
)

var _ trading.OrderBookProvider = (*Executor)(nil)

// BookOrder is one row of the broker's REST order book. Field names as
// the broker sends them (captured live 2026-10-12): gorderid, order_status
// ("Traded" / "Pending" / "Rms Rejected" ...), qty, traded_qty, price,
// AvgTrdPrice, reason, userTag (= the "tag" we sent with the order; the
// book's own "tag" field comes back empty).
type BookOrder struct {
	ID      string
	Status  string // normalised: OPEN / FILLED / PARTIALLY_FILLED / REJECTED / CANCELLED ...
	Raw     string // the broker's own status text
	Side    string
	Qty     int64
	Filled  int64
	Price   float64
	AvgFill float64
	Reason  string
	Tag     string
	Token   int64 // exchange token
	Symbol  string
}

// BookOrders reads the whole order book of the day.
func (e *Executor) BookOrders(ctx context.Context) ([]BookOrder, error) {
	if e == nil || e.Client == nil {
		return nil, fmt.Errorf("greeksoft executor client is nil")
	}
	book, err := e.Client.GetOrderBook(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Greeksoft order book: %w", err)
	}
	var out []BookOrder
	seen := map[string]struct{}{}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch node := v.(type) {
		case map[string]interface{}:
			id := firstGreeksoftOrderID(node, "gorderid", "gOrderID", "gOrderId", "ordID", "orderId", "orderID", "OrderID")
			if id != "" && id != "0" {
				if _, dup := seen[id]; dup {
					return
				}
				seen[id] = struct{}{}
				raw := firstStringValue(node, "order_status", "OrderStatus", "orderStatus", "status", "Status", "ordStatus")
				o := BookOrder{
					ID: id, Raw: raw, Status: normalizeGreeksoftOrderStatus(raw),
					Side:    normalizeGreeksoftSide(firstStringValue(node, "side", "Side", "transactionType")),
					Qty:     firstInt64Value(node, "qty", "Qty", "quantity"),
					Filled:  firstInt64Value(node, "traded_qty", "tradedQty", "TradedQty", "trdQty", "filled_qty", "filledQty"),
					Price:   firstFloat64Value(node, "price", "Price"),
					AvgFill: firstFloat64Value(node, "AvgTrdPrice", "avgTrdPrice", "avg_traded_price", "average_price", "avgPrice"),
					Reason:  strings.TrimSpace(firstStringValue(node, "reason", "Reason") + " " + firstStringValue(node, "remarks", "Remarks")),
					Tag:     firstStringValue(node, "userTag", "UserTag"),
					Token:   firstInt64Value(node, "exch_Token", "exch_token", "exchToken"),
					Symbol:  firstStringValue(node, "symbol", "tradeSymbol"),
				}
				if o.Qty > 0 && o.Filled >= o.Qty {
					o.Status = "FILLED"
				}
				out = append(out, o)
				return
			}
			for _, child := range node {
				walk(child)
			}
		case []interface{}:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(book)
	return out, nil
}

// OrderBookOrders returns EVERY order of the broker's REST order book with
// its current status, filled quantity and average traded price -- unlike
// GetVerifiedFills, which only knows the platform's own trades (reconciler
// DB) and only filled orders. Used for ad-hoc orders (the Mock tab).
func (e *Executor) OrderBookOrders(ctx context.Context) ([]trading.BrokerFill, error) {
	orders, err := e.BookOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]trading.BrokerFill, 0, len(orders))
	for _, o := range orders {
		out = append(out, trading.BrokerFill{BrokerOrderID: o.ID, Token: o.Token, Side: o.Side, FilledQty: o.Filled,
			AveragePrice: o.AvgFill, Status: o.Status, BrokerUserTag: o.Tag, TradeSymbol: o.Symbol})
	}
	return out, nil
}
