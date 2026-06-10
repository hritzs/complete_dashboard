package events

const (
	// Subjects for order and trade lifecycle events
	TopicOrderUpdates = "orders.update" // Publishes contracts.OrderUpdate
	TopicFillEvents   = "orders.fill"     // Publishes contracts.FillEvent
	TopicTradeUpdates = "trades.update"   // Publishes contracts.TradeSnapshot

	// Subjects for worker and system monitoring
	TopicWorkerHeartbeat = "workers.heartbeat" // Publishes contracts.WorkerHeartbeat
)