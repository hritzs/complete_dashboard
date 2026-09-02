package xts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"execution-gateway/internal/trading"
)

type Executor struct {
	BaseURL  string
	Token    string
	ClientID string
	HTTP     *http.Client
	sem      chan struct{}
}

func NewExecutor(baseURL, token, clientID string) *Executor {
	return &Executor{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Token:    token,
		ClientID: clientID,
		HTTP:     &http.Client{Timeout: 5 * time.Second},
		sem:      make(chan struct{}, 4),
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (e *Executor) ExecuteOrderIntent(ctx context.Context, intent trading.OrderIntent) (*trading.ExecutionResult, error) {
	start := time.Now()

	if strings.TrimSpace(e.BaseURL) == "" {
		return nil, fmt.Errorf("xts base url is empty")
	}
	if strings.TrimSpace(e.Token) == "" {
		return nil, fmt.Errorf("xts token is empty")
	}

	clientID := strings.TrimSpace(intent.AccountID)
	if clientID == "" {
		clientID = strings.TrimSpace(e.ClientID)
	}
	if clientID == "" {
		return nil, fmt.Errorf("xts client id is empty")
	}

	orderUID := strings.TrimSpace(intent.OrderUID)
	if orderUID == "" {
		orderUID = strings.TrimSpace(intent.IntentID)
	}
	if orderUID == "" {
		orderUID = fmt.Sprintf("OID-%d", time.Now().UnixNano())
	}
	orderUID = orderUID[:min(len(orderUID), 20)]

	orderType := strings.ToUpper(strings.TrimSpace(intent.OrderType))
	if orderType == "" {
		orderType = "LIMIT"
	}

	productType := strings.ToUpper(strings.TrimSpace(intent.ProductType))
	if productType == "" {
		productType = "MIS"
	}

	side := strings.ToUpper(strings.TrimSpace(intent.Side))
	if side == "" {
		return nil, fmt.Errorf("order side is required")
	}

	if intent.Quantity <= 0 {
		return nil, fmt.Errorf("invalid quantity for token %d: %d", intent.Token, intent.Quantity)
	}
	if intent.Token <= 0 {
		return nil, fmt.Errorf("invalid token: %d", intent.Token)
	}

	exchangeSegment := strings.ToUpper(strings.TrimSpace(intent.ExchangeSegment))
	if exchangeSegment == "" {
		exchangeSegment = "NSEFO"
	}

	limitPrice := 0.0
	if intent.LimitPrice != nil {
		limitPrice = *intent.LimitPrice
	}

	payload := map[string]interface{}{
		"exchangeSegment":       exchangeSegment,
		"exchangeInstrumentID":  intent.Token,
		"productType":           productType,
		"orderType":             orderType,
		"orderSide":             side,
		"timeInForce":           "DAY",
		"disclosedQuantity":     0,
		"orderQuantity":         intent.Quantity,
		"limitPrice":            limitPrice,
		"stopPrice":             0.0,
		"orderUniqueIdentifier": orderUID,
		"clientID":              clientID,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal xts payload: %w", err)
	}

	e.sem <- struct{}{}
	defer func() { <-e.sem }()
	time.Sleep(50 * time.Millisecond)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/interactive/orders", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create xts request: %w", err)
	}
	req.Header.Set("Authorization", e.Token)
	req.Header.Set("Content-Type", "application/json")

	log.Printf(
		"[XTS] placing order phase=%s leg=%s seg=%s token=%d side=%s qty=%d limit=%.2f uid=%s",
		intent.Phase,
		intent.LegType,
		exchangeSegment,
		intent.Token,
		side,
		intent.Quantity,
		limitPrice,
		orderUID,
	)

	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xts http do: %w", err)
	}
	defer resp.Body.Close()

	var xtsResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&xtsResp); err != nil {
		return nil, fmt.Errorf("decode xts response: %w", err)
	}

	rawResp, _ := json.Marshal(xtsResp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		desc := fmt.Sprintf("%v", xtsResp["description"])
		if desc == "" || desc == "<nil>" {
			desc = resp.Status
		}
		return nil, fmt.Errorf("xts rejected order: %s", desc)
	}

	if fmt.Sprintf("%v", xtsResp["type"]) != "success" {
		desc := fmt.Sprintf("%v", xtsResp["description"])
		return nil, fmt.Errorf("xts rejected order: %s", desc)
	}

	resultMap, _ := xtsResp["result"].(map[string]interface{})
	brokerOrderID := fmt.Sprintf("%v", resultMap["AppOrderID"])
	if brokerOrderID == "" || brokerOrderID == "<nil>" {
		brokerOrderID = fmt.Sprintf("%v", resultMap["OrderID"])
	}

	latency := time.Since(start).Milliseconds()

	log.Printf(
		"[XTS] submitted phase=%s leg=%s token=%d qty=%d oid=%s latency_ms=%d",
		intent.Phase,
		intent.LegType,
		intent.Token,
		intent.Quantity,
		brokerOrderID,
		latency,
	)

	return &trading.ExecutionResult{
		IntentID:      intent.IntentID,
		BrokerOrderID: brokerOrderID,
		Status:        "SUBMITTED",
		FilledQty:     0,
		FillPrice:     0,
		EventReason:   "LIVE_XTS_SUBMISSION",
		RawRequest:    string(bodyBytes),
		RawResponse:   string(rawResp),
		LatencyMS:     latency,
	}, nil
}
