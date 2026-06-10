package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

// validateRequest performs basic validation for specific POST endpoints.
func validateRequest(endpoint string, body []byte) error {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	req, ok := data["request"].(map[string]interface{})
	if !ok {
		return errors.New(`missing "request" object in payload`)
	}
	reqData, ok := req["data"].(map[string]interface{})
	if !ok {
		return errors.New(`missing "data" object in "request"`)
	}

	var requiredFields []string
	switch endpoint {
	case "NewOrderRequest":
		requiredFields = []string{"gcid", "gtoken"}
	case "jloginNew":
		requiredFields = []string{"gscid", "pass"}
	default:
		return nil // No validation for this endpoint
	}

	for _, field := range requiredFields {
		val, exists := reqData[field]
		if !exists || val == nil || (fmt.Sprintf("%v", val) == "") {
			return fmt.Errorf("%s is missing or empty", field)
		}
	}
	return nil
}

// handleProxyError handles network errors when proxying requests.
func handleProxyError(w http.ResponseWriter, err error, targetURL string) {
	w.Header().Set("Content-Type", "application/json")
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		slog.Error("Timeout error when connecting to API", "target", targetURL, "error", err)
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(map[string]string{"message": "Request to upstream API timed out"})
		return
	}
	slog.Error("Connection error to API", "target", targetURL, "error", err)
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]string{"message": "Error connecting to the API"})
}

// copyResponse copies the status code, headers, and body from an upstream response.
func copyResponse(w http.ResponseWriter, apiRes *http.Response) {
	for key, values := range apiRes.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(apiRes.StatusCode)
	if _, err := io.Copy(w, apiRes.Body); err != nil {
		slog.Error("Failed to copy response body", "error", err)
	}
}
