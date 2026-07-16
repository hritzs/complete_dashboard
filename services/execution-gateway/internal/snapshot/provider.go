package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"execution-gateway/internal/trading"
)

type Provider struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewProvider(baseURL string) *Provider {
	return &Provider{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type optionChainAPIResponse struct {
	Success bool                         `json:"success"`
	Data    *trading.OptionChainSnapshot `json:"data"`
	Error   string                       `json:"error"`
}

func (p *Provider) GetOptionChain(ctx context.Context, symbol string, expiry string) (*trading.OptionChainSnapshot, error) {
	u := fmt.Sprintf("%s/api/option-chain/%s", p.BaseURL, url.PathEscape(symbol))
	if expiry != "" {
		u = fmt.Sprintf("%s?expiry=%s", u, url.QueryEscape(expiry))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out optionChainAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	if !out.Success {
		return nil, fmt.Errorf(out.Error)
	}
	if out.Data == nil {
		return nil, fmt.Errorf("snapshot data missing")
	}

	return out.Data, nil
}
