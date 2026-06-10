package manager

import (
	"context"
	"fmt"
	"sync"

	registry "trading-platform/libs/broker-registry"
	gobroker "trading-platform/libs/go-broker"
)

// Manager handles the lifecycle of broker sessions for the platform.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*gobroker.SessionDetails
	registry *registry.Registry
}

// New creates a new Session Manager.
func New(reg *registry.Registry) *Manager {
	return &Manager{
		sessions: make(map[string]*gobroker.SessionDetails),
		registry: reg,
	}
}

// Login authenticates a user with a specific broker and stores the resulting session.
func (m *Manager) Login(ctx context.Context, brokerName string, cfg gobroker.Config, accCfg gobroker.AccountConfig) (*gobroker.SessionDetails, error) {
	// 1. Create the broker client dynamically using Layer 1 (Registry)
	client, err := m.registry.CreateClient(brokerName, cfg)
	if err != nil {
		return nil, fmt.Errorf("session-manager: failed to create broker client: %w", err)
	}

	// 2. Perform the broker-specific full login
	session, err := client.PerformFullLogin(ctx, &accCfg)
	if err != nil {
		return nil, fmt.Errorf("session-manager: login failed for broker %q: %w", brokerName, err)
	}

	// 3. Store the session thread-safely
	m.mu.Lock()
	m.sessions[accCfg.ClientID] = session
	m.mu.Unlock()

	return session, nil
}

// GetSession retrieves an active session for a given client ID.
func (m *Manager) GetSession(clientID string) (*gobroker.SessionDetails, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, exists := m.sessions[clientID]
	return session, exists
}

// RemoveSession deletes a session from the manager (e.g., on logout, token expiry).
func (m *Manager) RemoveSession(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, clientID)
}
