package greeksoft

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

	filledQty := int64(0)
	pendingQty := int64(0)
	fillPrice := float64(0)

	if status == "" {
		status = "SUBMITTED"
	}

	if brokerOrderID == "" {
		return &trading.ExecutionResult{
			IntentID:      intent.IntentID,
			BrokerOrderID: "",
			Status:        "REJECTED",
			FilledQty:     0,
			FillPrice:     0,
			EventReason:   "GREEKSOFT_ORDER_REJECTED_NO_ORDER_ID",
			RawResponse:   rawResponse,
		}, nil
	}

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

		filledQty, pendingQty, fillPrice = extractGreeksoftExecutionFields(rawResponse)

		if filledQty > 0 && status != "REJECTED" && status != "CANCELLED" {
			if pendingQty == 0 && intent.Quantity > 0 && filledQty >= intent.Quantity {
				status = "FILLED"
			} else {
				status = "PARTIALLY_FILLED"
			}
		}
	}

	return &trading.ExecutionResult{
		IntentID:      intent.IntentID,
		BrokerOrderID: brokerOrderID,
		Status:        status,
		FilledQty:     filledQty,
		PendingQty:    pendingQty,
		FillPrice:     fillPrice,
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

			fmt.Printf(
				"[GREEKSOFT ORDERBOOK] order=%s error=%v\n",
				brokerOrderID,
				err,
			)
		} else {

			fmt.Printf(
				"[GREEKSOFT ORDERBOOK] order=%s lookup_success\n",
				brokerOrderID,
			)

			status, raw, ok := findGreeksoftOrderStatus(
				book,
				brokerOrderID,
			)

			if ok {

				fmt.Printf(
					"[GREEKSOFT ORDERBOOK] matched order=%s status=%s\n",
					brokerOrderID,
					status,
				)

				return status, raw, nil
			}

			rawBytes, _ := json.Marshal(book)

			fmt.Printf(
				"[GREEKSOFT ORDERBOOK] order=%s not_found response=%s\n",
				brokerOrderID,
				string(rawBytes),
			)

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

	switch {
	case s == "":
		return "SUBMITTED"

	case strings.Contains(s, "RMS") && strings.Contains(s, "REJECT"):
		return "REJECTED"

	case strings.Contains(s, "REJECT"):
		return "REJECTED"

	case s == "FILLED" ||
		s == "COMPLETE" ||
		s == "COMPLETED" ||
		s == "TRADED" ||
		s == "EXECUTED":
		return "FILLED"

	case s == "CANCELLED" ||
		s == "CANCELED":
		return "CANCELLED"

	case s == "PARTIALLYFILLED" ||
		s == "PARTIAL" ||
		s == "PARTIALLY_FILLED" ||
		s == "PARTIALLY FILLED":
		return "PARTIALLY_FILLED"

	case s == "OPEN" ||
		s == "NEW" ||
		s == "PENDING" ||
		s == "PENDINGNEW" ||
		s == "PENDING_NEW" ||
		s == "PENDING NEW":
		return "OPEN"

	case s == "SUBMITTED" ||
		s == "ACKED" ||
		s == "ACKNOWLEDGED":
		return "SUBMITTED"

	default:
		return s
	}
}

func mapHasOrderID(m map[string]interface{}, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}

	keys := []string{
		"gorderid",
		"gOrderID",
		"gOrderId",
		"ordID",
		"orderId",
		"orderID",
		"OrderID",
		"AppOrderID",
		"broker_order_id",
	}

	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}

		if normalizeGreeksoftOrderID(v) == target {
			return true
		}
	}

	return false
}

func normalizeGreeksoftOrderID(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""

	case string:
		return strings.TrimSpace(x)

	case int:
		return fmt.Sprintf("%d", x)

	case int8:
		return fmt.Sprintf("%d", x)

	case int16:
		return fmt.Sprintf("%d", x)

	case int32:
		return fmt.Sprintf("%d", x)

	case int64:
		return fmt.Sprintf("%d", x)

	case uint:
		return fmt.Sprintf("%d", x)

	case uint8:
		return fmt.Sprintf("%d", x)

	case uint16:
		return fmt.Sprintf("%d", x)

	case uint32:
		return fmt.Sprintf("%d", x)

	case uint64:
		return fmt.Sprintf("%d", x)

	case float32:
		return fmt.Sprintf("%.0f", x)

	case float64:
		return fmt.Sprintf("%.0f", x)

	case json.Number:
		return strings.TrimSpace(x.String())

	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
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

func firstStringValue(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}

	return ""
}

func extractGreeksoftExecutionFields(raw string) (int64, int64, float64) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, 0
	}

	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0, 0, 0
	}

	filledQty := int64(firstFloatLikeField(v,
		"tradedqty",
		"traded_qty",
		"tradedQty",
		"filledqty",
		"filled_qty",
		"filledQty",
		"execQty",
		"executedQty",
		"CumulativeQuantity",
		"cumulative_quantity",
	))

	pendingQty := int64(firstFloatLikeField(v,
		"pendingqty",
		"pending_qty",
		"pendingQty",
		"remainingQty",
		"LeavesQuantity",
		"leaves_quantity",
	))

	avgPrice := firstFloatLikeField(v,
		"AvgTrdPrice",
		"avgTrdPrice",
		"avg_trd_price",
		"OrderAverageTradedPrice",
		"order_avg_price",
		"avg_price",
		"fillPrice",
		"fill_price",
	)

	return filledQty, pendingQty, avgPrice
}

func firstFloatLikeField(v interface{}, keys ...string) float64 {
	want := map[string]bool{}
	for _, k := range keys {
		want[normalizeGreeksoftFieldKey(k)] = true
	}

	var walk func(interface{}) (float64, bool)
	walk = func(x interface{}) (float64, bool) {
		switch t := x.(type) {
		case map[string]interface{}:
			for k, v := range t {
				if want[normalizeGreeksoftFieldKey(k)] {
					if f, ok := numberLikeToFloat64(v); ok {
						return f, true
					}
				}
			}
			for _, child := range t {
				if f, ok := walk(child); ok {
					return f, true
				}
			}
		case []interface{}:
			for _, child := range t {
				if f, ok := walk(child); ok {
					return f, true
				}
			}
		}
		return 0, false
	}

	if f, ok := walk(v); ok {
		return f
	}
	return 0
}

func normalizeGreeksoftFieldKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func numberLikeToFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	default:
		return 0, false
	}
}
