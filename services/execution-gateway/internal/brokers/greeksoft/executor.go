package greeksoft

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"math"
	"strconv"

	"execution-gateway/internal/trading"
	gs "trading-platform/libs/broker-greeksoft"
)

type Executor struct {
	Client *gs.Client

	// OMSFeed, when non-nil, backs GetVerifiedFills with GreekSoft's live
	// Iris push feed instead of REST order-book polling -- see
	// omsfeed.go's doc comment and the approved TBT-driven OMS/PMS
	// migration plan. Gated by VerifyViaIris rather than presence alone,
	// so a feed can be started (warming up its in-memory state) before
	// being trusted as the verification source of record.
	OMSFeed       *OMSFeed
	VerifyViaIris bool
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

	filledQty := int64(0)
	averagePrice := float64(0)

	if brokerOrderID != "" {
		orderStatus, orderBookRaw, orderFilledQty, orderAveragePrice, err :=
			e.waitOrderStatusFromOrderBook(
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
			filledQty = orderFilledQty
			averagePrice = orderAveragePrice
		}
	}

	return &trading.ExecutionResult{
		IntentID:      intent.IntentID,
		BrokerOrderID: brokerOrderID,
		Status:        status,
		FilledQty:     filledQty,
		FillPrice:     averagePrice,
		EventReason:   eventReason,
		RawResponse:   rawResponse,
	}, nil
}

func (e *Executor) waitOrderStatusFromOrderBook(
	ctx context.Context,
	brokerOrderID string,
	attempts int,
	delay time.Duration,
) (string, string, int64, float64, error) {
	brokerOrderID = strings.TrimSpace(brokerOrderID)
	if brokerOrderID == "" {
		return "", "", 0, 0, fmt.Errorf("broker order id is empty")
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
			return "", "", 0, 0, ctx.Err()
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

			status, raw, matched, ok := findGreeksoftOrderStatus(
				book,
				brokerOrderID,
			)

			if ok {
				fmt.Printf(
					"[GREEKSOFT ORDERBOOK] matched order=%s status=%s\n",
					brokerOrderID,
					status,
				)

				fmt.Printf(
					"[GREEKSOFT ORDERBOOK] matched fields order=%s keys=%v\n",
					brokerOrderID,
					sortedScalarKeys(matched),
				)

				fill, hasFill := greeksoftMapToVerifiedFill(matched)
				if hasFill {
					return status, raw, fill.FilledQty, fill.AveragePrice, nil
				}

				return status, raw, 0, 0, nil
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
		return "", "", 0, 0, lastErr
	}

	return "", "", 0, 0, fmt.Errorf(
		"order %s not found in Greeksoft order book",
		brokerOrderID,
	)
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

func findGreeksoftOrderStatus(
	v interface{},
	brokerOrderID string,
) (string, string, map[string]interface{}, bool) {
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
			return status, string(rawBytes), x, true
		}

		for _, child := range x {
			if status, raw, matched, ok := findGreeksoftOrderStatus(child, target); ok {
				return status, raw, matched, true
			}
		}

	case []interface{}:
		for _, child := range x {
			if status, raw, matched, ok := findGreeksoftOrderStatus(child, target); ok {
				return status, raw, matched, true
			}
		}

	default:
		return "", "", nil, false
	}

	return "", "", nil, false
}

func firstStringValue(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}

	return ""
}

var _ trading.VerifiedFillsProvider = (*Executor)(nil)

func (e *Executor) GetVerifiedFills(
	ctx context.Context,
) ([]trading.BrokerFill, error) {
	if e == nil || e.Client == nil {
		return nil, fmt.Errorf("greeksoft executor client is nil")
	}

	if e.VerifyViaIris && e.OMSFeed != nil {
		return e.OMSFeed.GetVerifiedFills(ctx)
	}

	book, err := e.Client.GetOrderBook(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Greeksoft order book: %w", err)
	}

	fills := make([]trading.BrokerFill, 0)
	seen := make(map[string]struct{})

	collectGreeksoftVerifiedFills(book, &fills, seen)

	return fills, nil
}

func collectGreeksoftVerifiedFills(
	value interface{},
	fills *[]trading.BrokerFill,
	seen map[string]struct{},
) {
	switch node := value.(type) {
	case map[string]interface{}:
		if fill, ok := greeksoftMapToVerifiedFill(node); ok {
			key := fill.BrokerOrderID
			if _, duplicate := seen[key]; !duplicate {
				seen[key] = struct{}{}
				*fills = append(*fills, fill)
			}
			return
		}

		for _, child := range node {
			collectGreeksoftVerifiedFills(child, fills, seen)
		}

	case []interface{}:
		for _, child := range node {
			collectGreeksoftVerifiedFills(child, fills, seen)
		}
	}
}

func greeksoftMapToVerifiedFill(
	m map[string]interface{},
) (trading.BrokerFill, bool) {
	brokerOrderID := firstGreeksoftOrderID(
		m,
		"gorderid",
		"gOrderID",
		"gOrderId",
		"ordID",
		"orderId",
		"orderID",
		"OrderID",
	)
	if brokerOrderID == "" || brokerOrderID == "0" {
		return trading.BrokerFill{}, false
	}

	rawStatus := firstStringValue(
		m,
		"OrderStatus",
		"orderStatus",
		"order_status",
		"status",
		"Status",
		"ordStatus",
	)
	status := normalizeGreeksoftOrderStatus(rawStatus)
	if status != "FILLED" && status != "PARTIALLY_FILLED" {
		return trading.BrokerFill{}, false
	}

	filledQty := firstInt64Value(
		m,
		"traded_qty",
		"tradedQty",
		"TradedQty",
		"filled_qty",
		"filledQty",
		"FilledQty",
		"fill_quantity",
	)
	averagePrice := firstFloat64Value(
		m,
		"AvgTrdPrice",
		"avgTrdPrice",
		"avg_traded_price",
		"average_price",
		"avgPrice",
	)
	if filledQty <= 0 || averagePrice <= 0 {
		return trading.BrokerFill{}, false
	}

	brokerToken := firstInt64Value(
		m,
		"token",
		"gtoken",
		"gToken",
		"instrument_token",
		"instrumentToken",
	)

	side := normalizeGreeksoftSide(
		firstStringValue(m, "side", "Side", "transactionType"),
	)

	brokerTime := greeksoftTimeValue(m)

	return trading.BrokerFill{
		BrokerOrderID: brokerOrderID,
		Token:         resolveGreeksoftShortToken(brokerToken),
		BrokerToken:   brokerToken,
		Side:          side,
		FilledQty:     filledQty,
		AveragePrice:  averagePrice,
		Status:        status,
		StrategyKey: firstStringValue(
			m,
			"strategyName",
			"StrategyName",
			"strategy_key",
		),
		BrokerTag: firstStringValue(m, "tag", "Tag"),
		BrokerUserTag: firstStringValue(
			m,
			"userTag",
			"UserTag",
			"user_tag",
		),
		OptionType: firstStringValue(
			m,
			"optionType",
			"OptionType",
			"option_type",
		),
		Strike: firstFloat64Value(
			m,
			"strikePrice",
			"StrikePrice",
			"strike_price",
		),
		Symbol: firstStringValue(
			m,
			"scripName",
			"symbol",
			"Symbol",
			"underlying",
		),
		TradeSymbol: firstStringValue(
			m,
			"tradeSymbol",
			"TradeSymbol",
			"tradingSymbol",
		),
		BrokerTime: brokerTime,
		Verified:   true,
		Source:     "GREEKSOFT_ORDERBOOK",
	}, true
}

func normalizeGreeksoftSide(value string) string {
	side := strings.ToUpper(strings.TrimSpace(value))

	switch side {
	case "1", "BUY", "B":
		return "BUY"
	case "2", "SELL", "S":
		return "SELL"
	default:
		return side
	}
}

func firstGreeksoftOrderID(
	m map[string]interface{},
	keys ...string,
) string {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}

		orderID := normalizeGreeksoftOrderID(value)
		if orderID != "" && orderID != "0" {
			return orderID
		}
	}

	return ""
}

func firstInt64Value(
	m map[string]interface{},
	keys ...string,
) int64 {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if parsed, ok := greeksoftInt64Value(value); ok {
				return parsed
			}
		}
	}

	return 0
}

func firstFloat64Value(
	m map[string]interface{},
	keys ...string,
) float64 {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if parsed, ok := greeksoftFloat64Value(value); ok {
				return parsed
			}
		}
	}

	return 0
}

func greeksoftInt64Value(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float32:
		return int64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		if err == nil {
			return parsed, true
		}
		asFloat, floatErr := v.Float64()
		if floatErr != nil {
			return 0, false
		}
		return int64(asFloat), true
	case string:
		normalized := strings.ReplaceAll(strings.TrimSpace(v), ",", "")
		if normalized == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(normalized, 10, 64)
		if err == nil {
			return parsed, true
		}
		asFloat, floatErr := strconv.ParseFloat(normalized, 64)
		if floatErr != nil || math.IsNaN(asFloat) || math.IsInf(asFloat, 0) {
			return 0, false
		}
		return int64(asFloat), true
	default:
		return 0, false
	}
}

func greeksoftFloat64Value(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	case string:
		normalized := strings.ReplaceAll(strings.TrimSpace(v), ",", "")
		if normalized == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(normalized, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func greeksoftTimeValue(
	m map[string]interface{},
) time.Time {
	// Greeksoft fields such as lu_time_exchange/LogTime were observed to
	// decode to an incorrect 2016 date for 2026 orders. Do not persist a
	// fabricated broker execution time. The raw broker order remains
	// available for later timestamp-format validation.
	_ = m
	return time.Time{}
}

func sortedScalarKeys(m map[string]interface{}) []string {
	keys := make([]string, 0)

	for key, value := range m {
		switch value.(type) {
		case map[string]interface{}, []interface{}:
			continue
		default:
			keys = append(keys, key)
		}
	}

	sort.Strings(keys)
	return keys
}
