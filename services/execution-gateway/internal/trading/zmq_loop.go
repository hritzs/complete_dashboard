package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	zmq "github.com/pebbe/zmq4"

	broker "trading-platform/libs/go-broker"
	"execution-gateway/internal/brokers"
)

func RunZMQLoop(ctx context.Context, subscriber *zmq.Socket, b brokers.BrokerClient, globalClientID string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := subscriber.Recv(0)
			if err != nil {
				continue
			}
			var intent broker.OrderIntent
			if err := json.Unmarshal([]byte(msg), &intent); err != nil {
				log.Printf("Failed to unmarshal OrderIntent: %v | raw: %s", err, msg)
				continue
			}
			log.Printf("📥 [ZMQ] client=%q token=%d side=%s qty=%d",
				intent.ClientID, intent.InstrumentToken, intent.Side, intent.Quantity)

			if intent.ExchangeSegment == "" {
				intent.ExchangeSegment = "NSEFO"
			}
			if intent.ProductType == "" {
				intent.ProductType = "MIS"
			}
			if intent.OrderType == "" {
				intent.OrderType = "MARKET"
			}
			if intent.ClientID == "" {
				intent.ClientID = globalClientID
			}
			if intent.Side == "" {
				intent.Side = "SELL"
			}
			uid := fmt.Sprintf("ZMQ_%d_%d", intent.InstrumentToken, time.Now().UnixNano()%1000000)
			go func(i broker.OrderIntent, u string) {
				if err := b.PlaceOrder(i, u, 0); err != nil {
					log.Printf("❌ ZMQ PlaceOrder failed: %v", err)
				} else {
					log.Printf("✅ ZMQ Order sent: %s", u)
				}
			}(intent, uid)
		}
	}
}
