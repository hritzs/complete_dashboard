package greeksoft

import (
	"context"
	"fmt"

	"execution-gateway/internal/trading"
)

var _ trading.OrderBookProvider = (*Executor)(nil)

// OrderBookOrders returns EVERY order of the broker's REST order book with
// its current status (OPEN / FILLED / PARTIALLY_FILLED / CANCELLED /
// REJECTED ...), filled quantity and average price -- unlike
// GetVerifiedFills, which only knows the platform's own trades (reconciler
// DB) and only filled orders. Used for ad-hoc orders (the Mock tab).
func (e *Executor) OrderBookOrders(ctx context.Context) ([]trading.BrokerFill, error) {
	if e == nil || e.Client == nil {
		return nil, fmt.Errorf("greeksoft executor client is nil")
	}
	book, err := e.Client.GetOrderBook(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Greeksoft order book: %w", err)
	}
	var out []trading.BrokerFill
	seen := map[string]struct{}{}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch node := v.(type) {
		case map[string]interface{}:
			id := firstGreeksoftOrderID(node, "gorderid", "gOrderID", "gOrderId", "ordID", "orderId", "orderID", "OrderID")
			if id != "" && id != "0" {
				if _, dup := seen[id]; !dup {
					seen[id] = struct{}{}
					out = append(out, trading.BrokerFill{
						BrokerOrderID: id,
						Status: normalizeGreeksoftOrderStatus(firstStringValue(node,
							"OrderStatus", "orderStatus", "order_status", "status", "Status", "ordStatus")),
						FilledQty: firstInt64Value(node,
							"traded_qty", "tradedQty", "TradedQty", "filled_qty", "filledQty", "FilledQty", "fill_quantity"),
						AveragePrice: firstFloat64Value(node,
							"AvgTrdPrice", "avgTrdPrice", "avg_traded_price", "average_price", "avgPrice"),
						Side: normalizeGreeksoftSide(firstStringValue(node, "side", "Side", "transactionType")),
					})
				}
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
