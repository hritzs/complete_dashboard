package greeksoft

import (
	"context"
	"fmt"
	"net/url"
)

// GetOrderDetail looks up a single order by GreekSoft's own order number.
// Note gorderid is only unique per trading day (see
// docs/greeksoft-integration-architecture.md) -- this call alone cannot
// disambiguate across days; scope any caching/correlation accordingly.
// Response shape not confirmed against a live capture (the Postman
// collection request has no saved example response) -- decoded generically
// pending that.
func (c *Client) GetOrderDetail(ctx context.Context, greekOrderNo string, gscid string) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	query := url.Values{}
	query.Set("greekOrderNo", greekOrderNo)
	query.Set("gscid", gscid)
	reqURL := fmt.Sprintf("%s/getOrderDetail?%s", c.RestAPIBaseURL, query.Encode())

	var resBody map[string]interface{}
	if _, _, err := c.getJSON(ctx, reqURL, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}

// GetTradeDetail looks up trade/fill detail for a given order number. Same
// per-trading-day scoping caveat as GetOrderDetail applies. Response shape
// not confirmed against a live capture; decoded generically pending that.
func (c *Client) GetTradeDetail(ctx context.Context, greekOrderNo string, gscid string) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	query := url.Values{}
	query.Set("greekOrderNo", greekOrderNo)
	query.Set("gscid", gscid)
	reqURL := fmt.Sprintf("%s/getTradeDetail?%s", c.RestAPIBaseURL, query.Encode())

	var resBody map[string]interface{}
	if _, _, err := c.getJSON(ctx, reqURL, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}
