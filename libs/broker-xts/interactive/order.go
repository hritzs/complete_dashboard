package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	broker "trading-platform/libs/go-broker"
)

type XTSPlaceOrderRequest struct {
	ExchangeSegment       string  `json:"exchangeSegment"`
	ExchangeInstrumentID  int     `json:"exchangeInstrumentID"`
	ProductType           string  `json:"productType"`
	OrderType             string  `json:"orderType"`
	OrderSide             string  `json:"orderSide"`
	TimeInForce           string  `json:"timeInForce"`
	DisclosedQuantity     int     `json:"disclosedQuantity"`
	OrderQuantity         int     `json:"orderQuantity"`
	LimitPrice            float64 `json:"limitPrice"`
	StopPrice             float64 `json:"stopPrice"`
	OrderUniqueIdentifier string  `json:"orderUniqueIdentifier"`
	ClientID              string  `json:"clientID"`
}

type XTSPlaceOrderResponse struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Result      struct {
		AppOrderID string `json:"AppOrderID"`
	} `json:"result"`
}

// PlaceOrder translates an internal OrderIntent into an XTS Interactive HTTP request
func PlaceOrder(ctx context.Context, baseURL, token, clientID string, intent *broker.OrderIntent) (*broker.OrderResponse, error) {
	url := baseURL + "/interactive/orders"

	reqPayload := XTSPlaceOrderRequest{
		ExchangeSegment:       intent.ExchangeSegment,
		ExchangeInstrumentID:  intent.InstrumentToken,
		ProductType:           "MIS",
		OrderType:             "LIMIT",
		OrderSide:             intent.Side,
		TimeInForce:           "DAY",
		DisclosedQuantity:     0,
		OrderQuantity:         intent.Quantity,
		LimitPrice:            intent.LimitPrice,
		StopPrice:             0,
		OrderUniqueIdentifier: intent.IntentID,
		ClientID:              clientID,
	}

	reqBody, _ := json.Marshal(reqPayload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("XTS order request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var orderRes XTSPlaceOrderResponse
	if err := json.Unmarshal(bodyBytes, &orderRes); err != nil || orderRes.Type != "success" {
		return nil, fmt.Errorf("XTS order placement failed: %s", orderRes.Description)
	}

	return &broker.OrderResponse{
		Status:        "SUBMITTED",
		BrokerOrderID: orderRes.Result.AppOrderID,
		RawResponse:   string(bodyBytes),
	}, nil
}
