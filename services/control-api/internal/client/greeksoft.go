package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"trading-platform/services/control-api/internal/config"
)

// GreeksoftClient provides methods to interact with the Greeksoft broker API.
type GreeksoftClient struct {
	BaseURL string
	Client  *http.Client
	cfg     *config.GreeksoftConfig
}

// NewGreeksoftClient creates a new client for the Greeksoft API.
func NewGreeksoftClient(baseURL string, client *http.Client, cfg *config.GreeksoftConfig) *GreeksoftClient {
	return &GreeksoftClient{
		BaseURL: baseURL,
		Client:  client,
		cfg:     cfg,
	}
}

// LoginRequest represents the payload for the Greeksoft login endpoint.
type LoginRequest struct {
	UserID      string `json:"user_id"`
	Password    string `json:"password"`
	YOB         string `json:"yob"`
	APIKey      string `json:"api_key"`
	APISecret   string `json:"api_secret"`
	TwoFAType   string `json:"two_fa_type"`
	TwoFACode   string `json:"two_fa_code"`
	RedirectURL string `json:"redirect_url"`
}

// LoginResponse represents the successful response from the Greeksoft login endpoint.
type LoginResponse struct {
	SessionToken string `json:"session_token"`
	// Add other fields from the login response as needed
}

// FlagValuesResponse represents the response from the getFlagValues endpoint.
type FlagValuesResponse struct {
	IsDheEnabled string `json:"isDheEnable"`
	// Add other flags as needed
}

// LoginInfoResponse represents the response from the getLoginInfo endpoint.
type LoginInfoResponse struct {
	GCID      string `json:"gcId"`
	SessionID string `json:"sId"`
	// Add other login info fields as needed
}

// Login performs authentication with the Greeksoft API and returns a session token.
func (c *GreeksoftClient) Login(ctx context.Context) (*LoginResponse, error) {
	loginReq := LoginRequest{
		UserID:    c.cfg.UserID,
		Password:  c.cfg.Password,
		YOB:       c.cfg.YOB,
		APIKey:    c.cfg.APIKey,
		APISecret: c.cfg.APISecret,
		// Static values for now, can be made dynamic later
		TwoFAType: "app",
		TwoFACode: "123456",
	}

	body, err := json.Marshal(loginReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal login request: %w", err)
	}

	url := c.BaseURL + "/j_spring_security_check" // Example login path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login request to Greeksoft failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return nil, fmt.Errorf("failed to decode login response: %w", err)
	}

	return &loginResp, nil
}

// GetFlagValues retrieves server configuration flags after login.
func (c *GreeksoftClient) GetFlagValues(ctx context.Context, sessionToken string) (*FlagValuesResponse, error) {
	var flagResp FlagValuesResponse
	err := c.get(ctx, "/getFlagValues", sessionToken, &flagResp)
	if err != nil {
		return nil, fmt.Errorf("GetFlagValues failed: %w", err)
	}
	return &flagResp, nil
}

// GetLoginInfo retrieves user-specific login details.
func (c *GreeksoftClient) GetLoginInfo(ctx context.Context, sessionToken string) (*LoginInfoResponse, error) {
	var loginInfoResp LoginInfoResponse
	err := c.get(ctx, "/getLoginInfo", sessionToken, &loginInfoResp)
	if err != nil {
		return nil, fmt.Errorf("GetLoginInfo failed: %w", err)
	}
	return &loginInfoResp, nil
}

// get is a helper for making authenticated GET requests to Greeksoft endpoints.
func (c *GreeksoftClient) get(ctx context.Context, path, sessionToken string, responseBody interface{}) error {
	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for path %s: %w", path, err)
	}

	// Greeksoft likely requires the session token in a cookie.
	req.AddCookie(&http.Cookie{Name: "jessionid", Value: sessionToken})

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request to %s returned status %d: %s", path, resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		// To help with debugging, read the raw body if JSON decoding fails
		body, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			return fmt.Errorf("failed to decode response from %s: %w. Body: %s", path, err, string(body))
		}
		return fmt.Errorf("failed to decode response from %s: %w", path, err)
	}

	return nil
}
