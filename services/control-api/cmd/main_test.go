package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"trading-platform/services/control-api/internal/config"
	"trading-platform/services/control-api/internal/handlers"
	"trading-platform/services/control-api/internal/router"
	"trading-platform/services/control-api/internal/session"
)

// mockUpstreamAPI simulates the backend Greeksoft API.
func mockUpstreamAPI() *httptest.Server {
	mux := http.NewServeMux()

	// Mock for /auth/greek/sessiontoken
	mux.HandleFunc("/auth/greek/sessiontoken", func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody.Username == "testuser" && reqBody.Password == "password" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":           123,
				"sessionToken": "fake-upstream-token",
			})
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"message": "Invalid credentials"})
		}
	})

	// Mock for a generic proxied endpoint
	mux.HandleFunc("/some/endpoint", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "fake-upstream-token" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data": "proxied content"}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "missing token"}`))
		}
	})

	return httptest.NewServer(mux)
}

// setupTestApp initializes the application with a mock upstream server.
func setupTestApp(upstreamURL string) http.Handler {
	cfg := config.Config{
		Host:                "localhost",
		Port:                "8080",
		StaticFilePath:      "../../ui", // Adjust path for test location
		GreekAuthApiBaseUrl: upstreamURL,
		GreekRestApiBaseUrl: upstreamURL,
		RequestTimeout:      5 * time.Second,
	}
	client := &http.Client{Timeout: cfg.RequestTimeout}
	sessionStore := session.NewStore()
	h := handlers.New(cfg, client, sessionStore)
	return router.New(h)
}

func findCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	for _, c := range cookies {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("cookie '%s' not found", name)
}

// TestServeIndex checks if the root path serves a file.
func TestServeIndex(t *testing.T) {
	// Create a dummy ui/login.html for this test to pass.
	// The path is relative to the service directory.
	os.MkdirAll("../../ui", 0755)
	os.WriteFile("../../ui/login.html", []byte("<html></html>"), 0644)
	defer os.RemoveAll("../../ui")

	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	server := setupTestApp("")
	server.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}
}

func TestLoginLogoutAndProxy(t *testing.T) {
	upstream := mockUpstreamAPI()
	defer upstream.Close()

	server := setupTestApp(upstream.URL)

	// 1. Test Successful Login
	loginBody := `{"username": "testuser", "password": "password", "brokerId": 123}`
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader(loginBody))
	loginRR := httptest.NewRecorder()
	server.ServeHTTP(loginRR, loginReq)

	if status := loginRR.Code; status != http.StatusOK {
		t.Errorf("login handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	cookies := loginRR.Result().Cookies()
	sessionCookie, err := findCookie(cookies, "session_id")
	if err != nil {
		t.Fatal("session_id cookie not found after login")
	}

	// 2. Test Session Status (Authorized)
	statusReq := httptest.NewRequest("GET", "/api/session/status", nil)
	statusReq.AddCookie(sessionCookie)
	statusRR := httptest.NewRecorder()
	server.ServeHTTP(statusRR, statusReq)

	if status := statusRR.Code; status != http.StatusOK {
		t.Errorf("session status returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// 3. Test Generic Proxy (Authorized)
	proxyReq := httptest.NewRequest("GET", "/api-proxy/some/endpoint", nil)
	proxyReq.AddCookie(sessionCookie)
	proxyRR := httptest.NewRecorder()
	server.ServeHTTP(proxyRR, proxyReq)

	if status := proxyRR.Code; status != http.StatusOK {
		t.Errorf("proxy returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if !strings.Contains(proxyRR.Body.String(), "proxied content") {
		t.Errorf("proxy returned unexpected body: got %s", proxyRR.Body.String())
	}

	// 4. Test Logout
	logoutReq := httptest.NewRequest("POST", "/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRR := httptest.NewRecorder()
	server.ServeHTTP(logoutRR, logoutReq)

	if status := logoutRR.Code; status != http.StatusOK {
		t.Errorf("logout handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// 5. Test Session Status (Unauthorized after logout)
	statusAfterLogoutReq := httptest.NewRequest("GET", "/api/session/status", nil)
	statusAfterLogoutReq.AddCookie(sessionCookie) // Use the old cookie
	statusAfterLogoutRR := httptest.NewRecorder()
	server.ServeHTTP(statusAfterLogoutRR, statusAfterLogoutReq)

	if status := statusAfterLogoutRR.Code; status != http.StatusUnauthorized {
		t.Errorf("session status after logout returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}

func TestUnauthorizedRequests(t *testing.T) {
	upstream := mockUpstreamAPI()
	defer upstream.Close()

	server := setupTestApp(upstream.URL)

	// Test session status without cookie
	req := httptest.NewRequest("GET", "/api/session/status", nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("session status without cookie returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}

	// Test proxy without cookie
	proxyReq := httptest.NewRequest("GET", "/api-proxy/some/endpoint", nil)
	proxyRR := httptest.NewRecorder()
	server.ServeHTTP(proxyRR, proxyReq)

	if status := proxyRR.Code; status != http.StatusUnauthorized {
		t.Errorf("proxy without cookie returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}

func TestLoginFailure(t *testing.T) {
	upstream := mockUpstreamAPI()
	defer upstream.Close()

	server := setupTestApp(upstream.URL)

	// Test with bad credentials
	loginBody := `{"username": "baduser", "password": "badpassword"}`
	req := httptest.NewRequest("POST", "/login", strings.NewReader(loginBody))
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("login with bad credentials returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}
