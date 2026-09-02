package trading

import "time"

type OptionChainRow struct {
	Strike   float64 `json:"strike"`
	CEToken  int64   `json:"ce_token"`
	PEToken  int64   `json:"pe_token"`
	CELtp    float64 `json:"ce_ltp"`
	PELtp    float64 `json:"pe_ltp"`
	CEDelta  float64 `json:"ce_delta"`
	PEDelta  float64 `json:"pe_delta"`
	CEGamma  float64 `json:"ce_gamma"`
	PEGamma  float64 `json:"pe_gamma"`
	CETheta  float64 `json:"ce_theta"`
	PETheta  float64 `json:"pe_theta"`
	CEVega   float64 `json:"ce_vega"`
	PEVega   float64 `json:"pe_vega"`
	CEIV     float64 `json:"ce_iv"`
	PEIV     float64 `json:"pe_iv"`
	IsATM    bool    `json:"is_atm"`
	CESymbol string  `json:"ce_symbol,omitempty"`
	PESymbol string  `json:"pe_symbol,omitempty"`
}

type OptionChainSnapshot struct {
	Symbol            string           `json:"symbol"`
	SyntheticFuture   float64          `json:"synthetic_future"`
	SyntheticSpot     float64          `json:"synthetic_spot"`
	FutureLtp         float64          `json:"future_ltp"`
	ATM               float64          `json:"atm"`
	Expiry            string           `json:"expiry"`
	AvailableExpiries []string         `json:"available_expiries,omitempty"`
	LotSize           int              `json:"lot_size"`
	Chain             []OptionChainRow `json:"chain"`
}

type DeployStraddleRequest struct {
	UserID           string `json:"user_id,omitempty"`
	BrokerName       string `json:"broker_name,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
	ExchangeSegment  string `json:"exchange_segment,omitempty"`
	Symbol           string `json:"symbol"`
	Lots             int    `json:"lots"`
	DeltaNeutral     bool   `json:"delta_neutral,omitempty"`
	ProductType      string `json:"product_type,omitempty"`
	TargetExpiry     string `json:"target_expiry,omitempty"`
	Strike           int    `json:"strike,omitempty"`
	CEToken          int64  `json:"ce_token,omitempty"`
	PEToken          int64  `json:"pe_token,omitempty"`
	LotSize          int    `json:"lot_size,omitempty"`
	CEStrikePrice    int    `json:"ce_strike_price,omitempty"`
	PEStrikePrice    int    `json:"pe_strike_price,omitempty"`
	OrderLotsPerCall int    `json:"order_lots_per_call,omitempty"`
}

type CustomStraddleRequest struct {
	UserID               string  `json:"userID"`
	BrokerName           string  `json:"brokerName"`
	AccountID            string  `json:"accountID"`
	ExchangeSegment      string  `json:"exchangeSegment,omitempty"`
	Symbol               string  `json:"symbol"`
	Lots                 int     `json:"lots"`
	CEStrikePrice        int     `json:"ceStrikePrice"`
	PEStrikePrice        int     `json:"peStrikePrice"`
	DeltaNeutral         bool    `json:"deltaNeutral"`
	ProductType          string  `json:"productType"`
	OrderLotsPerCall     int     `json:"orderLotsPerCall"`
	HedgeMonitorInterval float64 `json:"hedgeMonitorInterval"`
	SlMonitorInterval    float64 `json:"slMonitorInterval"`
	RollMonitorInterval  float64 `json:"rollMonitorInterval"`
}

type ConfigBuildRequest struct {
	UserID           string  `json:"user_id"`
	BrokerName       string  `json:"broker_name"`
	AccountID        string  `json:"account_id"`
	ExchangeSegment  string  `json:"exchange_segment,omitempty"`
	ProductType      string  `json:"product_type,omitempty"`
	Symbol           string  `json:"symbol"`
	Size             int     `json:"size"`
	Lots             int     `json:"lots"`
	EntryTime        string  `json:"entry_time"`
	ExitTime         string  `json:"exit_time"`
	Idv              float64 `json:"idv"`
	IdvDivisor       float64 `json:"idv_divisor"`
	StraddleFilter   float64 `json:"straddle_filter"`
	SlBps            float64 `json:"sl_bps"`
	BuyBuffer        float64 `json:"buy_buffer"`
	SellBuffer       float64 `json:"sell_buffer"`
	HedgeDiv         float64 `json:"hedge_div"`
	StraddleDiv      float64 `json:"straddle_div"`
	RollStraddleDiv  float64 `json:"roll_straddle_div"`
	SlStartTime      string  `json:"sl_start_time"`
	HedgeStartTime   string  `json:"hedge_start_time"`
	RollStartTime    string  `json:"roll_start_time"`
	OrderLotsPerCall int     `json:"order_lots_per_call"`
	TargetExpiry     string  `json:"target_expiry"`
}

type DeployStraddleResponse struct {
	Success    bool    `json:"success"`
	TradeUID   string  `json:"trade_uid,omitempty"`
	Status     string  `json:"status,omitempty"`
	Message    string  `json:"message,omitempty"`
	Symbol     string  `json:"symbol,omitempty"`
	Expiry     string  `json:"expiry,omitempty"`
	Strike     float64 `json:"strike,omitempty"`
	CEToken    int64   `json:"ce_token,omitempty"`
	PEToken    int64   `json:"pe_token,omitempty"`
	CEQty      int     `json:"ce_quantity,omitempty"`
	PEQty      int     `json:"pe_quantity,omitempty"`
	CELtp      float64 `json:"ce_ltp,omitempty"`
	PELtp      float64 `json:"pe_ltp,omitempty"`
	NetDelta   float64 `json:"net_delta,omitempty"`
	LotSize    int     `json:"lot_size,omitempty"`
	Lots       int     `json:"lots,omitempty"`
	DurationMS int64   `json:"duration_ms,omitempty"`
	CreatedAt  string  `json:"created_at,omitempty"`
	Error      string  `json:"error,omitempty"`
}

type StoredTrade struct {
	TradeUID        string        `json:"trade_uid"`
	UserID          string        `json:"user_id"`
	BrokerName      string        `json:"broker_name"`
	AccountID       string        `json:"account_id"`
	Symbol          string        `json:"symbol"`
	Expiry          string        `json:"expiry"`
	Strike          float64       `json:"strike"`
	ProductType     string        `json:"product_type"`
	ExchangeSegment string        `json:"exchange_segment,omitempty"`
	CEToken         int64         `json:"ce_token"`
	PEToken         int64         `json:"pe_token"`
	CEQty           int           `json:"ce_quantity"`
	PEQty           int           `json:"pe_quantity"`
	CELtp           float64       `json:"ce_entry_price"`
	PELtp           float64       `json:"pe_entry_price"`
	NetDelta        float64       `json:"net_delta"`
	Status          string        `json:"status"`
	Mode            string        `json:"mode,omitempty"`
	Underlying      float64       `json:"underlying,omitempty"`
	LotSize         int           `json:"lot_size"`
	Lots            int           `json:"lots"`
	CreatedAt       time.Time     `json:"created_at"`
	LastUpdateTime  time.Time     `json:"last_update_time"`
	Config          MonitorConfig `json:"config"`
	PointsOut       float64       `json:"points_out,omitempty"`
	PointsAllowed   float64       `json:"points_allowed,omitempty"`
	RealizedPnL     float64       `json:"realized_pnl,omitempty"`
	ClosedAt        time.Time     `json:"closed_at,omitempty"`
}

type MonitorConfig struct {
	// Legacy / existing fields
	BuyBuffer  float64 `json:"buy_buffer"`
	SellBuffer float64 `json:"sell_buffer"`
	// Deprecated: retained for compatibility only. It is alert-only and
	// must never directly set a trade to CLOSED_SL.
	SLPointsPerLot float64 `json:"sl_points_per_lot"`

	// Existing rupee-PnL monitor fields. These remain alert-only until
	// verified broker-risk execution is enabled in a later implementation.
	SLPnLLimit  float64 `json:"sl_pnl_limit"`
	TPPnLTarget float64 `json:"tp_pnl_target"`

	// Minute-end risk policy.
	//
	// SpotStopLossBps is converted to normalized straddle points:
	// underlying * bps / 10,000. A 1-bps NIFTY stop at 24,400 is 2.44
	// PnL-per-straddle points.
	SpotStopLossBps float64 `json:"spot_stop_loss_bps,omitempty"`

	// Positive normalized PnL-per-straddle target. Example: 5.0 exits
	// when one complete straddle earns +5 option-premium points.
	TakeProfitPointsPerStraddle float64 `json:"take_profit_points_per_straddle,omitempty"`

	// Explicit safety gate. Stage 1 records/logs signals only; live broker
	// execution remains blocked until SquareOff is hardened and verified.
	AutoRiskExecutionEnabled     bool      `json:"auto_risk_execution_enabled,omitempty"`
	HedgeThresholdDelta          float64   `json:"hedge_threshold_delta"`
	HedgeDeltaThreshold          float64   `json:"hedge_delta_threshold"`
	StraddleStopPrice            float64   `json:"straddle_stop_price"`
	StraddlePriceExit            float64   `json:"straddle_price_exit"`
	OrderLotsPerCall             int       `json:"order_lots_per_call"`
	PollIntervalSec              int       `json:"poll_interval_sec"`
	StraddleDiv                  float64   `json:"straddle_div"`
	HedgeDiv                     float64   `json:"hedge_div"`
	RollTimeThreshold            float64   `json:"roll_time_threshold"`
	SquareOffTime                time.Time `json:"square_off_time,omitempty"`
	ForceOneLotHedgeTest         bool      `json:"force_one_lot_hedge_test,omitempty"`
	HedgePointsFloor             float64   `json:"hedge_points_floor,omitempty"`
	HedgeTestExecuted            bool      `json:"hedge_test_executed,omitempty"`
	ForceHedgeRegardlessOfPoints bool      `json:"force_hedge_regardless_of_points,omitempty"`

	// === ENTRY MODES (mutually exclusive: scheduled vs price-trigger vs manual) ===
	// Scheduled entry: fire at this exact time (e.g., 09:20:00)
	EntryScheduledTime time.Time `json:"entry_scheduled_time,omitempty"`
	// Price-trigger entry: wait until straddle price >= this value before building
	// If straddle price is already above this at deploy time, build immediately.
	// If below, wait and poll; build partial/full as price crosses threshold.
	EntryStraddlePriceTrigger float64 `json:"entry_straddle_price_trigger,omitempty"`

	// === EXIT MODES (any can fire first, all independent) ===
	// Time-based square-off: hard exit at this time (e.g., 15:15:00)
	SquareOffHardTime time.Time `json:"square_off_hard_time,omitempty"`
	// PnL-based SL: exit if total loss exceeds X bps of spot price (default 14 bps = 0.0014)
	// Example: NIFTY spot 24200, 14 bps = 338.8 points straddle loss threshold
	SLPnLBpsOfSpot float64 `json:"sl_pnl_bps_of_spot,omitempty"`
	// PnL-based TP: exit if total profit exceeds Y bps of spot price (default 0 = disabled)
	TPPnLBpsOfSpot float64 `json:"tp_pnl_bps_of_spot,omitempty"`
	// MTM-based square-off: exit if intraday MTM crosses this threshold (+/- ₹ value)
	// Positive = take profit, negative = stop loss
	MTMSquareOffThreshold float64 `json:"mtm_square_off_threshold,omitempty"`
	// Straddle-price-based square-off: exit ONLY if straddle price falls BELOW this level
	// If straddle price is between this and EntryStraddlePriceTrigger, hold/manage position
	// and re-check every poll cycle; square off only when price actually drops below this.
	StraddlePriceSquareOffBelow float64 `json:"straddle_price_square_off_below,omitempty"`
	// Partial square-off percentage (0-100): close X% of position on any trigger
	// Default 0 = full square-off, 50 = half position, etc.
	PartialSquareOffPercent float64 `json:"partial_square_off_percent,omitempty"`
}

type StoredIntent struct {
	ReceivedAt time.Time   `json:"received_at"`
	Intent     OrderIntent `json:"intent"`
}

type OrderIntent struct {
	IntentID        string   `json:"intent_id"`
	TradeUID        string   `json:"trade_uid"`
	Token           int64    `json:"token"`
	Symbol          string   `json:"symbol"`
	ExchangeSegment string   `json:"exchange_segment,omitempty"`
	Side            string   `json:"side"`
	Quantity        int64    `json:"quantity"`
	OrderType       string   `json:"order_type"`
	LimitPrice      *float64 `json:"limit_price,omitempty"`
	ProductType     string   `json:"product_type"`
	LegType         string   `json:"leg_type"`
	Phase           string   `json:"phase"`
	ParentTradeUID  string   `json:"parent_trade_uid,omitempty"`
	HedgeGroupID    string   `json:"hedge_group_id,omitempty"`
	OrderUID        string   `json:"order_uid"`
	BrokerName      string   `json:"broker_name"`
	AccountID       string   `json:"account_id"`
	ExpectedPrice   float64  `json:"expected_price"`
}

type TradeLegSnapshot struct {
	Token      int64   `json:"token"`
	Strike     float64 `json:"strike"`
	OptionType string  `json:"option_type"`
	Action     string  `json:"action"`
	Quantity   int64   `json:"quantity"`
	EntryPrice float64 `json:"entry_price"`
	LTP        float64 `json:"ltp"`
	PNL        float64 `json:"pnl"`
	IV         float64 `json:"iv"`
	Delta      float64 `json:"delta"`
	Gamma      float64 `json:"gamma"`
	Theta      float64 `json:"theta"`
	Vega       float64 `json:"vega"`
}

type TradeSnapshot struct {
	TradeUID         string             `json:"trade_uid"`
	Timestamp        time.Time          `json:"timestamp"`
	Status           string             `json:"status"`
	Symbol           string             `json:"symbol"`
	Expiry           string             `json:"expiry"`
	Strike           float64            `json:"strike"`
	Underlying       float64            `json:"underlying"`
	TotalPNL         float64            `json:"total_pnl"`
	RealizedPNL      float64            `json:"realized_pnl"`
	UnrealizedPNL    float64            `json:"unrealized_pnl"`
	NetDelta         float64            `json:"net_delta"`
	NetGamma         float64            `json:"net_gamma"`
	NetTheta         float64            `json:"net_theta"`
	NetVega          float64            `json:"net_vega"`
	PnLPerStraddle   float64            `json:"pnl_per_straddle"`
	StraddleQuantity int64              `json:"straddle_quantity"`
	PointsOut        float64            `json:"points_out"`
	PointsAllowed    float64            `json:"points_allowed"`
	LivePositions    []TradeLegSnapshot `json:"live_positions"`
}
type PartialSquareOffRequest struct {
	Percentage float64 `json:"percentage"`
}
type RuntimeTrade struct {
	Trade           StoredTrade
	Snapshot        TradeSnapshot
	StopCh          chan struct{}
	DoneCh          chan struct{}
	LastMinuteCheck time.Time
}
