package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	registry "trading-platform/libs/broker-registry"
	xts "trading-platform/libs/broker-xts"
	gobroker "trading-platform/libs/go-broker"
)

// Mock XTS Login Response
type mockXTSLoginResponse struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Result      struct {
		Token  string `json:"token"`
		UserID string `json:"userID"`
	} `json:"result"`
}

func TestManager_Login_XTS_Success(t *testing.T) {
	// 1. Setup a mock HTTP server for XTS login
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// We only care about the interactive login endpoint for this test
		if r.URL.Path != "/apinteractive/user/session" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		resp := mockXTSLoginResponse{
			Type:        "success",
			Code:        "s-login-0001",
			Description: "Login successfully",
		}
		resp.Result.Token = "fake-xts-auth-token-12345"
		resp.Result.UserID = "TESTUSER"
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	// 2. Create and configure the broker registry
	reg := registry.New()
	reg.Register(xts.BrokerName, xts.NewFactory)

	// 3. Create the session manager
	mgr := New(reg)

	// 4. Define broker and account configs
	brokerCfg := gobroker.Config{
		BrokerName: xts.BrokerName,
		BaseURL:    mockServer.URL,
		AppKey:     "test-app-key",
		SecretKey:  "test-secret-key",
		Source:     "test-source",
	}
	accountCfg := gobroker.AccountConfig{
		ClientID: "TESTUSER",
	}

	// 5. Perform the login via the manager
	session, err := mgr.Login(context.Background(), xts.BrokerName, brokerCfg, accountCfg)

	// 6. Assert results
	if err != nil {
		t.Fatalf("Expected no error during login, but got: %v", err)
	}
	if session == nil {
		t.Fatal("Expected a session, but got nil")
	}
	if session.AuthToken != "fake-xts-auth-token-12345" {
		t.Errorf("Expected AuthToken to be 'fake-xts-auth-token-12345', but got %q", session.AuthToken)
	}
	if session.UserID != "TESTUSER" {
		t.Errorf("Expected UserID to be 'TESTUSER', but got %q", session.UserID)
	}
}
