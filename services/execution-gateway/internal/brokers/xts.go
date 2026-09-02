package brokers

import (
	xts "trading-platform/libs/broker-xts"
	"trading-platform/libs/broker-xts/interactive"
	broker "trading-platform/libs/go-broker"
)

type BrokerClient interface {
	PlaceOrder(intent broker.OrderIntent, orderUID string, limitPrice float64) error
}

type XTSWrapper struct {
	client *xts.Client
}

func NewXTSWrapper(c *xts.Client) BrokerClient {
	return &XTSWrapper{client: c}
}

func (w *XTSWrapper) PlaceOrder(intent broker.OrderIntent, orderUID string, limitPrice float64) error {
	return interactive.PlaceOrder(w.client, intent, orderUID, limitPrice)
}
