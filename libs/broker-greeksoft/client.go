package greeksoft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	broker "trading-platform/libs/go-broker"
)

type Client struct {
	HTTPClient     *http.Client
	AuthBaseURL    string
	RestAPIBaseURL string
	Session        *broker.SessionDetails
}

func NewClient(authURL string, restURL string) *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		AuthBaseURL:    strings.TrimRight(authURL, "/"),
		RestAPIBaseURL: strings.TrimRight(restURL, "/"),
	}
}

func (c *Client) doJSON(
	ctx context.Context,
	method string,
	url string,
	sessionToken string,
	reqBody interface{},
	resBody interface{},
) (*http.Response, []byte, error) {
	var bodyReader io.Reader

	if reqBody != nil {
		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if sessionToken != "" {
		req.Header.Set("Authorization", sessionToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	rawBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp, nil, fmt.Errorf("failed to read response body: %w", readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, rawBody, fmt.Errorf("api error: status=%d body=%s", resp.StatusCode, string(rawBody))
	}

	if resBody != nil && len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, resBody); err != nil {
			return resp, rawBody, fmt.Errorf("failed to decode json response: %w body=%s", err, string(rawBody))
		}
	}

	return resp, rawBody, nil
}

func (c *Client) postJSON(
	ctx context.Context,
	url string,
	sessionToken string,
	reqBody interface{},
	resBody interface{},
) (*http.Response, []byte, error) {
	return c.doJSON(ctx, http.MethodPost, url, sessionToken, reqBody, resBody)
}

func (c *Client) getRaw(
	ctx context.Context,
	url string,
	sessionToken string,
) (*http.Response, []byte, error) {
	return c.doJSON(ctx, http.MethodGet, url, sessionToken, nil, nil)
}
