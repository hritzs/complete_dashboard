package greeksoft

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gs "trading-platform/libs/broker-greeksoft"

	"execution-gateway/internal/trading"
)

type Executor struct {
	Client *gs.Client
}

func NewExecutor(client *gs.Client) *Executor {
	return &Executor{
		Client: client,
	}
}

func (e *Executor) ExecuteOrderIntent(
	ctx context.Context,
	intent trading.OrderIntent,
) (*trading.ExecutionResult, error) {
	req := MapOrderIntent(intent)

	resp, err := e.Client.PlaceOrder(ctx, req)
	if err != nil {
		return nil, err
	}

	brokerOrderID := strings.TrimSpace(resp.OrderID)
	status := strings.TrimSpace(resp.Status)
	rawResponse := resp.RawResponse
	eventReason := "GREEKSOFT_ORDER_SENT"

	if status == "" {
		status = "SUBMITTED"
	}

	if brokerOrderID != "" {
		orderStatus, orderBookRaw, err := e.waitOrderStatusFromOrderBook(
			ctx,
			brokerOrderID,
			8,
			250*time.Millisecond,
		)
		if err == nil && strings.TrimSpace(orderStatus) != "" {
			status = normalizeGreeksoftOrderStatus(orderStatus)
			eventReason = "GREEKSOFT_ORDERBOOK_CONFIRMED"
			if strings.TrimSpace(orderBookRaw) != "" {
				rawResponse = orderBookRaw
			}
		}
	}

	return &trading.ExecutionResult{
		IntentID:      intent.IntentID,
		BrokerOrderID: brokerOrderID,
		Status:        status,
		FilledQty:     0,
		FillPrice:     0,
		EventReason:   eventReason,
		RawResponse:   rawResponse,
	}, nil
}

func (e *Executor) waitOrderStatusFromOrderBook(
	ctx context.Context,
	brokerOrderID string,
	attempts int,
	delay time.Duration,
) (string, string, error) {
	brokerOrderID = strings.TrimSpace(brokerOrderID)
	if brokerOrderID == "" {
		return "", "", fmt.Errorf("broker order id is empty")
	}

	if attempts <= 0 {
		attempts = 1
	}

	if delay <= 0 {
		delay = 250 * time.Millisecond
	}

	var lastErr error

	for i := 0; i < attempts; i++ {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		book, err := e.Client.GetOrderBook(ctx)
		if err != nil {
			lastErr = err
		} else {
			status, raw, ok := findGreeksoftOrderStatus(book, brokerOrderID)
			if ok {
				return status, raw, nil
			}
		}

		if i < attempts-1 {
			time.Sleep(delay)
		}
	}

	if lastErr != nil {
		return "", "", lastErr
	}

	return "", "", fmt.Errorf("order %s not found in Greeksoft order book", brokerOrderID)
}

func normalizeGreeksoftOrderStatus(status string) string {
	s := strings.ToUpper(strings.TrimSpace(status))

	switch s {
	case "FILLED", "COMPLETE", "COMPLETED", "TRADED", "EXECUTED":
		return "FILLED"
	case "REJECTED", "REJECT", "FAILED":
		return "REJECTED"
	case "CANCELLED", "CANCELED":
		return "CANCELLED"
	case "PARTIALLYFILLED", "PARTIAL", "PARTIALLY_FILLED":
		return "PARTIALLY_FILLED"
	case "OPEN", "NEW", "PENDING", "PENDINGNEW", "PENDING_NEW":
		return "OPEN"
	case "SUBMITTED", "ACKED", "ACKNOWLEDGED":
		return "SUBMITTED"
	default:
		if s == "" {
			return "SUBMITTED"
		}
		return s
	}
}

func findGreeksoftOrderStatus(v interface{}, brokerOrderID string) (string, string, bool) {
	target := strings.TrimSpace(brokerOrderID)

	switch x := v.(type) {
	case map[string]interface{}:
		if mapHasOrderID(x, target) {
			status := firstStringValue(
				x,
				"OrderStatus",
				"orderStatus",
				"order_status",
				"status",
				"Status",
				"ordStatus",
				"order_state",
			)

			rawBytes, _ := json.Marshal(x)
			return status, string(rawBytes), true
		}

		for _, child := range x {
			if status, raw, ok := findGreeksoftOrderStatus(child, target); ok {
				return status, raw, true
			}
		}

	case []interface{}:
		for _, child := range x {
			if status, raw, ok := findGreeksoftOrderStatus(child, target); ok {
				return status, raw, true
			}
		}

	default:
		return "", "", false
	}

	return "", "", false
}

func mapHasOrderID(m map[string]interface{}, target string) bool {
	keys := []string{
		"gorderid",
		"gOrderID",
		"gOrderId",
		"orderId",
		"orderID",
		"OrderID",
		"AppOrderID",
		"broker_order_id",
	}

	for _, key := range keys {
		if v, ok := m[key]; ok {
			if strings.TrimSpace(fmt.Sprintf("%v", v)) == target {
				return true
			}
		}
	}

	return false
}

func firstStringValue(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}

	return ""
}
