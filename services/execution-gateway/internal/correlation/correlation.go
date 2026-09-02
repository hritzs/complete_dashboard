package correlation

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Manager tracks internal Order Intents to Broker Order IDs
type Manager struct {
	sync.RWMutex
	intentToOrder map[string]string
	orderToIntent map[string]string
	counter       uint64
}

// NewManager initializes a new thread-safe correlation manager
func NewManager() *Manager {
	return &Manager{
		intentToOrder: make(map[string]string),
		orderToIntent: make(map[string]string),
	}
}

// GenerateIntentID creates a unique, sortable internal correlation ID
func (m *Manager) GenerateIntentID(workerID string) string {
	c := atomic.AddUint64(&m.counter, 1)
	now := time.Now().UnixNano()
	return fmt.Sprintf("INT-%s-%d-%d", workerID, now, c)
}

// Map links an internal Intent ID to a Broker Order ID
func (m *Manager) Map(intentID, orderID string) {
	m.Lock()
	defer m.Unlock()
	m.intentToOrder[intentID] = orderID
	m.orderToIntent[orderID] = intentID
}

// GetOrderID retrieves the Broker Order ID given an Intent ID
func (m *Manager) GetOrderID(intentID string) (string, bool) {
	m.RLock()
	defer m.RUnlock()
	id, ok := m.intentToOrder[intentID]
	return id, ok
}

// GetIntentID retrieves the internal Intent ID given a Broker Order ID
func (m *Manager) GetIntentID(orderID string) (string, bool) {
	m.RLock()
	defer m.RUnlock()
	id, ok := m.orderToIntent[orderID]
	return id, ok
}
