package trading

import (
	"fmt"
	"strings"
)

type Provider func(
	userID string,
	accountID string,
) (Executor, error)

type DefaultBrokerFactory struct {
	providers map[string]Provider
}

func NewDefaultBrokerFactory() *DefaultBrokerFactory {
	return &DefaultBrokerFactory{
		providers: make(map[string]Provider),
	}
}

func (f *DefaultBrokerFactory) Register(
	brokerName string,
	provider Provider,
) {
	if provider == nil {
		return
	}

	f.providers[strings.ToUpper(strings.TrimSpace(brokerName))] = provider
}

func (f *DefaultBrokerFactory) GetExecutor(
	userID,
	brokerName,
	accountID string,
) (Executor, error) {

	name := strings.ToUpper(
		strings.TrimSpace(brokerName),
	)

	if name == "" {
		return nil, fmt.Errorf("brokerName is required")
	}

	provider, ok := f.providers[name]
	if !ok {
		return nil,
			fmt.Errorf(
				"unsupported broker %s",
				brokerName,
			)
	}

	return provider(userID, accountID)
}
