package xts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	broker "trading-platform/libs/go-broker"
)

// loadEnv is a simple helper to load the .env file for testing
func loadEnv() {
	// Find the .env file in the project root (2 levels up)
	envPath := filepath.Join("..", "..", ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), `"`) // remove quotes
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

// TestLiveXTSLogin tests the actual HTTP connection to Symphony XTS.
func TestLiveXTSLogin(t *testing.T) {
	loadEnv()

	appKey := os.Getenv("XTS_API_KEY")
	secretKey := os.Getenv("XTS_API_SECRET")
	baseURL := os.Getenv("XTS_API_BASE_URL")
	source := os.Getenv("XTS_SOURCE")
	clientID := os.Getenv("XTS_CLIENT_ID")

	if appKey == "" || secretKey == "" || baseURL == "" {
		t.Skip("Skipping live XTS test: missing XTS_API_KEY, XTS_API_SECRET, or XTS_API_BASE_URL environment variables")
	}

	// Initialize the client directly
	client := NewClient(baseURL, appKey, secretKey, source)

	// Configure a dummy account config
	accCfg := &broker.AccountConfig{
		ClientID: clientID,
	}

	// 1. Test Login
	session, err := client.PerformFullLogin(context.Background(), accCfg)
	if err != nil {
		t.Fatalf("Live XTS Login failed: %v", err)
	}

	if session.AuthToken == "" {
		t.Errorf("Expected AuthToken from XTS, got empty string")
	}

	t.Logf("Successfully logged into XTS! Auth Token: %s", session.AuthToken)
}
