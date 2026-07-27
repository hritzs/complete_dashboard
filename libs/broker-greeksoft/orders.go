package greeksoft

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	broker "trading-platform/libs/go-broker"
)

func (c *Client) PlaceOrder(ctx context.Context, intent *broker.OrderIntent) (*broker.OrderResponse, error) {
	if intent == nil {
		return nil, fmt.Errorf("order intent is nil")
	}

	if c.Session == nil {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	if c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session token missing")
	}

	if c.Session.BrokerSpecific == nil {
		return nil, fmt.Errorf("greeksoft broker-specific session data missing")
	}

	gcidValue, ok := c.Session.BrokerSpecific["gcid"]
	if !ok || fmt.Sprintf("%v", gcidValue) == "" {
		return nil, fmt.Errorf("greeksoft GCID missing; jloginNew not completed")
	}

	gcid := fmt.Sprintf("%v", gcidValue)

	if intent.InstrumentToken <= 0 {
		return nil, fmt.Errorf("missing Greeksoft gtoken/instrument token")
	}

	if intent.Quantity <= 0 {
		return nil, fmt.Errorf("invalid quantity=%d", intent.Quantity)
	}

	sideCode := "1"
	if strings.EqualFold(intent.Side, "SELL") {
		sideCode = "2"
	}

	orderType := "1"
	if strings.EqualFold(intent.OrderType, "MARKET") {
		orderType = "2"
	}

	price := fmt.Sprintf("%.2f", intent.LimitPrice)
	if strings.EqualFold(intent.OrderType, "MARKET") {
		price = "0"
	}

	tradeSymbol := strings.TrimSpace(intent.Symbol)

	if tradeSymbol == "" {
		return nil, fmt.Errorf("missing symbol")
	}

	disclosedQty := "0"
	if intent.DisclosedQty > 0 {
		disclosedQty = strconv.Itoa(intent.DisclosedQty)
	}

	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			Data: newOrderRequestData{
				TriggerPrice: "0",
				GToken:       strconv.Itoa(intent.InstrumentToken),
				Side:         sideCode,
				GCID:         gcid,
				Validity:     mapTimeInForce(intent.TimeInForce),
				Price:        price,
				Exchange:     normalizeExchange(intent.ExchangeSegment),
				DisclosedQty: disclosedQty,
				TradeSymbol:  tradeSymbol,
				Lot:          "1",
				OrderType:    orderType,
				Product:      mapProduct(intent.ProductType),
				Qty:          strconv.Itoa(intent.Quantity),
				COrderID:     "3",
				AMO:          "0",
				IProCli:      "0",
				GTDExpiry:    0,
				IsPostClosed: "0",
				IsPreOpen:    "0",
				IsSqOffOrder: "false",
				Offline:      "0",
				IsRestAPI:    "1",
				StrategyName: "STOCK",
			},
			ResponseFormat: "json",
			RequestType:    "subscribe",
			StreamingType:  "NewOrderRequest",
		},
	}

	url := fmt.Sprintf("%s/NewOrderRequest", c.RestAPIBaseURL)

	log.Printf(
		"[GREEKSOFT ORDER] sending token=%d side=%s qty=%d order_type=%s price=%s gcid=%s trade_uid=%s intent_id=%s",
		intent.InstrumentToken,
		intent.Side,
		intent.Quantity,
		intent.OrderType,
		price,
		gcid,
		intent.TradeUID,
		intent.IntentID,
	)

	var resBody newOrderResponse
	rawReq, _ := json.Marshal(reqBody)
	log.Printf("[GREEKSOFT ORDER REQUEST] %s", string(rawReq))
	_, raw, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody)
	if err != nil {
		log.Printf("[GREEKSOFT ORDER] failed err=%v raw=%s", err, string(raw))
		return nil, err
	}

	if resBody.Response.ErrorCode != 0 {
		log.Printf("[GREEKSOFT ORDER] rejected error_code=%d raw=%s", resBody.Response.ErrorCode, string(raw))
		return nil, fmt.Errorf("greeksoft NewOrderRequest ErrorCode=%d raw=%s", resBody.Response.ErrorCode, string(raw))
	}

	orderID := extractOrderID(resBody.Response.Data)

	log.Printf(
		"[GREEKSOFT ORDER] submitted order_id=%s status=SUBMITTED raw=%s",
		orderID,
		string(raw),
	)

	return &broker.OrderResponse{
		OrderID:       orderID,
		BrokerOrderID: orderID,
		TradeUID:      intent.TradeUID,
		Status:        "SUBMITTED",
		Message:       "Greeksoft NewOrderRequest submitted",
		RawResponse:   string(raw),
	}, nil
}

func normalizeExchange(exchange string) string {
	exchange = strings.ToUpper(strings.TrimSpace(exchange))

	if strings.HasPrefix(exchange, "NSE") {
		return "NSE"
	}

	if strings.HasPrefix(exchange, "BSE") {
		return "BSE"
	}

	return exchange
}

func mapProduct(product string) string {
	product = strings.ToUpper(strings.TrimSpace(product))

	switch product {
	case "MIS", "INTRADAY":
		return "0"
	case "NRML", "NORMAL", "CNC":
		return "1"
	default:
		return "0"
	}
}

func mapTimeInForce(tif string) string {
	tif = strings.ToUpper(strings.TrimSpace(tif))

	switch tif {
	case "IOC":
		return "1"
	default:
		return "0"
	}
}

func extractOrderID(data map[string]interface{}) string {
	if data == nil {
		return ""
	}

	keys := []string{
		"gorderid",
		"gOrderID",
		"greekOrderNo",
		"orderId",
		"OrderID",
		"order_id",
	}

	for _, key := range keys {
		if value, ok := data[key]; ok {
			return fmt.Sprintf("%v", value)
		}
	}

	return ""
}
