package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

const (
	defaultGreekAPIBaseURL    = "http://greekapi.greeksoft.in:3001"
	defaultGreekRESTAPIURL    = "http://restapi.greeksoft.in:3434"
	defaultRequestTimeoutSecs = 30
	defaultListenHost         = "0.0.0.0"
	defaultListenPort         = "8080"
)

// Config holds application configuration.
type Config struct {
	Host                string
	Port                string
	StaticFilePath      string
	GreekAuthApiBaseUrl string
	GreekRestApiBaseUrl string
	RequestTimeout      time.Duration
}

// Load loads configuration from environment variables with defaults.
func Load() Config {
	host := getEnvOrDefault("LISTEN_HOST", defaultListenHost)
	port := getEnvOrDefault("PORT", defaultListenPort)
	staticPath := getEnvOrDefault("STATIC_FILE_PATH", "../../ui")
	authURL := getEnvOrDefault("GREEK_API_BASE_URL", defaultGreekAPIBaseURL)
	restURL := getEnvOrDefault("GREEK_REST_API_URL", defaultGreekRESTAPIURL)
	timeoutStr := getEnvOrDefault("REQUEST_TIMEOUT", fmt.Sprintf("%d", defaultRequestTimeoutSecs))
	timeoutSec, err := strconv.Atoi(timeoutStr)
	if err != nil {
		slog.Warn("Invalid REQUEST_TIMEOUT value, using default", "value", timeoutStr, "default", defaultRequestTimeoutSecs)
		timeoutSec = defaultRequestTimeoutSecs
	}
	return Config{
		Host: host, Port: port, StaticFilePath: staticPath,
		GreekAuthApiBaseUrl: authURL, GreekRestApiBaseUrl: restURL,
		RequestTimeout: time.Duration(timeoutSec) * time.Second,
	}
}

// getEnvOrDefault fetches an environment variable or returns a default value.
func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
