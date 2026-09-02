package greeksoft

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func (c *Client) CancelOrder(
	ctx context.Context,
	orderID string,
	extraArgs map[string]interface{},
) error {
	if orderID == "" {
		return fmt.Errorf("order id cannot be empty")
	}

	if c.Session == nil {
		return fmt.Errorf("greeksoft session is nil; login first")
	}

	if c.Session.AuthToken == "" {
		return fmt.Errorf("greeksoft session token missing")
	}

	cancelURL := fmt.Sprintf("%s/Order/%s", c.RestAPIBaseURL, url.PathEscape(orderID))

	_, raw, err := c.doJSON(
		ctx,
		http.MethodDelete,
		cancelURL,
		c.Session.AuthToken,
		nil,
		nil,
	)
	if err != nil {
		return fmt.Errorf("greeksoft cancel order failed: %w", err)
	}

	_ = raw
	return nil
}

func (c *Client) GetOrderBook(
	ctx context.Context,
) (interface{}, error) {
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
	gscid := c.Session.UserID

	query := url.Values{}
	query.Set("exchangeType", "ALL")
	query.Set("ClientCode", gcid)
	query.Set("Order_Status", "ALL")
	query.Set("Ordertype", "ALL")
	query.Set("gscid", gscid)

	orderBookURL := fmt.Sprintf(
		"%s/getOrderBookDetailWithLegV2?%s",
		c.RestAPIBaseURL,
		query.Encode(),
	)

	var response map[string]interface{}

	_, raw, err := c.doJSON(
		ctx,
		http.MethodGet,
		orderBookURL,
		c.Session.AuthToken,
		nil,
		&response,
	)
	if err != nil {
		return nil, fmt.Errorf("greeksoft get order book failed: %w", err)
	}

	if response == nil {
		return string(raw), nil
	}

	return response, nil
}
