package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type LoginRequest struct {
	SecretKey string `json:"secretKey"`
	AppKey    string `json:"appKey"`
	Source    string `json:"source"`
}

type LoginResponse struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Result      struct {
		Token  string `json:"token"`
		UserID string `json:"userID"`
	} `json:"result"`
}

// InteractiveLogin performs the auth flow against the Symphony XTS Interactive API
func InteractiveLogin(baseURL, appKey, secretKey, source string) (string, string, error) {
	url := baseURL + "/interactive/user/session"

	reqBody, _ := json.Marshal(LoginRequest{
		SecretKey: secretKey,
		AppKey:    appKey,
		Source:    source,
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", "", fmt.Errorf("XTS HTTP POST failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var loginRes LoginResponse
	if err := json.Unmarshal(body, &loginRes); err != nil || loginRes.Type != "success" {
		return "", "", fmt.Errorf("XTS login failed: %s (Raw: %s)", loginRes.Description, string(body))
	}

	return loginRes.Result.Token, loginRes.Result.UserID, nil
}
