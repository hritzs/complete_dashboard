package main

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ControlAPIPort     string
	SnapshotServiceURL string
	ContractMasterURL  string
	ZMQEndpoint        string
	XTSClientID        string

	GreekAuthURL  string
	GreekRestURL  string
	GreekUsername string
	GreekPassword string
	GreekClientID string
	GreekBrokerID int
	GreekPanDob   string
}

func LoadConfig() *Config {
	return &Config{
		ControlAPIPort:     getEnv("CONTROL_API_PORT", ":8005"),
		SnapshotServiceURL: getEnv("SNAPSHOT_SERVICE_URL", "http"+"://127.0.0.1:8003"),
		ContractMasterURL:  getEnv("CONTRACT_MASTER_URL", "http"+"://127.0.0.1:8010"),
		ZMQEndpoint:        getEnv("ZMQ_ENDPOINT", "tcp"+"://127.0.0.1:5557"),
		XTSClientID:        getEnv("XTS_CLIENT_ID", ""),

		GreekAuthURL:  getEnv("GREEK_API_AUTH_URL", "http"+"://greekapi.greeksoft.in:3001"),
		GreekRestURL:  getEnv("GREEK_API_REST_URL", "http"+"://restapi.greeksoft.in:3434"),
		GreekUsername: getEnv("GREEK_USERNAME", ""),
		GreekPassword: getEnv("GREEK_PASSWORD", ""),
		GreekClientID: getEnv("GREEK_CLIENT_ID", getEnv("GREEK_USERNAME", "")),
		GreekBrokerID: getEnvInt("GREEK_BROKER_ID", 22),
		GreekPanDob:   getEnv("GREEK_PAN_DOB", "01/01/1901"),
	}
}

func getEnv(k string, d string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	return v
}

func getEnvInt(k string, d int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}

	return n
}
