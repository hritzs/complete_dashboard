package greeksoft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	broker "trading-platform/libs/go-broker"
)

// Client implements the broker.Client interface for Greeksoft.
type Client struct {
	RestURL    string
	HTTPClient *http.Client
	Session    *broker.SessionDetails // Replaces the global variables from your JS/Python
}

// NewClient initializes a native Greeksoft Go client.
func NewClient(restURL string) *Client {
	return &Client{
		RestURL: restURL,
		HTTPClient: &http.Client{
			Timeout: 3 * time.Second, // Low latency timeout
		},
	}
}

// PlaceOrder completely bypasses the Python proxy and constructs the NewOrderRequest payload natively.
func (c *Client) PlaceOrder(ctx context.Context, intent *broker.OrderIntent) (*broker.OrderResponse, error) {
	if c.Session == nil || c.Session.BrokerSpecific["gcid"] == nil {
		return nil, fmt.Errorf("greeksoft session not initialized or missing gcid")
	}

	gcid := c.Session.BrokerSpecific["gcid"].(string)

	sideCode := "1" // 1 = BUY
	if intent.Side == "SELL" {
		sideCode = "2" // 2 = SELL
	}

	// Replicating the exact JSON structure required by Greeksoft API
	payload := map[string]interface{}{
		"request": map[string]interface{}{
			"data": map[string]interface{}{
				"gtoken":           intent.InstrumentToken,
				"side":             sideCode,
				"gcid":             gcid,
				"price":            fmt.Sprintf("%.2f", intent.LimitPrice),
				"order_type":       "1", // 1 for LIMIT order
				"qty":              strconv.Itoa(intent.Quantity),
				"validity":         "0", // 0 = DAY
				"exchange":         intent.ExchangeSegment,
				"tradeSymbol":      intent.TradeUID, // Usually mapped from a Contract Master
				"product":          "0",             // 0 for MIS/NRML
				"is_preopen_order": "0",
				"is_post_closed":   "0",
				"is_restapi":       "1",
				"disclosed_qty":    "0",
				"lot":              "1", // Assumes pre-calculated lot sizing
			},
			"response_format": "json",
			"request_type":    "subscribe",
			"streaming_type":  "NewOrderRequest",
		},
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", c.RestURL+"/NewOrderRequest", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	// Inject Greeksoft Session Token
	if c.Session.AuthToken != "" {
		req.Header.Set("Authorization", c.Session.AuthToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("greeksoft native order request failed: %w", err)
	}
	defer resp.Body.Close()

	// In a complete implementation, you'd parse the specific response here.
	return &broker.OrderResponse{Status: "SUBMITTED"}, nil
}