package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// GenericProxyHandler proxies API requests using the server-side session token.
func (h *Handlers) GenericProxyHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		slog.Warn("Proxy request rejected: no session cookie")
		http.Error(w, `{"message": "Unauthorized: No session cookie. Please log in."}`, http.StatusUnauthorized)
		return
	}
	sessionID := cookie.Value
	sess, ok := h.SessionStore.Get(sessionID)
	if !ok {
		slog.Warn("Proxy request rejected: invalid session ID", "sessionID", sessionID)
		http.Error(w, `{"message": "Unauthorized: Invalid or expired session. Please log in."}`, http.StatusUnauthorized)
		return
	}

	var requestBodyBytes []byte
	if r.Body != nil {
		requestBodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			slog.Error("Failed to read request body", "endpoint", r.URL.Path, "error", err)
			http.Error(w, `{"message": "Invalid request body"}`, http.StatusBadRequest)
			return
		}
	}
	r.Body = io.NopCloser(bytes.NewBuffer(requestBodyBytes))

	endpoint := strings.TrimPrefix(r.URL.Path, "/api-proxy/")
	targetURLStr := fmt.Sprintf("%s/%s", h.Config.GreekRestApiBaseUrl, endpoint)
	if r.URL.RawQuery != "" {
		targetURLStr += "?" + r.URL.RawQuery
	}

	slog.Info("Proxying generic request", "user", sess.Username, "method", r.Method, "target", targetURLStr)

	if endpoint == "jloginNew" && r.Method == http.MethodPost {
		h.handleJLoginSequence(w, r, sessionID, &sess, requestBodyBytes)
		return
	}

	if r.Method == http.MethodPost {
		if err := validateRequest(endpoint, requestBodyBytes); err != nil {
			slog.Warn("Client request validation failed", "endpoint", endpoint, "error", err, "body", string(requestBodyBytes))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"message": "Invalid request from client: " + err.Error()})
			return
		}
	}

	proxyReq, err := http.NewRequest(r.Method, targetURLStr, bytes.NewReader(requestBodyBytes))
	if err != nil {
		slog.Error("Failed to create generic proxy request", "target", targetURLStr, "error", err)
		http.Error(w, `{"message": "Internal server error"}`, http.StatusInternalServerError)
		return
	}

	proxyReq.Header.Set("Authorization", sess.UpstreamToken)
	proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	apiRes, err := h.Client.Do(proxyReq)
	if err != nil {
		handleProxyError(w, err, targetURLStr)
		return
	}
	defer apiRes.Body.Close()

	copyResponse(w, apiRes)
}

// SessionStatusHandler checks if the user has a valid session.
func (h *Handlers) SessionStatusHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		http.Error(w, `{"message": "No active session"}`, http.StatusUnauthorized)
		return
	}
	sessionID := cookie.Value

	sess, ok := h.SessionStore.Get(sessionID)
	if !ok {
		http.Error(w, `{"message": "Invalid session"}`, http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	responseSession := sess
	responseSession.UpstreamToken = "" // Don't send the token back
	json.NewEncoder(w).Encode(responseSession)
}

// ServeIndexHandler serves the main application page.
func (h *Handlers) ServeIndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, h.Config.StaticFilePath+"/login.html")
}
