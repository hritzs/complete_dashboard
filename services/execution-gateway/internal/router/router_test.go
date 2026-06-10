package router

import (
	"context"
	"testing"

	broker "trading-platform/libs/go-broker"
)

// mockBrokerClient implements broker.Client for testing
type mockBrokerClient struct {
	placedOrder *broker.OrderIntent
}

func (m *mockBrokerClient) PerformFullLogin(ctx context.Context, cfg *broker.AccountConfig) (*broker.SessionDetails, error) {
	return nil, nil
}

func (m *mockBrokerClient) PlaceOrder(ctx context.Context, intent *broker.OrderIntent) (*broker.OrderResponse, error) {
	m.placedOrder = intent
	return &broker.OrderResponse{
		OrderID: "mock-order-123",
		Status:  "success",
	}, nil
}

func (m *mockBrokerClient) CancelOrder(ctx context.Context, orderID string, extraArgs map[string]interface{}) error {
	return nil
}

func (m *mockBrokerClient) GetOrderBook(ctx context.Context) (interface{}, error) {
	return nil, nil
}

func TestRouter_RouteOrder(t *testing.T) {
	r := New()
	mockClient := &mockBrokerClient{}

	// 1. Register the mock client for user "TESTUSER"
	r.AddClient("TESTUSER", mockClient)

	// 2. Create a test intent that is broker-agnostic
	intent := &broker.OrderIntent{
		ClientID: "TESTUSER",
		Side:     "BUY",
		Quantity: 100,
	}

	// 3. Route the order
	resp, err := r.RouteOrder(context.Background(), intent)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if resp.OrderID != "mock-order-123" {
		t.Errorf("Expected mock-order-123, got %s", resp.OrderID)
	}

	// 4. Test unmapped client to ensure isolation
	badIntent := &broker.OrderIntent{ClientID: "UNKNOWN_USER"}
	if _, err = r.RouteOrder(context.Background(), badIntent); err == nil {
		t.Errorf("Expected error for UNKNOWN_USER, got nil")
	}
}
