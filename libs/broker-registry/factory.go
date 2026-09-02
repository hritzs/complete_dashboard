package registry

import (
	"fmt"
	"strings"

	greeksoft "trading-platform/libs/broker-greeksoft"
	xts "trading-platform/libs/broker-xts"
	broker "trading-platform/libs/go-broker"
)

type BrokerFactory struct{}

func NewBrokerFactory() *BrokerFactory {
	return &BrokerFactory{}
}

func (f *BrokerFactory) CreateClient(cfg *BrokerConfig) (broker.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("broker config cannot be nil")
	}

	brokerType := strings.ToLower(strings.TrimSpace(cfg.BrokerType))

	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("broker config missing base_url")
	}

	switch brokerType {

	case "xts", "symphony xts":

		if cfg.APIKey == "" {
			return nil, fmt.Errorf("xts broker requires api_key")
		}

		if cfg.APISecret == "" {
			return nil, fmt.Errorf("xts broker requires api_secret")
		}

		return xts.NewClient(), nil

	case "greeksoft":

		return greeksoft.NewClient(
			cfg.BaseURL,
			cfg.BaseURL,
		), nil

	default:
		return nil, fmt.Errorf("unsupported broker type: %s", cfg.BrokerType)
	}
}
