package registry

import (
	"fmt"
	"sync"

	gobroker "trading-platform/libs/go-broker"
)

// Factory is a function signature that creates a new Broker client based on the provided configuration.
type Factory func(config gobroker.Config) (gobroker.Client, error)

// Registry maintains a thread-safe map of broker names to their respective factory functions.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	clients   map[string]gobroker.Client   // Active broker clients
	configs   map[string]*BrokerConfig     // Broker configurations
	primary   string                       // Primary broker name
}

// New creates and initializes a new Broker Registry.
func New() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		clients:   make(map[string]gobroker.Client),
		configs:   make(map[string]*BrokerConfig),
		primary:   "",
	}
}

// Register adds a new broker factory to the registry.
// It panics if the factory is nil or if a broker with the same name is already registered.
func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if factory == nil {
		panic("broker-registry: factory cannot be nil")
	}
	if _, exists := r.factories[name]; exists {
		panic(fmt.Sprintf("broker-registry: broker factory for %q is already registered", name))
	}

	r.factories[name] = factory
}

// CreateClient looks up the factory for the specified broker name and creates a new client.
func (r *Registry) CreateClient(name string, config gobroker.Config) (gobroker.Client, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("broker-registry: unsupported broker %q", name)
	}

	return factory(config)
}

// SetPrimaryBroker sets the default broker for orders
func (r *Registry) SetPrimaryBroker(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.clients[name]; !exists {
		return fmt.Errorf("broker-registry: broker %q not registered", name)
	}

	r.primary = name
	return nil
}

// GetPrimaryBroker returns the primary broker client
func (r *Registry) GetPrimaryBroker() (gobroker.Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.primary == "" {
		return nil, fmt.Errorf("broker-registry: no primary broker set")
	}

	client, exists := r.clients[r.primary]
	if !exists {
		return nil, fmt.Errorf("broker-registry: primary broker %q not found", r.primary)
	}

	return client, nil
}

// StoreClient caches an active broker client
func (r *Registry) StoreClient(name string, client gobroker.Client) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if client == nil {
		return fmt.Errorf("broker-registry: client cannot be nil")
	}

	r.clients[name] = client

	// Set as primary if no primary exists
	if r.primary == "" {
		r.primary = name
	}

	return nil
}

// ListBrokers returns all registered broker names
func (r *Registry) ListBrokers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}
