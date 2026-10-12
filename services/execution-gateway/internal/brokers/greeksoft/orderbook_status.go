package greeksoft

import (
	"context"
	"fmt"
	"strconv"

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
	// The typed book: trdQty is the filled quantity (the generic walk used
	// before looked for traded_qty / filledQty and always read 0).
	book, err := e.Client.GetOrderBookTyped(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("get Greeksoft order book: %w", err)
	}
	out := make([]trading.BrokerFill, 0, len(book.Data))
	for _, o := range book.Data {
		if o.OrdID <= 0 {
			continue
		}
		status := normalizeGreeksoftOrderStatus(o.Status)
		if o.Qty > 0 && o.TrdQty >= o.Qty {
			status = "FILLED"
		}
		// The book has no average traded price; a filled LIMIT order's own
		// price is the closest it carries (worst case for the fill).
		avg := 0.0
		if o.TrdQty > 0 {
			avg = o.Price
		}
		out = append(out, trading.BrokerFill{
			BrokerOrderID: strconv.FormatInt(o.OrdID, 10), BrokerToken: o.Token,
			FilledQty: o.TrdQty, AveragePrice: avg, Status: status,
			OptionType: o.OptionType, Strike: o.StrikePrice, Symbol: o.TradeSymbol,
		})
	}
	return out, nil
}
