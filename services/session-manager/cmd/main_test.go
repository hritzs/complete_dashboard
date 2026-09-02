package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	broker "trading-platform/libs/broker-greeksoft"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestServer creates a mock HTTP server for testing broker interactions.
func setupTestServer() *httptest.Server {
	mux := http.NewServeMux()

	// Mock for successful session token
	mux.HandleFunc("/auth/greek/sessiontoken", func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]string
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody["username"] == "gooduser" {
			w.WriteHeader(http.StatusCreated) // As per Postman collection
			w.Write([]byte(`{
				"type": "success", "description": "Login success",
				"result": { "sessionToken": "test-session-token", "gcid": "TESTUSER", "isInvestorClient": true }
			}`))
		} else {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"type": "error", "description": "Invalid credentials"}`))
		}
	})

	return httptest.NewServer(mux)
}

func TestGetSessionToken_Success(t *testing.T) {
	server := setupTestServer()
	defer server.Close()

	// Override the broker URL to point to our mock server
	originalURL := broker.AuthBaseURL
	broker.AuthBaseURL = server.URL
	defer func() { broker.AuthBaseURL = originalURL }()

	log.SetOutput(io.Discard) // Suppress logs during test

	creds := broker.Credentials{Username: "gooduser", Password: "password"}
	manager := NewSessionManager(creds)

	err := manager.GetSessionToken()

	require.NoError(t, err)
	assert.Equal(t, "test-session-token", manager.broker.SessionToken)
	assert.Equal(t, "TESTUSER", manager.broker.GCID)
}

func TestGetSessionToken_Failure(t *testing.T) {
	server := setupTestServer()
	defer server.Close()

	originalURL := broker.AuthBaseURL
	broker.AuthBaseURL = server.URL
	defer func() { broker.AuthBaseURL = originalURL }()

	log.SetOutput(io.Discard)

	creds := broker.Credentials{
		Username: "baduser",
		Password: "password",
	}
	manager := NewSessionManager(creds)

	err := manager.GetSessionToken()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid credentials")
	assert.Empty(t, manager.broker.SessionToken)
}