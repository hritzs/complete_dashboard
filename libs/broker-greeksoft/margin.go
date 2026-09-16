package greeksoft

import (
	"context"
	"fmt"
)

type marginRequestData struct {
	GCID         string `json:"gcid"`
	SessionID    string `json:"sessionId"`
	Segment      int    `json:"segment"`
	ExchangeType string `json:"exchange_type"`
}

// MarginDetailRequest returns margin usage for the account/segment.
// Response shape not confirmed against a live capture (the Postman
// collection request has no saved example response); decoded generically
// pending that.
func (c *Client) MarginDetailRequest(ctx context.Context, segment int, exchangeType string) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}
	if c.Session.BrokerSpecific == nil {
		return nil, fmt.Errorf("greeksoft broker-specific session data missing")
	}

	gcidValue, ok := c.Session.BrokerSpecific["gcid"]
	if !ok || fmt.Sprintf("%v", gcidValue) == "" {
		return nil, fmt.Errorf("greeksoft GCID missing; jloginNew not completed")
	}
	gcid := fmt.Sprintf("%v", gcidValue)
	sessionID := brokerSpecificString(c.Session.BrokerSpecific, "session_id")

	url := fmt.Sprintf("%s/MarginDetailRequest", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			FormFactor: "M",
			Data: marginRequestData{
				GCID:         gcid,
				SessionID:    sessionID,
				Segment:      segment,
				ExchangeType: exchangeType,
			},
			SvcGroup:      "portfolio",
			SvcVersion:    "1.0.0",
			StreamingType: "MarginDetailRequest",
			RequestType:   "subscribe",
		},
	}

	var resBody map[string]interface{}
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}

type holdingValueRequestData struct {
	GCID      string `json:"gcid"`
	Gscid     string `json:"gscid"`
	SessionID string `json:"sessionId"`
}

// GetHoldingValueInfo returns holdings valuation.
//
// GreekSoft's official docs show this as a POST under svcGroup=portfolio
// (matching NPRequest/MarginDetailRequest's family), but the Postman
// collection's saved request for the same endpoint is a bodyless GET.
// This implementation follows the docs' POST shape since it's a captured,
// concrete example rather than an apparently-incomplete/unset Postman
// entry -- verify against a live account if this doesn't work.
func (c *Client) GetHoldingValueInfo(ctx context.Context) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}
	if c.Session.BrokerSpecific == nil {
		return nil, fmt.Errorf("greeksoft broker-specific session data missing")
	}

	gcidValue, ok := c.Session.BrokerSpecific["gcid"]
	if !ok || fmt.Sprintf("%v", gcidValue) == "" {
		return nil, fmt.Errorf("greeksoft GCID missing; jloginNew not completed")
	}
	gcid := fmt.Sprintf("%v", gcidValue)
	sessionID := brokerSpecificString(c.Session.BrokerSpecific, "session_id")

	url := fmt.Sprintf("%s/HoldingValueInfo", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			FormFactor: "M",
			Data: holdingValueRequestData{
				GCID:      gcid,
				Gscid:     c.Session.UserID,
				SessionID: sessionID,
			},
			SvcGroup:      "portfolio",
			SvcVersion:    "1.0.0",
			StreamingType: "HoldingValueInfo",
			RequestType:   "subscribe",
		},
	}

	var resBody map[string]interface{}
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}
