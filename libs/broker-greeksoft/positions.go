package greeksoft

import (
	"context"
	"fmt"
)

type positionsRequestData struct {
	Gscid string `json:"gscid"`
}

// StockPosition is one row of NPRequest's response, confirmed against
// GreekSoft's official docs example.
type StockPosition struct {
	NSEToken        string `json:"NSEToken"`
	BSEToken        string `json:"BSEToken"`
	Token           string `json:"token"`
	NetQty          string `json:"netQty"`
	DayNetAmt       string `json:"DayNetAmt"`
	PreNetQty       string `json:"preNetQty"`
	PAmt            string `json:"PAmt"`
	ProductType     string `json:"ProductType"`
	Symbol          string `json:"symbol"`
	ISIN            string `json:"isin"`
	Instrument      string `json:"instrument"`
	Description     string `json:"description"`
	LotQty          string `json:"lotQty"`
	SqOffToken      string `json:"sqoffToken"`
	Account         string `json:"account"`
	Multiplier      string `json:"multiplier"`
	PriceMultiplier string `json:"price_multiplier"`
}

type NetPositionResponse struct {
	Response struct {
		SvcName string `json:"svcName"`
		Data    struct {
			NoOfRecords  string          `json:"noofrecords"`
			IsLast       string          `json:"islast"`
			StockDetails []StockPosition `json:"stockDetails"`
		} `json:"data"`
	} `json:"response"`
}

// NPRequest returns net positions for the account.
func (c *Client) NPRequest(ctx context.Context) (*NetPositionResponse, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/NPRequest", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			FormFactor:    "M",
			Data:          positionsRequestData{Gscid: c.Session.UserID},
			SvcGroup:      "portfolio",
			SvcVersion:    "1.0.0",
			StreamingType: "NPRequest",
			RequestType:   "subscribe",
		},
	}

	var resBody NetPositionResponse
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return &resBody, nil
}

// NPDetailRequest returns detailed (per-leg) net positions. Response shape
// not confirmed against a live capture; decoded generically pending that.
func (c *Client) NPDetailRequest(ctx context.Context) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/NPDetailRequest", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			FormFactor:    "M",
			Data:          positionsRequestData{Gscid: c.Session.UserID},
			SvcGroup:      "portfolio",
			SvcVersion:    "1.0.0",
			StreamingType: "NPDetailRequest",
			RequestType:   "subscribe",
		},
	}

	var resBody map[string]interface{}
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}

// GetNetPositionMTM returns live MTM for open positions. Response shape
// not confirmed against a live capture; decoded generically pending that.
func (c *Client) GetNetPositionMTM(ctx context.Context, gscid string) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getNetPositionMTM?gscid=%s", c.RestAPIBaseURL, gscid)

	var resBody map[string]interface{}
	if _, _, err := c.getJSON(ctx, url, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}

// GetStrategyNameWiseNetPositionDetail returns positions grouped by
// strategy tag. Response shape not confirmed against a live capture;
// decoded generically pending that.
func (c *Client) GetStrategyNameWiseNetPositionDetail(ctx context.Context, gscid string) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getStrategyNameWiseNetPositionDetail?gscid=%s", c.RestAPIBaseURL, gscid)

	var resBody map[string]interface{}
	if _, _, err := c.getJSON(ctx, url, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}
