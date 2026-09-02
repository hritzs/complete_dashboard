package greeksoft

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"

	broker "trading-platform/libs/go-broker"
)

func hashPasswordMD5(password string) string {
	sum := md5.Sum([]byte(password))
	return hex.EncodeToString(sum[:])
}

func hashPasswordMD5Upper(password string) string {
	return hashPasswordMD5(strings.ToUpper(password))
}

func (c *Client) PerformFullLogin(ctx context.Context, accCfg *broker.AccountConfig) (*broker.SessionDetails, error) {
	if accCfg == nil {
		return nil, fmt.Errorf("account config is nil")
	}

	slog.Info("starting Greeksoft login sequence", "account", accCfg.Name)

	sessionToken, err := c.getSessionToken(ctx, accCfg.APIKey, accCfg.APISecret, accCfg.BrokerID)
	if err != nil {
		return nil, fmt.Errorf("session token failed: %w", err)
	}

	jloginPassword := strings.TrimSpace(os.Getenv("GREEK_JLOGIN_PASSWORD"))
	if jloginPassword == "" {
		jloginPassword = accCfg.APISecret
	}

	jloginRes, err := c.jloginNew(ctx, sessionToken, accCfg.ClientID, jloginPassword, accCfg.PanDob)
	if err != nil {
		return nil, fmt.Errorf("jloginNew failed: %w", err)
	}

	gcid := jloginRes.Response.Data.ClientCode
	if gcid == 0 {
		return nil, fmt.Errorf("jloginNew response missing ClientCode/GCID")
	}

	flagValues, err := c.getFlagValues(ctx, sessionToken, accCfg.ClientID)
	if err != nil {
		slog.Warn("getFlagValues failed; falling back to websocket fields from jloginNew", "error", err)
	}

	brokerSpecific := map[string]interface{}{
		"gcid":        gcid,
		"session_id":  jloginRes.Response.SessionID,
		"iris_ip":     jloginRes.Response.Data.IrisIP,
		"iris_port":   jloginRes.Response.Data.IrisPort,
		"apollo_ip":   jloginRes.Response.Data.ApolloIP,
		"apollo_port": jloginRes.Response.Data.ApolloPort,
	}

	if flagValues != nil {
		if flagValues.Response.SessionID != "" {
			brokerSpecific["session_id"] = flagValues.Response.SessionID
		}
		if flagValues.Response.Data.IrisIP != "" {
			brokerSpecific["iris_ip"] = flagValues.Response.Data.IrisIP
		}
		if flagValues.Response.Data.IrisPort != 0 {
			brokerSpecific["iris_port"] = flagValues.Response.Data.IrisPort
		}
		if flagValues.Response.Data.ApolloIP != "" {
			brokerSpecific["apollo_ip"] = flagValues.Response.Data.ApolloIP
		}
		if flagValues.Response.Data.ApolloPort != 0 {
			brokerSpecific["apollo_port"] = flagValues.Response.Data.ApolloPort
		}
	}

	if brokerSpecific["iris_ip"] != nil && brokerSpecific["iris_port"] != nil {
		brokerSpecific["iris_url"] = fmt.Sprintf("%v:%v", brokerSpecific["iris_ip"], brokerSpecific["iris_port"])
	}

	if brokerSpecific["apollo_ip"] != nil && brokerSpecific["apollo_port"] != nil {
		brokerSpecific["apollo_url"] = fmt.Sprintf("%v:%v", brokerSpecific["apollo_ip"], brokerSpecific["apollo_port"])
	}

	session := &broker.SessionDetails{
		UserID:         accCfg.ClientID,
		AuthToken:      sessionToken,
		IsLoggedIn:     true,
		BrokerSpecific: brokerSpecific,
	}

	c.Session = session

	slog.Info(
		"Greeksoft login successful",
		"account", accCfg.Name,
		"user_id", session.UserID,
		"gcid", gcid,
	)

	return session, nil
}

func (c *Client) getSessionToken(ctx context.Context, username string, password string, brokerID int) (string, error) {
	url := fmt.Sprintf("%s/auth/greek/sessiontoken", c.AuthBaseURL)

	reqBody := sessionTokenRequest{
		Username: username,
		Password: password,
		BrokerID: brokerID,
		ValidFor: "30d",
	}

	var resBody sessionTokenResponse

	_, raw, err := c.postJSON(ctx, url, "", reqBody, &resBody)
	if err != nil {
		return "", err
	}

	if resBody.SessionToken == "" {
		return "", fmt.Errorf("sessionToken missing in response: %s", string(raw))
	}

	return resBody.SessionToken, nil
}

func (c *Client) jloginNew(
	ctx context.Context,
	sessionToken string,
	clientID string,
	password string,
	panDob string,
) (*jloginResponse, error) {
	url := fmt.Sprintf("%s/jloginNew", c.RestAPIBaseURL)
	slog.Info(
    "jlogin debug",
    "password", password,
    "hash", hashPasswordMD5(password),
)
	hashedPassword := hashPasswordMD5(password)

	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			SvcName:  "jloginNew",
			SvcGroup: "Login",
			Data: jloginRequestData{
				PanDob:         panDob,
				DeviceID:       "4f89423bab1280c9",
				Gscid:          strings.ToUpper(clientID),
				DeviceDetails:  "",
				DeviceType:     "0",
				Password:       hashedPassword,
				TransPass:      "",
				UserType:       "Customer",
				BrokerID:       "1",
				PassType:       "0",
				VersionNo:      "1.0.1.10",
				EncryptionType: "1",
			},
		},
	}

	var resBody jloginResponse

	_, raw, err := c.postJSON(ctx, url, sessionToken, reqBody, &resBody)
	if err != nil {
		return nil, err
	}

	if resBody.Response.Data.ErrorCode != 0 {
		return nil, fmt.Errorf("jloginNew ErrorCode=%d message=%s raw=%s",
			resBody.Response.Data.ErrorCode,
			resBody.Response.Data.Message,
			string(raw),
		)
	}

	if resBody.Response.Data.ClientCode == 0 {
		return nil, fmt.Errorf("jloginNew missing ClientCode/GCID raw=%s", string(raw))
	}

	return &resBody, nil
}

func (c *Client) getFlagValues(
	ctx context.Context,
	sessionToken string,
	clientID string,
) (*flagValuesResponse, error) {
	url := fmt.Sprintf("%s/getFlagValues", c.RestAPIBaseURL)

	reqBody := greekEnvelope{
		Request: greekRequestPayload{
			SvcVersion: "1.0.0",
			SvcGroup:   "",
			SvcName:    "getFlagValues",
			Gscid:      strings.ToUpper(clientID),
			Data:       map[string]interface{}{},
		},
	}

	var resBody flagValuesResponse

	_, raw, err := c.postJSON(ctx, url, sessionToken, reqBody, &resBody)
	if err != nil {
		return nil, err
	}

	if resBody.Response.Data.IrisIP == "" && resBody.Response.Data.ApolloIP == "" {
		return nil, fmt.Errorf("getFlagValues missing websocket IP fields raw=%s", string(raw))
	}

	return &resBody, nil
}
