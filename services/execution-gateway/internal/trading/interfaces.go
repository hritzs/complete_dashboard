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
}

type LotSizeProvider interface {
    GetLotSize(ctx context.Context, symbol, expiry string) (int, error)
}

type Executor interface {
    ExecuteOrderIntent(ctx context.Context, intent OrderIntent) (*ExecutionResult, error)
}

type BrokerFactory interface {
    GetExecutor(userID, brokerName, accountID string) (Executor, error)
}