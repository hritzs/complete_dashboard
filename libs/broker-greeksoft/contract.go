package greeksoft

import (
	"context"
	"fmt"
	"net/url"
)

// ContractDetail is a single scrip/contract record as returned by both
// GetAllContract and GetFullScripDetails -- both endpoints return the same
// item shape (confirmed against GreekSoft's REST API docs example for
// getFullScripDetailsBySymbol_Mobile).
type ContractDetail struct {
	Name           string  `json:"Name"`
	OptionType     string  `json:"OptionType"`
	ScriptName     string  `json:"ScriptName"`
	Series         string  `json:"Series"`
	UniqueID       int64   `json:"UniqueId"`
	AssetType      string  `json:"assetType"`
	Description    string  `json:"description"`
	Exchange       string  `json:"exchange"`
	ExpiryDate     int64   `json:"expiryDate"` // unix seconds
	InstrumentName string  `json:"instrumentName"`
	LotQty         int64   `json:"lotQty"`
	Multiplier     float64 `json:"multiplier"`
	StrikePrice    float64 `json:"strickPrice"` // sic -- matches GreekSoft's own misspelling
	TickSize       float64 `json:"tickSize"`
	Token          int64   `json:"token"`
	TradeSymbol    string  `json:"tradeSymbol"`
}

type contractListResponse struct {
	ErrorCode int              `json:"ErrorCode"`
	Data      []ContractDetail `json:"data"`
	Message   string           `json:"message"`
	Success   string           `json:"success"`
}

// GetAllContract returns the full contract/scrip master dump for the
// account. This is a large response; callers should cache it rather than
// polling frequently.
func (c *Client) GetAllContract(ctx context.Context) ([]ContractDetail, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	reqURL := fmt.Sprintf("%s/getAllContract", c.RestAPIBaseURL)

	var resBody contractListResponse
	if _, raw, err := c.getJSON(ctx, reqURL, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	} else if resBody.ErrorCode != 0 {
		return nil, fmt.Errorf("greeksoft getAllContract ErrorCode=%d raw=%s", resBody.ErrorCode, string(raw))
	}

	return resBody.Data, nil
}

// GetFullScripDetails looks up contract details for a single symbol/code.
// assetType and instrumentType follow GreekSoft's own vocabulary, e.g.
// "future"/"option"/"equity".
func (c *Client) GetFullScripDetails(ctx context.Context, exchange, assetType, code, instrumentType string) ([]ContractDetail, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	query := url.Values{}
	query.Set("exchange", exchange)
	query.Set("assetType", assetType)
	query.Set("code", code)
	query.Set("type", instrumentType)

	reqURL := fmt.Sprintf("%s/getFullScripDetailsBySymbol_Mobile?%s", c.RestAPIBaseURL, query.Encode())

	var resBody contractListResponse
	if _, raw, err := c.getJSON(ctx, reqURL, c.Session.AuthToken, &resBody); err != nil {
		return nil, err
	} else if resBody.ErrorCode != 0 {
		return nil, fmt.Errorf("greeksoft getFullScripDetailsBySymbol_Mobile ErrorCode=%d raw=%s", resBody.ErrorCode, string(raw))
	}

	return resBody.Data, nil
}

type allowedProductRequestData struct{}

type AllowedProductResponse struct {
	Response struct {
		ErrorCode int `json:"ErrorCode"`
		Data      struct {
			AllowedProduct []struct {
				ProductToken int    `json:"iProductToken"`
				ProductName  string `json:"cProductName"`
			} `json:"AllowedProduct"`
		} `json:"data"`
	} `json:"response"`
}

// GetAllowedProduct returns the product types (MIS/NRML/CNC/...) enabled
// for this account.
func (c *Client) GetAllowedProduct(ctx context.Context) (*AllowedProductResponse, error) {
	if c.Session == nil || c.Session.AuthToken == "" {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	reqURL := fmt.Sprintf("%s/getAllowedProduct", c.RestAPIBaseURL)

	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			SvcName:  "getAllowedProduct",
			SvcGroup: "Login",
			Data:     allowedProductRequestData{},
		},
	}

	var resBody AllowedProductResponse
	if _, raw, err := c.postJSON(ctx, reqURL, c.Session.AuthToken, reqBody, &resBody); err != nil {
		return nil, err
	} else if resBody.Response.ErrorCode != 0 {
		return nil, fmt.Errorf("greeksoft getAllowedProduct ErrorCode=%d raw=%s", resBody.Response.ErrorCode, string(raw))
	}

	return &resBody, nil
}
