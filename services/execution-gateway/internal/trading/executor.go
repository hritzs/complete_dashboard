package trading

import (
    "context"
    "fmt"
    "log"
    "math/rand"
    "time"
)

// MockExecutor is a UAT/test executor that simulates broker fills.
type MockExecutor struct{}

// NewMockExecutor constructs a new MockExecutor.
func NewMockExecutor() *MockExecutor {
    rand.Seed(time.Now().UnixNano())
    return &MockExecutor{}
}

// ExecuteOrderIntent simulates order execution and always returns FILLED.
func (m *MockExecutor) ExecuteOrderIntent(ctx context.Context, intent OrderIntent) (*ExecutionResult, error) {
    start := time.Now()

    // Simulate small network / broker latency while respecting context.
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-time.After(2 * time.Millisecond):
    }

    // Choose fill price: prefer limit if set, else expected.
    fillPrice := intent.ExpectedPrice
    if intent.LimitPrice != nil && *intent.LimitPrice > 0 {
        fillPrice = *intent.LimitPrice
    }
    if fillPrice <= 0 {
        fillPrice = 1
    }

    brokerOID := fmt.Sprintf("MOCK-%d", rand.Int63())
    latency := time.Since(start).Milliseconds()

    log.Printf(
        "[MOCK EXECUTOR] phase=%s leg=%s token=%d qty=%d oid=%s latency_ms=%d",
        intent.Phase,
        intent.LegType,
        intent.Token,
        intent.Quantity,
        brokerOID,
        latency,
    )

    return &ExecutionResult{
        IntentID:      intent.IntentID,
        BrokerOrderID: brokerOID,
        Status:        "FILLED",
        FilledQty:     intent.Quantity,
        FillPrice:     fillPrice,
        EventReason:   "UAT_MOCK",
        LatencyMS:     latency,
        RawRequest:    "",
        RawResponse:   "",
    }, nil
}