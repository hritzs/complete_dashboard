package greeksoft

import (
	"context"
	"fmt"

	broker "trading-platform/libs/go-broker"
)

type modifyOrderRequestData struct {
	TriggerPrice string `json:"trigger_price"`
	GCID         string `json:"gcid"`
	Validity     string `json:"validity"`
	Price        string `json:"price"`
	DisclosedQty string `json:"disclosed_qty"`
	OrderType    string `json:"order_type"`
	Lot          string `json:"lot"`
	Qty          string `json:"qty"`
	GOrderID     string `json:"gorderid"`
	AMO          string `json:"amo"`
	SLPrice      string `json:"sl_price"`
	GTDExpiry    int    `json:"gtdExpiry"`
}

// ModifyOrderRequest is the caller-facing input to ModifyOrder.
type ModifyOrderRequest struct {
	GOrderID      string // GreekSoft's own order id (gorderid) -- only unique per trading day, see docs/greeksoft-integration-architecture.md
	Price         float64
	TriggerPrice  float64
	StopLossPrice float64
	Qty           int
	Lot           int
	DisclosedQty  int
	Validity      string // mapped via mapTimeInForce, same as PlaceOrder
	OrderType     string // mapped the same way orders.go's PlaceOrder maps it
}

// ModifyOrder submits SmallModifyOrderRequest for an existing order.
func (c *Client) ModifyOrder(ctx context.Context, req *ModifyOrderRequest) (*broker.OrderResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("modify order request is nil")
	}
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}
	if c.Session.BrokerSpecific == nil {
		return nil, fmt.Errorf("greeksoft broker-specific session data missing")
	}
	if req.GOrderID == "" {
		return nil, fmt.Errorf("missing gorderid to modify")
	}

	gcidValue, ok := c.Session.BrokerSpecific["gcid"]
	if !ok || fmt.Sprintf("%v", gcidValue) == "" {
		return nil, fmt.Errorf("greeksoft GCID missing; jloginNew not completed")
	}
	gcid := fmt.Sprintf("%v", gcidValue)

	orderType := "1"
	if req.OrderType != "" {
		orderType = mapOrderTypeForModify(req.OrderType)
	}

	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			Data: modifyOrderRequestData{
				TriggerPrice: formatPrice(req.TriggerPrice),
				GCID:         gcid,
				Validity:     mapTimeInForce(req.Validity),
				Price:        formatPrice(req.Price),
				DisclosedQty: fmt.Sprintf("%d", req.DisclosedQty),
				OrderType:    orderType,
				Lot:          fmt.Sprintf("%d", req.Lot),
				Qty:          fmt.Sprintf("%d", req.Qty),
				GOrderID:     req.GOrderID,
				AMO:          "0",
				SLPrice:      formatPrice(req.StopLossPrice),
				GTDExpiry:    0,
			},
			ResponseFormat: "json",
			RequestType:    "subscribe",
			StreamingType:  "ModifyOrderRequest",
		},
	}

	url := fmt.Sprintf("%s/SmallModifyOrderRequest", c.RestAPIBaseURL)

	var resBody newOrderResponse
	_, raw, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody)
	if err != nil {
		return nil, err
	}
	if resBody.Response.ErrorCode != 0 {
		return nil, fmt.Errorf("greeksoft SmallModifyOrderRequest ErrorCode=%d raw=%s", resBody.Response.ErrorCode, string(raw))
	}

	return &broker.OrderResponse{
		OrderID:       req.GOrderID,
		BrokerOrderID: req.GOrderID,
		Status:        "MODIFIED",
		Message:       "Greeksoft SmallModifyOrderRequest submitted",
		RawResponse:   string(raw),
	}, nil
}

func mapOrderTypeForModify(orderType string) string {
	switch orderType {
	case "MARKET":
		return "2"
	default:
		return "1"
	}
}

func formatPrice(p float64) string {
	if p <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.2f", p)
}
