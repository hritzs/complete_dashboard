package greeksoft

import (
	"context"
	"fmt"
	"strings"
)

type marketStatusRequestData struct {
	Gscid string `json:"gscid"`
}

type MarketStatusResponse struct {
	Response struct {
		ErrorCode int `json:"ErrorCode"`
		Data      struct {
			MarketStatus []struct {
				MarketID int `json:"market_id"`
				Status   int `json:"status"`
				Session  int `json:"session"`
			} `json:"MarketStatus"`
		} `json:"data"`
	} `json:"response"`
}

// GetMarketStatus returns the current open/closed status of each exchange
// segment.
func (c *Client) GetMarketStatus(ctx context.Context) (*MarketStatusResponse, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getMarketStatus", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			SvcName:  "getMarketStatus",
			SvcGroup: "Markets",
			Data:     marketStatusRequestData{Gscid: strings.ToUpper(c.Session.UserID)},
		},
	}

	var resBody MarketStatusResponse
	if _, raw, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	} else if resBody.Response.ErrorCode != 0 {
		return nil, fmt.Errorf("greeksoft getMarketStatus ErrorCode=%d raw=%s", resBody.Response.ErrorCode, string(raw))
	}

	return &resBody, nil
}

// GetIndianIndicesDataV2 returns snapshot data for the standard Indian
// indices. Response shape is not documented in the Postman collection or
// the official docs reviewed so far; decoded generically pending a real
// capture.
func (c *Client) GetIndianIndicesDataV2(ctx context.Context) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getIndianIndicesData_V2", c.RestAPIBaseURL)

	var resBody map[string]interface{}
	if _, _, err := c.getJSON(ctx, url, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}

type quoteRequestData struct {
	Token     string `json:"token"`
	AssetType string `json:"assetType"`
	Gscid     string `json:"gscid"`
	Gcid      string `json:"gcid,omitempty"`
}

// QuoteResponse is streaming_type-less REST quote data, confirmed against
// GreekSoft's official docs example for getQuoteForSingleSymbol_V2.
type QuoteResponse struct {
	Response struct {
		Data struct {
			AssetToken    int64   `json:"AssetToken"`
			Ask           float64 `json:"ask"`
			AssetLTP      float64 `json:"assetLtp"`
			ATP           float64 `json:"atp"`
			AuthorizedQty int64   `json:"authorizedQty"`
			Bid           float64 `json:"bid"`
			Change        float64 `json:"change"`
			Close         float64 `json:"close"`
			Description   string  `json:"description"`
			ExpiryDate    int64   `json:"expiryDate"`
			FreezeQty     int64   `json:"freezQty"`
			High          float64 `json:"high"`
			HighRange     float64 `json:"highRange"`
			Instrument    string  `json:"instrument"`
			ISINNumber    string  `json:"isinumber"`
			Last          float64 `json:"last"`
			Level2        []struct {
				Ask struct {
					No    int     `json:"no"`
					Price float64 `json:"price"`
					Qty   int64   `json:"qty"`
				} `json:"ask"`
				Bid struct {
					No    int     `json:"no"`
					Price float64 `json:"price"`
					Qty   int64   `json:"qty"`
				} `json:"bid"`
			} `json:"level2"`
			Lot          int64   `json:"lot"`
			Low          float64 `json:"low"`
			LowRange     float64 `json:"lowRange"`
			LTT          int64   `json:"ltt"`
			OI           int64   `json:"oi"`
			OIPercentChg float64 `json:"oi_pChange"`
			Open         float64 `json:"open"`
			OptionType   string  `json:"optiontype"`
			PercentChg   float64 `json:"p_change"`
			Reason       string  `json:"reason"`
			SqOffQty     int64   `json:"sqOffQty"`
			StrikePrice  float64 `json:"strikeprice"`
			Symbol       string  `json:"symbol"`
			TickSize     float64 `json:"tickSize"`
			Token        int64   `json:"token"`
			TotalBuyQty  int64   `json:"tot_buyQty"`
			TotalSellQty int64   `json:"tot_sellQty"`
			TotalVolume  int64   `json:"tot_vol"`
			YearHigh     float64 `json:"yhigh"`
			YearLow      float64 `json:"ylow"`
		} `json:"data"`
	} `json:"response"`
}

// GetQuoteForSingleSymbolV2 returns a REST snapshot quote for one
// instrument token. For live streaming quotes prefer the Apollo websocket
// (NewApolloMarketDataClient) -- this REST call is comparatively slow and
// meant for on-demand lookups, not a polling loop.
func (c *Client) GetQuoteForSingleSymbolV2(ctx context.Context, token string, assetType string, gcid string) (*QuoteResponse, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getQuoteForSingleSymbol_V2", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			SvcName:  "getQuoteForSingleSymbol_V2",
			SvcGroup: "Markets",
			Data: quoteRequestData{
				Token:     token,
				AssetType: assetType,
				Gscid:     strings.ToUpper(c.Session.UserID),
				Gcid:      gcid,
			},
		},
	}

	var resBody QuoteResponse
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return &resBody, nil
}

// ExchangeToken identifies one instrument for GetTokenOnlyMbpData.
type ExchangeToken struct {
	Exchange string `json:"exchange"`
	Token    string `json:"token"`
}

type mbpDataRequestData struct {
	Mode           string          `json:"mode"` // "FULL" for tick-by-tick depth+LTP, per the Postman collection
	ExchangeTokens []ExchangeToken `json:"exchangeTokens"`
}

// GetTokenOnlyMbpData is GreekSoft's REST tick-by-tick market-by-price
// pull (mode:"FULL"). Prefer the Apollo websocket for continuous
// streaming; this is a request/response pull, not a subscription, so
// polling it in a tight loop defeats the purpose of "tick-by-tick" and
// will be much slower than Apollo.
func (c *Client) GetTokenOnlyMbpData(ctx context.Context, tokens []ExchangeToken) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/getToken_OnlyMbpData", c.RestAPIBaseURL)
	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			Data: mbpDataRequestData{Mode: "FULL", ExchangeTokens: tokens},
		},
	}

	var resBody map[string]interface{}
	// GreekSoft's own Postman collection documents this as a GET request
	// with a JSON body (disableBodyPruning set) -- unusual, but doJSON
	// supports an arbitrary method+body combination directly.
	if _, _, err := c.doJSON(ctx, "GET", url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}

type historicalRequestData struct {
	Gscid    string `json:"gscid"`
	Token    int64  `json:"token"`
	Interval int    `json:"interval"`
	Date     string `json:"date"`
	NoOfDays int    `json:"noofdays"`
}

// historicalRequestPayload matches get_ohlc's distinct envelope shape
// (FormFactor + svcName + requestType, per the Postman collection) --
// intentionally not reusing greekRequestPayload, whose field names/casing
// don't match this endpoint.
type historicalRequestPayload struct {
	FormFactor  string                `json:"FormFactor"`
	Data        historicalRequestData `json:"data"`
	SvcGroup    string                `json:"svcGroup"`
	SvcVersion  string                `json:"svcVersion"`
	SvcName     string                `json:"svcName"`
	RequestType string                `json:"requestType"`
}

type historicalEnvelope struct {
	Request historicalRequestPayload `json:"request"`
}

// GetOHLC returns historical OHLC candles for a token.
func (c *Client) GetOHLC(ctx context.Context, token int64, interval int, date string, noOfDays int) (map[string]interface{}, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	url := fmt.Sprintf("%s/get_ohlc", c.RestAPIBaseURL)
	reqBody := historicalEnvelope{
		Request: historicalRequestPayload{
			FormFactor: "M",
			Data: historicalRequestData{
				Gscid:    strings.ToUpper(c.Session.UserID),
				Token:    token,
				Interval: interval,
				Date:     date,
				NoOfDays: noOfDays,
			},
			SvcGroup:    "portfolio",
			SvcVersion:  "1.0.0",
			SvcName:     "jhistorical_New",
			RequestType: "U",
		},
	}

	var resBody map[string]interface{}
	if _, _, err := c.postJSON(ctx, url, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody, nil
}
