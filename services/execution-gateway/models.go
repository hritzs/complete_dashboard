package main

import (
	"time"

	broker "trading-platform/libs/go-broker"
)

type OptionChainResponse struct {
	Success bool            `json:"success"`
	Data    OptionChainData `json:"data"`
	Error   string          `json:"error"`
}

type OptionChainData struct {
	Symbol            string           `json:"symbol"`
	SyntheticFuture   float64          `json:"synthetic_future"`
	FutureLtp         float64          `json:"future_ltp"`
	ATM               float64          `json:"atm"`
	Expiry            string           `json:"expiry"`
	AvailableExpiries []string         `json:"available_expiries,omitempty"`
	LotSize           int              `json:"lot_size"`
	Chain             []OptionChainRow `json:"chain"`
}

type OptionChainRow struct {
	Strike  float64 `json:"strike"`
	CEToken int64   `json:"ce_token"`
	PEToken int64   `json:"pe_token"`
	CELtp   float64 `json:"ce_ltp"`
	PELtp   float64 `json:"pe_ltp"`
	CEDelta float64 `json:"ce_delta"`
	PEDelta float64 `json:"pe_delta"`
	IsATM   bool    `json:"is_atm"`
}

type DeployStraddleRequest struct {
	UserID       string `json:"user_id"`
	BrokerName   string `json:"broker_name"`
	AccountID    string `json:"account_id"`
	Symbol       string `json:"symbol"`
	Lots         int    `json:"lots"`
	DeltaNeutral bool   `json:"delta_neutral"`
	ProductType  string `json:"product_type"`
	TargetExpiry string `json:"target_expiry"`

	// Optional override from UI if already present
	Strike  int   `json:"strike"`
	CEToken int64 `json:"ce_token"`
	PEToken int64 `json:"pe_token"`
	LotSize int   `json:"lot_size"`

	// Optional custom CE / PE strike flow from UI
	CEStrikePrice int `json:"ce_strike_price"`
	PEStrikePrice int `json:"pe_strike_price"`
}

type DeployStraddleResponse struct {
	Success   bool    `json:"success"`
	TradeUID  string  `json:"trade_uid,omitempty"`
	Status    string  `json:"status,omitempty"`
	Message   string  `json:"message,omitempty"`
	Symbol    string  `json:"symbol,omitempty"`
	Expiry    string  `json:"expiry,omitempty"`
	Strike    float64 `json:"strike,omitempty"`
	CEToken   int64   `json:"ce_token,omitempty"`
	PEToken   int64   `json:"pe_token,omitempty"`
	CEQty     int     `json:"ce_quantity,omitempty"`
	PEQty     int     `json:"pe_quantity,omitempty"`
	CELtp     float64 `json:"ce_entry_price,omitempty"`
	PELtp     float64 `json:"pe_entry_price,omitempty"`
	NetDelta  float64 `json:"net_delta,omitempty"`
	Error     string  `json:"error,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
}

type StoredTrade struct {
	TradeUID       string    `json:"trade_uid"`
	UserID         string    `json:"user_id"`
	BrokerName     string    `json:"broker_name"`
	AccountID      string    `json:"account_id"`
	Symbol         string    `json:"symbol"`
	Expiry         string    `json:"expiry"`
	Strike         float64   `json:"strike"`
	ProductType    string    `json:"product_type"`
	CEToken        int64     `json:"ce_token"`
	PEToken        int64     `json:"pe_token"`
	CEQty          int       `json:"ce_quantity"`
	PEQty          int       `json:"pe_quantity"`
	CELtp          float64   `json:"ce_entry_price"`
	PELtp          float64   `json:"pe_entry_price"`
	NetDelta       float64   `json:"net_delta"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	LastUpdateTime time.Time `json:"last_update_time"`
}

type StoredIntent struct {
	ReceivedAt time.Time          `json:"received_at"`
	Intent     broker.OrderIntent `json:"intent"`
}
