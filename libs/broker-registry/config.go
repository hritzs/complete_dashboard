package registry

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// BrokerConfig holds broker connection parameters
type BrokerConfig struct {
	BrokerType string        `yaml:"broker_type" json:"broker_type"`
	Name       string        `yaml:"name" json:"name"`
	BaseURL    string        `yaml:"base_url" json:"base_url"`
	APIKey     string        `yaml:"api_key" json:"api_key"`
	APISecret  string        `yaml:"api_secret" json:"api_secret"`
	ClientID   string        `yaml:"client_id" json:"client_id"`
	Source     string        `yaml:"source" json:"source"`       // XTS specific
	BrokerID   int           `yaml:"broker_id" json:"broker_id"` // Greeksoft specific
	PanDob     string        `yaml:"pan_dob" json:"pan_dob"`     // Greeksoft specific
	Timeout    time.Duration `yaml:"timeout" json:"timeout"`     // Connection timeout
}

// RegistryConfig holds the collection of broker configurations
type RegistryConfig struct {
	Brokers map[string]*BrokerConfig `yaml:"brokers" json:"brokers"`
}

// LoadConfig loads broker configurations from YAML file
func LoadConfig(filePath string) (*RegistryConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &RegistryConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate brokers map is not nil
	if cfg.Brokers == nil {
		cfg.Brokers = make(map[string]*BrokerConfig)
	}

	return cfg, nil
}

// LoadConfigFromEnv loads broker credentials from environment variables
// Format: BROKER_<BROKERNAME>_<FIELD> = value
// Example: BROKER_XTS_BASEURL, BROKER_XTS_APIKEY, etc.
func LoadConfigFromEnv(brokerName string) (*BrokerConfig, error) {
	cfg := &BrokerConfig{
		Name: brokerName,
	}

	prefix := "BROKER_" + brokerName + "_"

	// Common fields
	if v, ok := os.LookupEnv(prefix + "BROKER_TYPE"); ok {
		cfg.BrokerType = v
	}
	if v, ok := os.LookupEnv(prefix + "BASE_URL"); ok {
		cfg.BaseURL = v
	}
	if v, ok := os.LookupEnv(prefix + "API_KEY"); ok {
		cfg.APIKey = v
	}
	if v, ok := os.LookupEnv(prefix + "API_SECRET"); ok {
		cfg.APISecret = v
	}
	if v, ok := os.LookupEnv(prefix + "CLIENT_ID"); ok {
		cfg.ClientID = v
	}
	if v, ok := os.LookupEnv(prefix + "SOURCE"); ok {
		cfg.Source = v
	}

	return cfg, nil
}
