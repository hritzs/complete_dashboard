package xts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL   string
	Token     string
	AppKey    string
	SecretKey string
	Source    string
	HTTP      *http.Client
}

func NewClient() *Client {
	baseURL := strings.TrimRight(os.Getenv("XTS_API_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "https://developers.symphonyfintech.in"
	}

	return &Client{
		BaseURL:   baseURL,
		AppKey:    os.Getenv("XTS_MD_API_KEY"),
		SecretKey: os.Getenv("XTS_MD_API_SECRET"),
		Source:    getenvDefault("XTS_SOURCE", "WEBAPI"),
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func getenvDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func (c *Client) Login(ctx context.Context) error {
	if c.AppKey == "" || c.SecretKey == "" {
		return fmt.Errorf("XTS market data credentials missing (XTS_MD_API_KEY / XTS_MD_API_SECRET)")
	}

	body := map[string]string{
		"appKey":    c.AppKey,
		"secretKey": c.SecretKey,
		"source":    c.Source,
	}

	b, _ := json.Marshal(body)

	url := c.BaseURL + "/apimarketdata/auth/login"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("failed to decode marketdata login response: %w", err)
	}

	log.Println("🔐 XTS MARKET LOGIN RESPONSE:", out)

	resultRaw, ok := out["result"]
	if !ok || resultRaw == nil {
		return fmt.Errorf("invalid login response: %v", out)
	}

	result, ok := resultRaw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid login result type: %v", resultRaw)
	}

	tokenRaw, ok := result["token"]
	if !ok || tokenRaw == nil {
		return fmt.Errorf("token missing in login response: %v", result)
	}

	token, ok := tokenRaw.(string)
	if !ok || token == "" {
		return fmt.Errorf("invalid token in login response: %v", tokenRaw)
	}

	c.Token = token
	log.Println("✅ XTS MARKET LOGIN SUCCESS")
	return nil
}

func (c *Client) GetMaster(ctx context.Context) (map[string]interface{}, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("marketdata token missing; call Login first")
	}

	body := map[string]interface{}{
		"exchangeSegmentList": []string{"NSEFO"},
	}

	log.Println("🚀 MASTER REQUEST:", body)

	b, _ := json.Marshal(body)

	url := c.BaseURL + "/apimarketdata/instruments/master"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(b))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("failed to decode master response: %w", err)
	}

	log.Println("📦 MASTER RESPONSE:", out)
	return out, nil
}
