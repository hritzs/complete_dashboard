package greeksoft

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// OrderBookEntry is one row of getOrderBookDetailWithLegV2's response,
// confirmed against GreekSoft's official REST API docs examples (Pending/
// Traded/RMS Rejected/Exchange Rejected orderbooks).
type OrderBookEntry struct {
	BookType      int                      `json:"BookType"`
	LogTime       int64                    `json:"LogTime"`
	Action        int                      `json:"action"`
	Amount        float64                  `json:"amount"`
	AssetType     string                   `json:"assetType"`
	ClientCode    string                   `json:"clientCode"`
	Description   string                   `json:"description"`
	DiscQty       int64                    `json:"discQty"`
	ErrorCode     int                      `json:"errorCode"`
	Exchange      string                   `json:"exchange"`
	ExpiryDate    int64                    `json:"expiryDate"`
	Instrument    string                   `json:"instrument"`
	LotSize       int64                    `json:"lotSize"`
	OptionType    string                   `json:"optionType"`
	OrdID         int64                    `json:"ordID"` // GreekSoft's gorderid -- only unique per trading day
	OrdModTime    int64                    `json:"ordModTime"`
	OrdTime       string                   `json:"ordTime"`
	OrderType     int                      `json:"orderType"`
	PendingQty    int64                    `json:"pendingQty"`
	Price         float64                  `json:"price"`
	Product       int                      `json:"product"`
	Qty           int64                    `json:"qty"`
	Remarks       string                   `json:"remarks"`
	ScripName     string                   `json:"scripName"`
	Status        string                   `json:"status"` // "Pending" | "Traded" | "RMS Rejected" | "Exchange Rejected" confirmed; others may exist
	StrategyName  string                   `json:"strategyName"`
	StrikePrice   float64                  `json:"strikePrice"`
	Tag           string                   `json:"tag"` // broker/RMS annotation (e.g. "Ageing"), not a reliable client correlation id
	TickSize      float64                  `json:"tickSize"`
	Token         int64                    `json:"token"`
	TradeSymbol   string                   `json:"tradeSymbol"`
	TrdQty        int64                    `json:"trdQty"` // traded/filled quantity so far
	TrigPrice     float64                  `json:"trigPrice"`
	UniqueID      int64                    `json:"uniqueID"`      // same as ordID for most rows
	UniqueOrderID string                   `json:"uniqueOrderID"` // exchange order id ("eorderid" equivalent); "0" when not yet assigned
	UserID        string                   `json:"userID"`
	LegInfo       []map[string]interface{} `json:"LegInfo"`
}

// OrderBookTypedResponse is getOrderBookDetailWithLegV2's top-level shape.
type OrderBookTypedResponse struct {
	ErrorCode int              `json:"ErrorCode"`
	Data      []OrderBookEntry `json:"data"`
	Message   string           `json:"message"`
	Success   string           `json:"success"`
}

// GetOrderBookTyped is a typed alternative to GetOrderBook, for new
// callers (e.g. the reconciler). GetOrderBook itself is left untouched --
// execution-gateway's existing fill-verification pipeline
// (collectGreeksoftVerifiedFills/findGreeksoftOrderStatus) does generic
// map[string]interface{} traversal on its result, so changing its decode
// target would silently break that already-working code.
func (c *Client) GetOrderBookTyped(ctx context.Context, statusFilter string) (*OrderBookTypedResponse, error) {
	if c.Session == nil {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}
	if c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session token missing")
	}
	if c.Session.BrokerSpecific == nil {
		return nil, fmt.Errorf("greeksoft broker-specific session data missing")
	}

	gcidValue, ok := c.Session.BrokerSpecific["gcid"]
	if !ok || fmt.Sprintf("%v", gcidValue) == "" {
		return nil, fmt.Errorf("greeksoft GCID missing; jloginNew not completed")
	}
	gcid := fmt.Sprintf("%v", gcidValue)

	if statusFilter == "" {
		statusFilter = "ALL"
	}

	query := url.Values{}
	query.Set("exchangeType", "ALL")
	query.Set("ClientCode", gcid)
	query.Set("Order_Status", statusFilter)
	query.Set("Ordertype", "ALL")
	query.Set("gscid", c.Session.UserID)

	reqURL := fmt.Sprintf("%s/getOrderBookDetailWithLegV2?%s", c.RestAPIBaseURL, query.Encode())

	var resBody OrderBookTypedResponse
	if _, raw, err := c.doJSON(ctx, http.MethodGet, reqURL, c.Session.AuthToken, nil, &resBody); err != nil {
		return nil, fmt.Errorf("greeksoft get order book failed: %w", err)
	} else if resBody.ErrorCode != 0 {
		return nil, fmt.Errorf("greeksoft getOrderBookDetailWithLegV2 ErrorCode=%d raw=%s", resBody.ErrorCode, string(raw))
	}

	return &resBody, nil
}
