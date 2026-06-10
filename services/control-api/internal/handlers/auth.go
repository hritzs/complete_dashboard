package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"trading-platform/services/control-api/internal/config"
	"trading-platform/services/control-api/internal/session"
)

// Handlers holds dependencies for HTTP handlers.
type Handlers struct {
	Config       config.Config
	Client       *http.Client
	SessionStore *session.Store
}

func New(cfg config.Config, client *http.Client, store *session.Store) *Handlers {
	return &Handlers{Config: cfg, Client: client, SessionStore: store}
}

// LoginProxyHandler proxies login, and on success, creates a server-side session.
func (h *Handlers) LoginProxyHandler(w http.ResponseWriter, r *http.Request) {
	sessionTokenEndpoint := fmt.Sprintf("%s/auth/greek/sessiontoken", h.Config.GreekAuthApiBaseUrl)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read login request body", "error", err)
		http.Error(w, `{"message": "Invalid request body"}`, http.StatusBadRequest)
		return
	}

	var loginData struct {
		Username string      `json:"username"`
		BrokerID interface{} `json:"brokerId"`
	}
	username, brokerID := "N/A", "N/A"
	if err := json.Unmarshal(bodyBytes, &loginData); err == nil {
		if loginData.Username != "" {
			username = loginData.Username
		}
		if loginData.BrokerID != nil {
			brokerID = fmt.Sprintf("%v", loginData.BrokerID)
		}
	}
	slog.Info("Proxying login request", "user", username)

	proxyReq, err := http.NewRequest(http.MethodPost, sessionTokenEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		slog.Error("Failed to create proxy request to Greek API", "error", err)
		http.Error(w, `{"message": "Internal server error"}`, http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	apiRes, err := h.Client.Do(proxyReq)
	if err != nil {
		handleProxyError(w, err, sessionTokenEndpoint)
		return
	}
	defer apiRes.Body.Close()

	if apiRes.StatusCode >= 200 && apiRes.StatusCode < 300 {
		var apiResponse struct {
			SessionToken string `json:"sessionToken"`
		}
		apiBodyBytes, err := io.ReadAll(apiRes.Body)
		if err != nil {
			slog.Error("Failed to read upstream login response", "error", err)
			http.Error(w, `{"message": "Internal server error while reading upstream response"}`, http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(apiBodyBytes, &apiResponse); err != nil {
			slog.Error("Failed to parse upstream login response", "error", err, "body", string(apiBodyBytes))
			http.Error(w, `{"message": "Internal server error while parsing upstream response"}`, http.StatusInternalServerError)
			return
		}
		if apiResponse.SessionToken == "" {
			slog.Error("Upstream login success but no session token returned", "body", string(apiBodyBytes))
			http.Error(w, `{"message": "Login failed: No session token from API"}`, http.StatusUnauthorized)
			return
		}

		sessionID := h.SessionStore.Create(session.Session{
			UpstreamToken: apiResponse.SessionToken,
			Username:      username,
			BrokerID:      brokerID,
			CreatedAt:     time.Now(),
		})
		slog.Info("Created new server-side session", "user", username, "sessionID", sessionID)

		http.SetCookie(w, &http.Cookie{
			Name: "session_id", Value: sessionID, Path: "/",
			Expires: time.Now().Add(30 * 24 * time.Hour), HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"username": username,
			"brokerId": brokerID,
		})
		return
	}

	copyResponse(w, apiRes)
}

// getSessionFromCookie retrieves the server-side session based on the session cookie in the request.
func (h *Handlers) getSessionFromCookie(r *http.Request) (*session.Session, error) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		return nil, errors.New("authentication required: missing session cookie")
	}

	sess := h.SessionStore.Get(cookie.Value)
	if sess == nil {
		return nil, errors.New("authentication required: invalid session")
	}

	return sess, nil
}

// LogoutHandler clears the server-side session and expires the client cookie.
func (h *Handlers) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session_id"); err == nil {
		h.SessionStore.Delete(cookie.Value)
		slog.Info("Cleared server-side session", "sessionID", cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session_id", Value: "", Path: "/", Expires: time.Unix(0, 0), HttpOnly: true,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Logged out successfully"})
}

// handleJLoginSequence performs the multi-step login to get trading session details.
func (h *Handlers) handleJLoginSequence(w http.ResponseWriter, r *http.Request, sessionID string, sess *session.Session, jloginBodyBytes []byte) {
	slog.Info("Handling jlogin sequence", "user", sess.Username)

	// Step 1: Proxy the jloginNew request
	jloginURL := fmt.Sprintf("%s/jloginNew", h.Config.GreekRestApiBaseUrl)
	if jloginBodyBytes == nil {
		http.Error(w, `{"message": "Invalid request body"}`, http.StatusBadRequest)
		return
	}
	jloginReq, err := http.NewRequest(http.MethodPost, jloginURL, bytes.NewReader(jloginBodyBytes))
	if err != nil {
		http.Error(w, `{"message": "Internal server error"}`, http.StatusInternalServerError)
		return
	}
	jloginReq.Header.Set("Authorization", sess.UpstreamToken)
	jloginReq.Header.Set("Content-Type", "application/json")

	jloginRes, err := h.Client.Do(jloginReq)
	if err != nil {
		handleProxyError(w, err, jloginURL)
		return
	}
	defer jloginRes.Body.Close()

	var jloginResponse struct {
		Response struct {
			Data struct {
				ErrorCode  int    `json:"ErrorCode"`
				ClientCode string `json:"ClientCode"`
			} `json:"data"`
		} `json:"response"`
	}
	jloginResBodyBytes, _ := io.ReadAll(jloginRes.Body)
	if err := json.Unmarshal(jloginResBodyBytes, &jloginResponse); err != nil || jloginResponse.Response.Data.ErrorCode != 0 {
		slog.Error("jloginNew failed", "user", sess.Username, "status", jloginRes.StatusCode, "body", string(jloginResBodyBytes))
		w.WriteHeader(jloginRes.StatusCode)
		w.Write(jloginResBodyBytes)
		return
	}

	// Step 2: jloginNew was successful, store GCID and call getFlagValues
	sess.GCID = jloginResponse.Response.Data.ClientCode
	slog.Info("jloginNew successful", "user", sess.Username, "gcid", sess.GCID)

	flagValuesURL := fmt.Sprintf("%s/getFlagValues", h.Config.GreekRestApiBaseUrl)
	flagValuesBody := fmt.Sprintf(`{"request":{"svcVersion":"1.0.0","svcGroup":"","svcName":"getFlagValues","gscid":"%s","data":{}}}`, sess.Username)
	flagValuesReq, _ := http.NewRequest(http.MethodPost, flagValuesURL, strings.NewReader(flagValuesBody))
	flagValuesReq.Header.Set("Authorization", sess.UpstreamToken)
	flagValuesReq.Header.Set("Content-Type", "application/json")

	flagValuesRes, err := h.Client.Do(flagValuesReq)
	if err != nil {
		handleProxyError(w, err, flagValuesURL)
		return
	}
	defer flagValuesRes.Body.Close()

	var flagValuesResponse struct {
		Response struct {
			SessionID string `json:"sessionId"`
			Data      struct {
				IrisIP, IrisPort, ApolloIP, ApolloPort string
			} `json:"data"`
		} `json:"response"`
	}
	flagValuesResBodyBytes, _ := io.ReadAll(flagValuesRes.Body)
	if err := json.Unmarshal(flagValuesResBodyBytes, &flagValuesResponse); err != nil {
		slog.Error("getFlagValues failed", "user", sess.Username, "status", flagValuesRes.StatusCode, "body", string(flagValuesResBodyBytes))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(jloginResBodyBytes)
		return
	}

	// Step 3: Store all session details and send consolidated response
	sess.WSSessionID = flagValuesResponse.Response.SessionID
	sess.IrisIP = flagValuesResponse.Response.Data.IrisIP
	sess.IrisPort = flagValuesResponse.Response.Data.IrisPort
	sess.ApolloIP = flagValuesResponse.Response.Data.ApolloIP
	sess.ApolloPort = flagValuesResponse.Response.Data.ApolloPort

	// This is a critical step: update the session in the store
	h.SessionStore.Delete(sessionID) // Simple delete+create to update
	newSessionID := h.SessionStore.Create(*sess)

	// Must update the client's cookie with the new session ID
	http.SetCookie(w, &http.Cookie{
		Name: "session_id", Value: newSessionID, Path: "/",
		Expires: time.Now().Add(30 * 24 * time.Hour), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	slog.Info("Full trading session established", "user", sess.Username, "gcid", sess.GCID, "iris", sess.IrisIP)

	consolidatedResponse := map[string]interface{}{
		"response": map[string]interface{}{
			"data": map[string]interface{}{
				"ErrorCode": 0, "ClientCode": sess.GCID,
				"Iris_IP": sess.IrisIP, "Iris_Port": sess.IrisPort,
				"Apollo_IP": sess.ApolloIP, "Apollo_Port": sess.ApolloPort,
			},
			"sessionId": sess.WSSessionID,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(consolidatedResponse)
}
