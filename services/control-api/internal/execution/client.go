package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Client struct {
	BaseURL    string
	HTTPClient http.Client
}

func NewClient(baseURL string, httpClient http.Client) Client {
	return Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: httpClient,
	}
}

func (c Client) DeployStraddle(ctx context.Context, req DeployStraddleRequest) (DeployStraddleResponse, error) {
	var out DeployStraddleResponse

	body, err := json.Marshal(req)
	if err != nil {
		return out, fmt.Errorf("marshal deploy request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/trade/straddle", bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("create execution-gateway request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return out, fmt.Errorf("call execution-gateway: %w", err)
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("decode execution-gateway response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if out.Error == "" {
			out.Error = fmt.Sprintf("execution-gateway returned HTTP %d", res.StatusCode)
		}
		return out, fmt.Errorf(out.Error)
	}

	return out, nil
}
