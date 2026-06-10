package registry

import (
	"fmt"

	"trading-platform/libs/broker-greeksoft"
	"trading-platform/libs/broker-xts"
	broker "trading-platform/libs/go-broker"
)

// BrokerFactory creates broker clients based on configuration
type BrokerFactory struct{}

// NewBrokerFactory creates a new factory instance
func NewBrokerFactory() *BrokerFactory {
	return &BrokerFactory{}
}

// CreateClient instantiates a broker.Client based on BrokerType
// Supported types: "xts", "greeksoft"
func (f *BrokerFactory) CreateClient(cfg *BrokerConfig) (broker.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("broker config cannot be nil")
	}

	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("broker config missing base_url")
	}

	switch cfg.BrokerType {
	case "xts", "XTS", "Symphony XTS":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("XTS broker requires api_key")
		}
		if cfg.APISecret == "" {
			return nil, fmt.Errorf("XTS broker requires api_secret")
		}
		return xts.NewClient(cfg.BaseURL, cfg.APIKey, cfg.APISecret, cfg.Source), nil

	case "greeksoft", "Greeksoft", "GREEKSOFT":
		return greeksoft.NewClient(cfg.BaseURL), nil

	default:
		return nil, fmt.Errorf("unsupported broker type: %s", cfg.BrokerType)
	}
}
