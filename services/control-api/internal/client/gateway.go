package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GatewayClient provides methods to interact with the execution-gateway service.
type GatewayClient struct {
	BaseURL string
	Client  *http.Client
}

// NewGatewayClient creates a new client for the execution-gateway.
func NewGatewayClient(baseURL string, client *http.Client) *GatewayClient {
	return &GatewayClient{
		BaseURL: baseURL,
		Client:  client,
	}
}

// Post forwards a POST request to the execution-gateway.
func (c *GatewayClient) Post(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to gateway failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway response: %w", err)
	}

	return respBody, nil
}
