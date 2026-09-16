// Package publish broadcasts reconciler-derived canonical events over NATS
// so other services can subscribe instead of polling Postgres/the broker.
package publish

import (
	"time"

	"github.com/nats-io/nats.go"

	"trading-platform/libs/contracts"
	"trading-platform/libs/go-common/events"
	"trading-platform/services/reconciler/internal/normalize"
	"trading-platform/services/reconciler/internal/persistence"
)

type Publisher struct {
	nc *nats.Conn
}

func NewPublisher(nc *nats.Conn) *Publisher {
	return &Publisher{nc: nc}
}

// PublishOrderUpdate converts a normalize.OrderUpdate into the shared
// contracts.OrderUpdate shape and publishes it to events.TopicOrderUpdates.
func (p *Publisher) PublishOrderUpdate(tradeUID string, update normalize.OrderUpdate) error {
	return events.Publish(p.nc, events.TopicOrderUpdates, contracts.OrderUpdate{
		BrokerOrderID:   update.BrokerOrderID,
		ExchangeOrderID: update.ExchangeOrderID,
		TradeID:         tradeUID,
		Status:          string(update.Status),
		FilledQty:       update.FilledQtyToday,
		PendingQty:      update.PendingQty,
		AvgFillPrice:    update.Price,
		ReasonText:      update.ReasonText,
		BrokerTimestamp: update.BrokerTimestamp,
		InternalTime:    time.Now(),
	})
}

// PublishFillEvent converts what persistence actually wrote (fill_id,
// instrument token, the derived fill) into the shared contracts.FillEvent
// shape and publishes it to events.TopicFillEvents.
//
// IntentID is left empty: OrderResponse frames don't echo back the
// client-supplied intent/correlation tag (confirmed against a live
// capture -- see docs/greeksoft-integration-architecture.md), so there is
// no intent id available at this layer to populate it with.
func (p *Publisher) PublishFillEvent(tradeUID string, persisted persistence.PersistedFill, side string) error {
	return events.Publish(p.nc, events.TopicFillEvents, contracts.FillEvent{
		FillID:        persisted.FillID,
		BrokerOrderID: persisted.Event.BrokerOrderID,
		TradeID:       tradeUID,
		InstrumentID:  persisted.InstrumentID,
		Side:          side,
		FillQty:       persisted.Event.FillQtyDelta,
		FillPrice:     persisted.Event.FillPrice,
		FillTime:      persisted.Event.BrokerTimestamp,
	})
}
