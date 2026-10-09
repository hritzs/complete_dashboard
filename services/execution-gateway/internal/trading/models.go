package trading

import (
	"encoding/json"
	"time"
)

type OptionChainRow struct {
	Strike   float64 `json:"strike"`
	CEToken  int64   `json:"ce_token"`
	PEToken  int64   `json:"pe_token"`
	CELtp    float64 `json:"ce_ltp"`
	PELtp    float64 `json:"pe_ltp"`
	CEBid    float64 `json:"ce_bid"`
	CEAsk    float64 `json:"ce_ask"`
	PEBid    float64 `json:"pe_bid"`
	PEAsk    float64 `json:"pe_ask"`
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

	// L1-L5 exchange depth (published for strikes near the ATM on the
	// nearest expiries only); nil when not published / not yet received.
	CEDepth *DepthBook `json:"ce_depth,omitempty"`
	PEDepth *DepthBook `json:"pe_depth,omitempty"`

	// Exchange trade time of the LTP and the candle close (feed-decoder's
	// MinuteCloseTracker, two nearest expiries; unix seconds, 0 = absent):
	// *Close is the last trade before every minute boundary B with
	// *CloseLTT < B <= *CloseNext. See lutCloseBefore.
	CELTT       int64   `json:"ce_ltt,omitempty"`
	PELTT       int64   `json:"pe_ltt,omitempty"`
	CEClose     float64 `json:"ce_close,omitempty"`
	PEClose     float64 `json:"pe_close,omitempty"`
	CECloseLTT  int64   `json:"ce_close_ltt,omitempty"`
	PECloseLTT  int64   `json:"pe_close_ltt,omitempty"`
	CECloseNext int64   `json:"ce_close_next,omitempty"`
	PECloseNext int64   `json:"pe_close_next,omitempty"`
}

// DepthBook is one option's 5-level book, best level first. Each level is
// [price, qty] on the wire.
type DepthBook struct {
	Bids  []DepthLevel `json:"b"`
	Asks  []DepthLevel `json:"a"`
	AgeMs int64        `json:"age_ms"`
}

// DepthLevel is one book level.
type DepthLevel struct {
	Price float64
	Qty   int64
}

// UnmarshalJSON accepts the compact [price, qty] wire form.
func (l *DepthLevel) UnmarshalJSON(b []byte) error {
	var pair []float64
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if len(pair) >= 2 {
		l.Price, l.Qty = pair[0], int64(pair[1])
	}
	return nil
}

// MarshalJSON writes the same compact form back out.
func (l DepthLevel) MarshalJSON() ([]byte, error) {
	return json.Marshal([]float64{l.Price, float64(l.Qty)})
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

	// Direct risk fields for callers of this endpoint (the Testing tab's
	// manual deploy) that don't go through ConfigBuild/Risk below -- applied
	// the same way, via applyBuildRiskConfig, before any order is sent.
	ExitTime string  `json:"exit_time,omitempty"`
	SlBps    float64 `json:"sl_bps,omitempty"`
	TpBps    float64 `json:"tp_bps,omitempty"`
	WingPct  float64 `json:"wing_pct,omitempty"`

	// Risk is applied to the new trade's monitor config before any order is
	// sent. Set only by the automated/config build path; takes precedence
	// over the raw fields above if both are somehow present.
	Risk *BuildRiskConfig `json:"-"`
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
	TpBps            float64 `json:"tp_bps"`
	WingPct          float64 `json:"wing_pct"`
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
	// When > 0, drives a real, verified autonomous exit: runMonitorCycle
	// compares live PnL against a threshold fixed to the trade's
	// ORIGINAL size (trade.Lots, not live CEQty/PEQty -- see
	// slThresholdForTrade) and, on breach, calls SquareOff(tradeUID, "SL")
	// (aggressive chunking, verified fills, sets CLOSED_SL only once the
	// exit is confirmed -- never a bare status flip).
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
	AutoRiskExecutionEnabled bool      `json:"auto_risk_execution_enabled,omitempty"`
	HedgeThresholdDelta      float64   `json:"hedge_threshold_delta"`
	HedgeDeltaThreshold      float64   `json:"hedge_delta_threshold"`
	StraddleStopPrice        float64   `json:"straddle_stop_price"`
	StraddlePriceExit        float64   `json:"straddle_price_exit"`
	OrderLotsPerCall         int       `json:"order_lots_per_call"`
	PollIntervalSec          int       `json:"poll_interval_sec"`
	StraddleDiv              float64   `json:"straddle_div"`
	HedgeDiv                 float64   `json:"hedge_div"`
	RollTimeThreshold        float64   `json:"roll_time_threshold"`
	SquareOffTime            time.Time `json:"square_off_time,omitempty"`
	ForceOneLotHedgeTest     bool      `json:"force_one_lot_hedge_test,omitempty"`
	HedgePointsFloor         float64   `json:"hedge_points_floor,omitempty"`
	HedgeTestExecuted        bool      `json:"hedge_test_executed,omitempty"`
	// HedgeCount: hedges that actually filled; HedgeEvents: every hedge
	// attempt (auto / manual) with what filled -- saved with the trade.
	HedgeCount                   int          `json:"hedge_count,omitempty"`
	HedgeEvents                  []HedgeEvent `json:"hedge_events,omitempty"`
	ForceHedgeRegardlessOfPoints bool         `json:"force_hedge_regardless_of_points,omitempty"`

	// Floor under points_allowed for the minute-end hedge trigger, in basis
	// points of the live synthetic spot (the reference system's
	// hedge_min_threshold_bps): a move smaller than this is never worth the
	// transaction cost of a hedge. nil means "use defaultHedgeMinThresholdBps";
	// an explicit 0 disables the floor.
	HedgeMinThresholdBps *float64 `json:"hedge_min_threshold_bps,omitempty"`

	// WingPct > 0 enables wings: long far-OTM options bought purely for
	// margin (RM). Each wing sits WingPct% of the action's strike away
	// (strike -/+ strike*WingPct/100, rounded to the chain's strike step),
	// sized to the quantity that action sold, so at every point net CE and
	// net PE across all strikes are zero. Wing delta/PnL never enter
	// PointsOut, SL/TP, hedge sizing or realized PnL. 0 disables wings.
	WingPct float64 `json:"wing_pct,omitempty"`

	// PartialFill marks an "ACTIVE (partial)" trade: its build stopped
	// before the full size (e.g. margin rejection) and it is tracked --
	// monitors, hedge, exits -- at the quantity that really filled.
	// RequestedCEQty/PEQty keep what the build originally asked for.
	PartialFill    bool `json:"partial_fill,omitempty"`
	RequestedCEQty int  `json:"requested_ce_qty,omitempty"`
	RequestedPEQty int  `json:"requested_pe_qty,omitempty"`

	// ExitHalted: a square-off stopped because some exit orders were
	// unconfirmed. Never auto-resumed or auto-exited again -- a human
	// checks the broker position first.
	ExitHalted bool `json:"exit_halted,omitempty"`

	// Modifications: every Modify Config change (time, field, from -> to).
	Modifications []ConfigChange `json:"modifications,omitempty"`
	// RiskExcludedTokens: manually added legs kept out of PnL / greeks /
	// PointsOut / SL / TP (still shown, still closed by Full Exit).
	RiskExcludedTokens []int64 `json:"risk_excluded_tokens,omitempty"`

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
	// PnL-based SL, wired in runMonitorCycle: when > 0, exits (via
	// SquareOff(tradeUID, "SL")) once pnlPerStraddle <= -(spot * bps /
	// 10,000). Example: NIFTY spot 24,200, 14 bps -> a 33.88-point
	// straddle loss threshold (corrected from an earlier, numerically
	// wrong "338.8" example in this comment). Independent of, and
	// checked alongside, SLPointsPerLot -- either can trigger the exit.
	SLPnLBpsOfSpot float64 `json:"sl_pnl_bps_of_spot,omitempty"`
	// PnL-based TP, wired in runMonitorCycle: when > 0, exits (via
	// SquareOff(tradeUID, "TP"), normal (non-aggressive) chunking) once
	// pnlPerStraddle >= (spot * bps / 10,000). Sets CLOSED_TP on
	// success. TPPnLTarget (rupee-based) remains alert-only.
	TPPnLBpsOfSpot float64 `json:"tp_pnl_bps_of_spot,omitempty"`
	// MTM-based square-off: exit if intraday MTM crosses this threshold (+/- ₹ value)
	// Positive = take profit, negative = stop loss
	MTMSquareOffThreshold float64 `json:"mtm_square_off_threshold,omitempty"`
	// MTM exit (mtm_exit.go), on every trade: nil = infinity (never
	// fires). When set, the trade is closed lot by lot at depth-checked IOC
	// limits once its executable MTM >= the level, ending at or above it.
	// Unit: "rs" (trade total, default), "pts" (per straddle), "bps" (of
	// spot, per straddle).
	MTMExitLevel *float64 `json:"mtm_exit_level,omitempty"`
	MTMExitUnit  string   `json:"mtm_exit_unit,omitempty"`
	// MTMExitPct: share of the ORIGINAL position the MTM exit closes (0 or
	// 100 = complete). MTMExitClosedQty: straddle contracts it has closed
	// so far (CE+PE) -- it never closes more than its share, and never more
	// than is open (combined_sqf.go).
	MTMExitPct       float64 `json:"mtm_exit_pct,omitempty"`
	MTMExitClosedQty int64   `json:"mtm_exit_closed_qty,omitempty"`

	// ATM-straddle exit (combined_sqf.go): when the live ATM CE LTP + PE
	// LTP falls BELOW this level, close StraddleExitPct of the ORIGINAL
	// position (0 or 100 = complete); above it, nothing. nil = off.
	StraddleExitBelow     *float64 `json:"straddle_exit_below,omitempty"`
	StraddleExitPct       float64  `json:"straddle_exit_pct,omitempty"`
	StraddleExitClosedQty int64    `json:"straddle_exit_closed_qty,omitempty"`
	// Extra steps of both rules (exit_tiers.go): each closes its own % of
	// the ORIGINAL position once, at its own level.
	MTMExitTiers      []ExitTier `json:"mtm_exit_tiers,omitempty"`
	StraddleExitTiers []ExitTier `json:"straddle_exit_tiers,omitempty"`
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
	LotSize         int64    `json:"lot_size,omitempty"`
	OrderType       string   `json:"order_type"`
	LimitPrice      *float64 `json:"limit_price,omitempty"`
	TimeInForce     string   `json:"time_in_force,omitempty"` // "" / DAY (default) or IOC
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
	// Wing marks a margin-only wing leg: shown, never in the totals.
	Wing bool `json:"wing,omitempty"`
	// Excluded: a manually added leg the user chose to keep OUT of the
	// trade's PnL / greeks / SL / TP (shown, closed with the trade).
	Excluded bool   `json:"excluded,omitempty"`
	Expiry   string `json:"expiry,omitempty"`
}

type TradeSnapshot struct {
	TradeUID       string    `json:"trade_uid"`
	Timestamp      time.Time `json:"timestamp"`
	Status         string    `json:"status"`
	Symbol         string    `json:"symbol"`
	Expiry         string    `json:"expiry"`
	Strike         float64   `json:"strike"`
	Underlying     float64   `json:"underlying"`
	TotalPNL       float64   `json:"total_pnl"`
	RealizedPNL    float64   `json:"realized_pnl"`
	UnrealizedPNL  float64   `json:"unrealized_pnl"`
	NetDelta       float64   `json:"net_delta"`
	NetGamma       float64   `json:"net_gamma"`
	NetTheta       float64   `json:"net_theta"`
	NetVega        float64   `json:"net_vega"`
	PnLPerStraddle float64   `json:"pnl_per_straddle"`
	// MTM exit: the level in rupees (nil = infinity, never fires) and the
	// trade's executable MTM (closing every open leg through the depth now).
	MTMExitFloor     *float64           `json:"mtm_exit_floor,omitempty"`
	MTMExecPNL       *float64           `json:"mtm_exec_pnl,omitempty"`
	StraddleQuantity int64              `json:"straddle_quantity"`
	PointsOut        float64            `json:"points_out"`
	PointsAllowed    float64            `json:"points_allowed"`
	LivePositions    []TradeLegSnapshot `json:"live_positions"`

	// The two candidates PointsAllowed is the minimum of, and the strike
	// whose straddle/IV they were computed from, so both can be checked
	// against the live chain.
	PointsAllowedStraddle float64 `json:"points_allowed_straddle"` // straddle / straddle_div
	PointsAllowedIV       float64 `json:"points_allowed_iv"`       // spot * IV / hedge_div
	AllowedStrike         float64 `json:"allowed_strike"`

	// BuildOrdersSubmitted/BuildOrdersTotal drive a live "Building X/Y
	// orders placed" progress indicator on the UI trade card while
	// Status == "BUILDING". Pushed after every order submission during
	// executeBuild so the UI reflects real-time progress over the
	// existing straddle_update websocket, not just a stale poll.
	BuildOrdersSubmitted int `json:"build_orders_submitted,omitempty"`
	BuildOrdersTotal     int `json:"build_orders_total,omitempty"`

	// Wings (margin-only, excluded from every total above). WingNetCE /
	// WingNetPE are net signed CE / PE quantity across ALL strikes,
	// wings included -- both 0 when the wings exactly cover the shorts.
	WingPct   float64 `json:"wing_pct,omitempty"`
	WingPNL   float64 `json:"wing_pnl,omitempty"`
	WingNetCE int64   `json:"wing_net_ce"`
	WingNetPE int64   `json:"wing_net_pe"`
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

// HedgeEvent is one hedge attempt on a trade.
type HedgeEvent struct {
	Time        string  `json:"time"`    // HH:MM:SS IST
	Trigger     string  `json:"trigger"` // AUTO (minute-end check) / MANUAL
	Reason      string  `json:"reason,omitempty"`
	Lots        int     `json:"lots"`
	Qty         int64   `json:"qty"`
	CESide      string  `json:"ce_side,omitempty"`
	PESide      string  `json:"pe_side,omitempty"`
	CEFilled    int64   `json:"ce_filled"`
	PEFilled    int64   `json:"pe_filled"`
	Strike      float64 `json:"strike,omitempty"`
	DeltaBefore float64 `json:"delta_before"`
	Tranches    int     `json:"tranches"`
	Result      string  `json:"result"` // FILLED / PARTIAL / REFUSED / FAILED
	Error       string  `json:"error,omitempty"`
}
