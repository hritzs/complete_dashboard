package trading

import "context"

// ExecutionResult represents the outcome of executing a single order intent.
type ExecutionResult struct {
	IntentID      string  `json:"intent_id"`
	BrokerOrderID string  `json:"broker_order_id"`
	Status        string  `json:"status"`
	FilledQty     int64   `json:"filled_qty"`
	FillPrice     float64 `json:"fill_price"`
	EventReason   string  `json:"event_reason"`
	LatencyMS     int64   `json:"latency_ms"`
	RawRequest    string  `json:"raw_request"`
	RawResponse   string  `json:"raw_response"`
}

type Store interface {
	SaveTrade(trade StoredTrade)
	UpdateTrade(trade StoredTrade)
	DeleteTrade(tradeUID string)
	LoadTrade(tradeUID string) (StoredTrade, bool)
	AllTrades() []StoredTrade

	AppendIntent(tradeUID string, intent OrderIntent)
	LoadIntents(tradeUID string) []StoredIntent

	SaveSnapshot(snapshot TradeSnapshot)
	LoadSnapshot(tradeUID string) (TradeSnapshot, bool)

	SaveRuntime(rt *RuntimeTrade)
	LoadRuntime(tradeUID string) (*RuntimeTrade, bool)
	DeleteRuntime(tradeUID string)
}

type SnapshotProvider interface {
	GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error)
	PushSnapshot(ctx context.Context, snap TradeSnapshot) error
}

type LotSizeProvider interface {
	GetLotSize(ctx context.Context, symbol, expiry string) (int, error)
}

type Executor interface {
	ExecuteOrderIntent(ctx context.Context, intent OrderIntent) (*ExecutionResult, error)
}

// OrderModifier and OrderCanceller are optional broker capabilities. The
// build's leftover-quantity chase uses them and does nothing (leaves the
// order as it was) when an executor lacks either.
type OrderModifier interface {
	// ModifyOrderPrice re-prices a resting LIMIT order in place, keeping its
	// quantity, so no second order exists that could also fill.
	ModifyOrderPrice(ctx context.Context, brokerOrderID string, price float64, quantity int64, lotSize int) error
}

type OrderCanceller interface {
	CancelOrder(ctx context.Context, brokerOrderID string) error
}

// OrderBookProvider lists every order of the broker's order book with its
// current status / filled quantity / average price (ad-hoc orders that are
// not part of a platform trade: the Mock tab).
type OrderBookProvider interface {
	OrderBookOrders(ctx context.Context) ([]BrokerFill, error)
}

// FreezeInfo is a contract's order-size limits as reported live by the
// broker -- nothing in it is hardcoded.
type FreezeInfo struct {
	FreezeQty   int64 // broker freezQty: an order at or above this is frozen
	LotSize     int64 // broker lot size for the contract
	MaxOrderQty int64 // largest placeable order: (FreezeQty-1) rounded down to whole lots
}

// FreezeQtyProvider is an optional broker capability returning a token's
// live freeze quantity and lot size. Freeze quantities are revised by
// exchange circular and can change overnight, so they are never
// hardcoded; without a live value, orders go one lot at a time.
type FreezeQtyProvider interface {
	GetFreezeQty(ctx context.Context, token int64) (FreezeInfo, error)
}

type BrokerFactory interface {
	GetExecutor(userID, brokerName, accountID string) (Executor, error)
}

// FillPersistenceReport records the result of a manual reconciliation run.
// It is deliberately explicit: unmatched broker orders are reported, never
// silently attached to an unrelated local trade.
type FillPersistenceReport struct {
	Processed int      `json:"processed"`
	Persisted int      `json:"persisted"`
	Skipped   int      `json:"skipped"`
	Unmatched []string `json:"unmatched_order_ids,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

// VerifiedFillPersistence is implemented by stores that can durably persist
// broker-confirmed execution fills. It never places or changes broker orders.
type VerifiedFillPersistence interface {
	PersistVerifiedFills(
		ctx context.Context,
		brokerName string,
		accountID string,
		fills []BrokerFill,
	) (FillPersistenceReport, error)
}
