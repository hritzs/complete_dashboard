package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

func (h *Handlers) GenericProxyHandler(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := h.getSessionFromCookie(r)
	if err != nil {
		slog.Warn("Proxy request rejected", "error", err)
		http.Error(w, `{"message":"Unauthorized. Please log in."}`, http.StatusUnauthorized)
		return
	}

	var requestBodyBytes []byte
	if r.Body != nil {
		requestBodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			slog.Error("Failed to read request body", "endpoint", r.URL.Path, "error", err)
			http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
			return
		}
	}
	r.Body = io.NopCloser(bytes.NewBuffer(requestBodyBytes))

	endpoint := strings.TrimPrefix(r.URL.Path, "/api-proxy/")
	targetURLStr := h.Config.GreekRestApiBaseURL + "/" + endpoint
	if r.URL.RawQuery != "" {
		targetURLStr += "?" + r.URL.RawQuery
	}

	slog.Info("Proxying generic request", "user", sess.Username, "method", r.Method, "target", targetURLStr)

	if endpoint == "jloginNew" && r.Method == http.MethodPost {
		h.handleJLoginSequence(w, sessionID, sess, requestBodyBytes)
		return
	}

	if r.Method == http.MethodPost {
		if err := validateRequest(endpoint, requestBodyBytes); err != nil {
			slog.Warn("Client request validation failed", "endpoint", endpoint, "error", err, "body", string(requestBodyBytes))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": "Invalid request from client: " + err.Error(),
			})
			return
		}
	}

	proxyReq, err := http.NewRequest(r.Method, targetURLStr, bytes.NewReader(requestBodyBytes))
	if err != nil {
		slog.Error("Failed to create generic proxy request", "target", targetURLStr, "error", err)
		http.Error(w, `{"message":"Internal server error"}`, http.StatusInternalServerError)
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

func (h *Handlers) SessionStatusHandler(w http.ResponseWriter, r *http.Request) {
	sess, _, err := h.getSessionFromCookie(r)
	if err != nil {
		http.Error(w, `{"message":"No active session"}`, http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	responseSession := *sess
	responseSession.UpstreamToken = ""
	_ = json.NewEncoder(w).Encode(responseSession)
}

func (h *Handlers) ServeIndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, h.Config.StaticFilePath+"/login.html")
}
	