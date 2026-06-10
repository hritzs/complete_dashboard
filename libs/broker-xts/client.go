package xts

import (
	"context"

	"fmt"
	"trading-platform/libs/broker-xts/auth"
	"trading-platform/libs/broker-xts/interactive"
	broker "trading-platform/libs/go-broker"
)

// BrokerName is the unique identifier for the XTS broker.
const BrokerName = "xts"

// Client implements the broker.Broker interface for Symphony XTS.
type Client struct {
	BaseURL   string
	AppKey    string
	SecretKey string
	Source    string
	Session   *broker.SessionDetails
}

// NewFactory creates a new XTS client from a generic broker config.
// It satisfies the registry.Factory function signature.
func NewFactory(config broker.Config) (broker.Client, error) {
	if config.AppKey == "" || config.SecretKey == "" {
		return nil, fmt.Errorf("xts-factory: AppKey and SecretKey are required")
	}
	return NewClient(
		config.BaseURL,
		config.AppKey,
		config.SecretKey,
		config.Source), nil
}

// NewClient initializes a native Symphony XTS Go client.
func NewClient(baseURL, appKey, secretKey, source string) *Client {
	return &Client{
		BaseURL:   baseURL,
		AppKey:    appKey,
		SecretKey: secretKey,
		Source:    source,
	}
}

// PerformFullLogin authenticates with XTS and retrieves the Interactive token
func (c *Client) PerformFullLogin(ctx context.Context, cfg *broker.AccountConfig) (*broker.SessionDetails, error) {
	token, userID, err := auth.InteractiveLogin(c.BaseURL, c.AppKey, c.SecretKey, c.Source)
	if err != nil {
		return nil, err
	}

	c.Session = &broker.SessionDetails{
		UserID:    userID,
		AuthToken: token,
		BrokerSpecific: map[string]interface{}{
			"appKey": c.AppKey,
		},
	}
	return c.Session, nil
}

// PlaceOrder issues an order via the XTS Interactive API
func (c *Client) PlaceOrder(ctx context.Context, intent *broker.OrderIntent) (*broker.OrderResponse, error) {
	return interactive.PlaceOrder(ctx, c.BaseURL, c.Session.AuthToken, c.Session.UserID, intent)
}

// CancelOrder cancels an existing open order in XTS.
func (c *Client) CancelOrder(ctx context.Context, orderID string, extraArgs map[string]interface{}) error {
	// TODO: Wire up actual XTS Interactive API call for Order Cancellation
	// Usually requires passing AppOrderID from extraArgs if needed
	return fmt.Errorf("xts: CancelOrder not implemented yet")
}

// GetOrderBook retrieves the current state of orders from XTS.
func (c *Client) GetOrderBook(ctx context.Context) (interface{}, error) {
	// TODO: Wire up actual XTS Interactive API call for OrderBook retrieval
	return nil, fmt.Errorf("xts: GetOrderBook not implemented yet")
}
