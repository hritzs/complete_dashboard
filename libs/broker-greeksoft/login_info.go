package greeksoft

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type loginInfoRequestData struct {
	Gscid string `json:"gscid"`
}

type LoginInfoResponse struct {
	Response struct {
		AppID  string `json:"appID"`
		InfoID string `json:"infoID"`
		Data   struct {
			// Gcid's JSON type isn't consistently documented (int elsewhere,
			// e.g. jloginResponseData.ClientCode) -- kept raw to avoid a
			// decode failure on either shape; use GcidString() to read it.
			Gcid          json.RawMessage `json:"gcid"`
			Gscid         string          `json:"gscid"`
			AllowedMarket []struct {
				MarketID int `json:"market_id"`
			} `json:"AllowedMarket"`
		} `json:"data"`
	} `json:"response"`
}

// GetLoginInfo returns account/market metadata for the logged-in GSCID
// (allowed markets, GCID confirmation).
func (c *Client) GetLoginInfo(ctx context.Context) (*LoginInfoResponse, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getLoginInfo", c.RestAPIBaseURL)

	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			SvcVersion: "1.0.0",
			SvcGroup:   "Login",
			SvcName:    "getLoginInfo",
			Data: loginInfoRequestData{
				Gscid: strings.ToUpper(c.Session.UserID),
			},
		},
	}

	var resBody LoginInfoResponse
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return &resBody, nil
}

// GcidString returns the response's gcid field as a string regardless of
// whether GreekSoft encoded it as a JSON number or a JSON string.
func (r *LoginInfoResponse) GcidString() string {
	raw := strings.Trim(string(r.Response.Data.Gcid), `"`)
	return raw
}
