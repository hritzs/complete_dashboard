package router

import (
	"context"
	"fmt"
	"sync"

	broker "trading-platform/libs/go-broker"
)

// Router handles the routing of incoming order intents to the appropriate broker client.
type Router struct {
	mu      sync.RWMutex
	clients map[string]broker.Client // Map of ClientID -> logged-in broker Client
}

// New creates a new Execution Gateway Router.
func New() *Router {
	return &Router{
		clients: make(map[string]broker.Client),
	}
}

// AddClient registers an active broker client for a specific client ID.
// This connects Layer 2 (Session Management) with Layer 3 (Execution Routing).
func (r *Router) AddClient(clientID string, client broker.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[clientID] = client
}

// RemoveClient unregisters a broker client (e.g., on logout or session expiry).
func (r *Router) RemoveClient(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, clientID)
}

// RouteOrder determines the correct broker for the intent and executes it.
func (r *Router) RouteOrder(ctx context.Context, intent *broker.OrderIntent) (*broker.OrderResponse, error) {
	if intent == nil {
		return nil, fmt.Errorf("gateway-router: order intent cannot be nil")
	}

	r.mu.RLock()
	client, exists := r.clients[intent.ClientID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("gateway-router: no active broker client found for ClientID %q", intent.ClientID)
	}

	// Place the order via the unified broker interface
	// The underlying client (e.g., XTS) handles translating this to a broker-specific HTTP payload
	return client.PlaceOrder(ctx, intent)
}
