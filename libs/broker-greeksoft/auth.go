package auth
package greeksoft

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client manages communication with the Greeksoft APIs.
type Client struct {
	HTTPClient     *http.Client
	AuthBaseURL    string
	RestAPIBaseURL string
// hashPassword mimics the CryptoJS.MD5(userPassword).toString() from the UI
func hashPassword(password string) string {
	hash := md5.Sum([]byte(password))
	return hex.EncodeToString(hash[:])
}

// NewClient creates a new API client.
func NewClient(authURL, restURL string) *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		AuthBaseURL:    authURL,
		RestAPIBaseURL: restURL,
	}
}
// PerformJLogin executes the jloginNew API call to obtain the GCID and WS Endpoints
func PerformJLogin(baseURL, username, password, panDob, brokerID string) (*SessionDetails, error) {
	client := &http.Client{Timeout: 10 * time.Second}

// LoginResult holds the successful authentication data from the broker.
type LoginResult struct {
	SessionToken string // The main session token from initial login
	UserID       string // The client_id/gscid
	GCID         string // The ClientCode from jloginNew, required for orders
	SessionID    string // The sessionId from getFlagValues, for WebSockets
	IrisURL      string // Full Iris WebSocket URL (e.g., "123.45.67.89:12345")
	ApolloURL    string // Full Apollo WebSocket URL
}
	hashedPass := hashPassword(password)

// AccountConfig holds the credentials for a single account.
type AccountConfig struct {
	Name      string
	APIKey    string
	APISecret string
	ClientID  string
	BrokerID  int
	PanDob    string
}

// PerformFullLogin executes the complete, multi-step Greeksoft login sequence.
func (c *Client) PerformFullLogin(ctx context.Context, accCfg *AccountConfig) (*LoginResult, error) {
	slog.Info("Starting Greeksoft login sequence", "account", accCfg.Name)

	// Step 1: Get Session Token
	slog.Info("[Step 1/3] Authenticating to get session token...")
	sessionToken, err := c.getSessionToken(ctx, accCfg.APIKey, accCfg.APISecret, accCfg.BrokerID)
	if err != nil {
		return nil, fmt.Errorf("step 1 (session token) failed: %w", err)
	}
	slog.Info("[Step 1/3] Session token obtained.")

	// Step 2: Perform jloginNew (to get the critical GCID for trading)
	slog.Info("[Step 2/3] Performing jloginNew...")
	jloginRes, err := c.jloginNew(ctx, sessionToken, accCfg.ClientID, accCfg.APISecret, accCfg.PanDob)
	if err != nil {
		return nil, fmt.Errorf("step 2 (jloginNew) failed: %w", err)
	}
	slog.Info("[Step 2/3] jloginNew complete.", "gcid", jloginRes.Response.Data.ClientCode)

	// Step 3: Get Flag Values (for WebSocket details and sessionID)
	slog.Info("[Step 3/3] Fetching flag values...")
	flagValues, err := c.getFlagValues(ctx, sessionToken, accCfg.ClientID)
	if err != nil {
		return nil, fmt.Errorf("step 3 (getFlagValues) failed: %w", err)
	}
	slog.Info("[Step 3/3] Flag values obtained.")

	// Assemble the final result
	result := &LoginResult{
		SessionToken: sessionToken,
		UserID:       accCfg.ClientID,
		GCID:         jloginRes.Response.Data.ClientCode,
		SessionID:    flagValues.Response.SessionID,
		IrisURL:      fmt.Sprintf("%s:%s", flagValues.Response.Data.IrisIP, flagValues.Response.Data.IrisPort),
		ApolloURL:    fmt.Sprintf("%s:%s", flagValues.Response.Data.ApolloIP, flagValues.Response.Data.ApolloPort),
	}
	slog.Info("Greeksoft login sequence successful", "account", accCfg.Name, "user_id", result.UserID, "gcid", result.GCID)
	return result, nil
}

// getSessionToken performs the initial authentication.
func (c *Client) getSessionToken(ctx context.Context, username, password string, brokerID int) (string, error) {
	url := fmt.Sprintf("%s/auth/greek/sessiontoken", c.AuthBaseURL)
	reqBody := &sessionTokenRequest{
		Username: username,
		Password: password,
		BrokerID: brokerID,
		ValidFor: "30d",
	}
	var resBody sessionTokenResponse

	_, err := c.post(ctx, url, "", reqBody, &resBody)
	if err != nil {
		return "", err
	}
	if resBody.SessionToken == "" {
		return "", fmt.Errorf("API response did not contain a session token (message: %s)", resBody.Message)
	}
	return resBody.SessionToken, nil
}

// jloginNew performs the final login step to get the trading GCID.
func (c *Client) jloginNew(ctx context.Context, sessionToken, clientID, apiSecret, panDob string) (*jloginResponse, error) {
	url := fmt.Sprintf("%s/jloginNew", c.RestAPIBaseURL)

	hasher := md5.New()
	hasher.Write([]byte(apiSecret))
	hashedPassword := hex.EncodeToString(hasher.Sum(nil))

	reqBody := &genericRequest{
		Request: genericRequestPayload{
	payload := JLoginRequest{
		Request: JLoginRequestData{
			SvcName:  "jloginNew",
			SvcGroup: "Login",
			Gscid:    clientID,
			Data: jloginRequestData{
			Data: JLoginData{
				PanDob:         panDob,
				DeviceID:       "4f89423bab1280c9", // Static value from Postman/JS
				Gscid:          clientID,
				DeviceID:       "4f89423bab1280c9", // Static device ID from legacy spec
				Gscid:          strings.ToUpper(username),
				DeviceDetails:  "",
				DeviceType:     "0",
				Pass:           hashedPass,
				TransPass:      "",
				UserType:       "Customer",
				BrokerID:       "1", // Static value from Postman/JS
				Password:       hashedPassword,
				BrokerID:       brokerID,
				PassType:       "0",
				VersionNo:      "1.0.1.10",
				EncryptionType: "1",
			},
		},
	}
	var resBody jloginResponse

	_, err := c.post(ctx, url, sessionToken, reqBody, &resBody)
	jsonReq, err := json.Marshal(payload)
	if err != nil {
		return nil, err
		return nil, fmt.Errorf("failed to marshal jlogin request: %w", err)
	}
	if resBody.Response.Data.ErrorCode != 0 || resBody.Response.Data.ClientCode == "" {
		return nil, fmt.Errorf("jloginNew failed with ErrorCode %d: %s", resBody.Response.Data.ErrorCode, resBody.Response.Data.Message)
	}
	return &resBody, nil
}

// getFlagValues fetches WebSocket connection details.
func (c *Client) getFlagValues(ctx context.Context, sessionToken, clientID string) (*flagValuesResponse, error) {
	url := fmt.Sprintf("%s/getFlagValues", c.RestAPIBaseURL)
	reqBody := &genericRequest{Request: genericRequestPayload{SvcName: "getFlagValues", Gscid: clientID, Data: struct{}{}}}
	var resBody flagValuesResponse
	_, err := c.post(ctx, url, sessionToken, reqBody, &resBody)
	url := fmt.Sprintf("%s/interactive/orders/jloginNew", baseURL) // Adjust path as per proxy mapping
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonReq))
	if err != nil {
		return nil, err
	}
	if resBody.Response.Data.IrisIP == "" {
		return nil, fmt.Errorf("API response did not contain Iris IP")
	}
	return &resBody, nil
}

// post performs a POST request and decodes the JSON response.
func (c *Client) post(ctx context.Context, url string, sessionToken string, reqBody, resBody interface{}) (*http.Response, error) {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if sessionToken != "" {
		req.Header.Set("Authorization", sessionToken)
	}

	resp, err := c.HTTPClient.Do(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
		return nil, fmt.Errorf("jloginNew request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return resp, fmt.Errorf("api error: status %d, body: %s", resp.StatusCode, string(body))
	bodyBytes, _ := io.ReadAll(resp.Body)

	var loginResp JLoginResponse
	if err := json.Unmarshal(bodyBytes, &loginResp); err != nil {
		return nil, fmt.Errorf("failed to parse jlogin response: %w. Body: %s", err, string(bodyBytes))
	}

	return resp, json.NewDecoder(resp.Body).Decode(resBody)
	data := loginResp.Response.Data
	return &SessionDetails{
		GCID:               data.ClientCode,
		WebsocketSessionID: loginResp.Response.SessionID,
		IrisEndpoint:       fmt.Sprintf("%s:%d", data.IrisIP, data.IrisPort),
		ApolloEndpoint:     fmt.Sprintf("%s:%d", data.ApolloIP, data.ApolloPort),
	}, nil
}
