package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ServiceConfig holds the configuration for the entire session-manager service.
type ServiceConfig struct {
	Accounts []AccountConfig `yaml:"accounts"`
}

// AccountConfig holds the credentials and details for a single broker account.
type AccountConfig struct {
	Name       string `yaml:"name"`
	BrokerType string `yaml:"broker_type"`
	APIKey     string `yaml:"api_key"`
	APISecret  string `yaml:"api_secret"`
	ClientID   string `yaml:"client_id"`
	Source     string `yaml:"source"`    // Used by XTS
	BrokerID   int    `yaml:"broker_id"` // Greeksoft specific
	PanDob     string `yaml:"pan_dob"`   // Greeksoft specific
}

// LoadConfig reads the YAML configuration file.
func LoadConfig(path string) (*ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg ServiceConfig
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config YAML: %w", err)
	}

	return &cfg, nil
}
