package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Port                   string
	GreekAuthApiBaseURL    string
	GreekRestApiBaseURL    string
	StaticFilePath         string
	SnapshotServiceBaseURL string
	ContractMasterBaseURL  string
	ExecutionGatewayBaseURL string
	RequestTimeout         time.Duration
}

func Load() Config {
	return Config{
		Port:                    getEnv("PORT", "8006"),
		GreekAuthApiBaseURL:     strings.TrimRight(getEnv("GREEK_AUTH_API_BASE_URL", "http://127.0.0.1:8081"), "/"),
		GreekRestApiBaseURL:     strings.TrimRight(getEnv("GREEK_REST_API_BASE_URL", "http://127.0.0.1:8082"), "/"),
		StaticFilePath:          getEnv("STATIC_FILE_PATH", "../ui"),
		SnapshotServiceBaseURL:  strings.TrimRight(getEnv("SNAPSHOT_SERVICE_BASE_URL", "http://127.0.0.1:8003"), "/"),
		ContractMasterBaseURL:   strings.TrimRight(getEnv("CONTRACT_MASTER_BASE_URL", "http://127.0.0.1:8010"), "/"),
		ExecutionGatewayBaseURL: strings.TrimRight(getEnv("EXECUTION_GATEWAY_BASE_URL", "http://127.0.0.1:8005"), "/"),
		RequestTimeout:          20 * time.Second,
	}
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}