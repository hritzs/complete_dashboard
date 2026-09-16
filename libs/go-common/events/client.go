package events

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

// Connect establishes a connection to the NATS server.
func Connect(log *logrus.Logger) (*nats.Conn, error) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL // "nats://127.0.0.1:4222"
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS at %s: %w", natsURL, err)
	}

	log.Infof("✅ Successfully connected to NATS at %s", natsURL)
	return nc, nil
}

// Publish sends an event to a NATS subject.
func Publish(nc *nats.Conn, subject string, event interface{}) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event for subject %s: %w", subject, err)
	}
	if err := nc.Publish(subject, payload); err != nil {
		return fmt.Errorf("failed to publish event to subject %s: %w", subject, err)
	}
	return nil
}

// Subscribe registers a handler for every message published to subject,
// decoding the raw payload as JSON into a new T for each message. Decode
// errors are passed to onDecodeError rather than silently dropped; pass
// nil to ignore them.
func Subscribe[T any](nc *nats.Conn, subject string, handler func(T), onDecodeError func(error)) (*nats.Subscription, error) {
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var event T
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			if onDecodeError != nil {
				onDecodeError(fmt.Errorf("decode event on subject %s: %w", subject, err))
			}
			return
		}
		handler(event)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}
	return sub, nil
}