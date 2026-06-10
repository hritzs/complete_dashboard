package broker

import (
	"context"
)

// Client defines the common capabilities required from any broker integration.
// Any new broker added to the platform must implement these methods.
type Client interface {
	// PerformFullLogin handles authentication and returns standard session details.
	PerformFullLogin(ctx context.Context, cfg *AccountConfig) (*SessionDetails, error)

	// PlaceOrder executes a generic order intent and translates it to broker-specific payloads.
	PlaceOrder(ctx context.Context, intent *OrderIntent) (*OrderResponse, error)

	// CancelOrder cancels an existing open order.
	// extraArgs can be used for broker-specific requirements (like OrderUniqueIdentifier in XTS).
	CancelOrder(ctx context.Context, orderID string, extraArgs map[string]interface{}) error

	// GetOrderBook retrieves the current state of orders.
	GetOrderBook(ctx context.Context) (interface{}, error)
}