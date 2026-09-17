// reconciler consumes GreekSoft's live Iris order-push feed, persists
// canonical order/fill state to Postgres transactionally, and publishes
// canonical events over NATS. See
// docs/greeksoft-integration-architecture.md for the design and
// docs/TODO.md ("Phase 5") for what's built vs still pending.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"

	greeksoft "trading-platform/libs/broker-greeksoft"
	"trading-platform/libs/broker-greeksoft/ingest"
	"trading-platform/libs/broker-greeksoft/normalize"
	broker "trading-platform/libs/go-broker"
	"trading-platform/libs/go-common/events"
	"trading-platform/services/reconciler/internal/persistence"
	"trading-platform/services/reconciler/internal/publish"
	"trading-platform/services/reconciler/internal/recover"
)

func requiredEnv(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		log.Fatalf("required environment variable %s is empty", name)
	}
	return v
}

func envOrDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// resolveGreekAccountID matches the rest of the platform's .env
// convention (GREEK_CLIENT_ID/GREEK_USERNAME, e.g. "147") rather than
// requiring a separate GREEK_ACCOUNT_ID variable nothing else sets.
func resolveGreekAccountID() string {
	for _, key := range []string{"GREEK_ACCOUNT_ID", "GREEK_CLIENT_ID", "GREEK_USERNAME"} {
		if v := strings.ToUpper(strings.TrimSpace(os.Getenv(key))); v != "" {
			return v
		}
	}
	return "HRITIK"
}

func main() {
	authURL := requiredEnv("GREEK_API_AUTH_URL")
	restURL := requiredEnv("GREEK_API_REST_URL")
	password := requiredEnv("GREEK_PASSWORD")
	panDOB := requiredEnv("GREEK_PAN_DOB")
	accountID := resolveGreekAccountID()
	dsn := requiredEnv("POSTGRES_DSN")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	healthPort := envOrDefault("RECONCILER_HEALTH_PORT", "8021")
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","service":"reconciler"}`))
		})
		if err := http.ListenAndServe(":"+healthPort, mux); err != nil {
			log.Printf("[RECONCILER] health server exited: %v", err)
		}
	}()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping postgres: %v", err)
	}
	store := persistence.NewStore(db)

	nc, err := events.Connect(logrus.New())
	var publisher *publish.Publisher
	if err != nil {
		log.Printf("[RECONCILER] NATS connect failed, continuing without event publishing: %v", err)
	} else {
		defer nc.Close()
		publisher = publish.NewPublisher(nc)
	}

	client := greeksoft.NewClient(authURL, restURL)
	accCfg := &broker.AccountConfig{
		Name:       "greeksoft-reconciler-" + strings.ToLower(accountID),
		BrokerType: "greeksoft",
		APIKey:     accountID,
		APISecret:  password,
		ClientID:   accountID,
		PanDob:     panDOB,
	}
	loginCtx, loginCancel := context.WithTimeout(ctx, 30*time.Second)
	session, err := client.LoginShared(loginCtx, db, accCfg, 10*time.Minute)
	loginCancel()
	if err != nil {
		log.Fatalf("GreekSoft login failed: %v", err)
	}
	log.Printf("[RECONCILER] login successful account=%s user_id=%s", accountID, session.UserID)

	// handleApplied runs after a successful ApplyOrderUpdate, whether that
	// happened on the first try or after retryUnmatched's backoff below.
	handleApplied := func(result persistence.ApplyResult, update normalize.OrderUpdate) {
		// Iris order-confirmation latency: time between execution-gateway
		// creating the local order row (order submission) and this push
		// being processed. Logged unconditionally (not just on fills) so
		// it's visible on every status transition, including the initial
		// ack.
		log.Printf("[RECONCILER] latency broker_order_id=%s status=%s confirm_latency=%s",
			update.BrokerOrderID, update.Status, result.ConfirmationLatency)
		if err := store.RecordConfirmationLatencyIfFirst(ctx, result.OrderID, result.TradeUID, update.BrokerOrderID, result.ConfirmationLatency); err != nil {
			log.Printf("[RECONCILER] record latency sample failed: %v", err)
		}

		if publisher == nil {
			return
		}
		if err := publisher.PublishOrderUpdate(result.TradeUID, update); err != nil {
			log.Printf("[RECONCILER] publish order update failed: %v", err)
		}
		if result.Fill != nil {
			if err := publisher.PublishFillEvent(result.TradeUID, *result.Fill, update.Side); err != nil {
				log.Printf("[RECONCILER] publish fill event failed: %v", err)
			}
		}
	}

	// retryUnmatched handles a confirmed live race (2026-09-17): an Iris
	// push can arrive and be dispatched before execution-gateway's own
	// order-row write (broker_order_id) commits -- ExecuteOrderIntent
	// does its own synchronous REST-based order confirmation (up to
	// ~2s) before it writes broker_order_id, while GreekSoft's Iris push
	// for that same order often arrives within tens of milliseconds of
	// placement. Without this retry, that race silently drops a real
	// fill (confirmed live: a square-off's BUY-back orders filled at the
	// broker but were never recorded, leaving the DB showing an open
	// position the broker didn't actually have).
	//
	// Runs in its own goroutine, never blocking the Iris read loop, so a
	// slow-to-match order can't delay processing of other orders'
	// pushes. Six attempts over 300ms each (~1.8s) comfortably covers
	// the observed race window without duplicating the 30s recovery
	// poller's job of handling much longer gaps.
	retryUnmatched := func(update normalize.OrderUpdate, raw []byte) {
		for attempt := 0; attempt < 6; attempt++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}

			result, err := store.ApplyOrderUpdate(ctx, update, raw)
			if err == persistence.ErrOrderNotFound {
				continue
			}
			if err != nil {
				log.Printf("[RECONCILER] retry persist broker_order_id=%s failed: %v", update.BrokerOrderID, err)
				return
			}
			log.Printf("[RECONCILER] matched broker_order_id=%s on retry attempt=%d", update.BrokerOrderID, attempt+1)
			handleApplied(result, update)
			return
		}
		log.Printf("[RECONCILER] no matching order row for broker_order_id=%s after retries -- giving up (recovery poller will catch it later)", update.BrokerOrderID)
	}

	applyAndPublish := func(update normalize.OrderUpdate, raw []byte) {
		result, err := store.ApplyOrderUpdate(ctx, update, raw)
		if err == persistence.ErrOrderNotFound {
			go retryUnmatched(update, raw)
			return
		}
		if err != nil {
			log.Printf("[RECONCILER] persist broker_order_id=%s failed: %v", update.BrokerOrderID, err)
			return
		}
		handleApplied(result, update)
	}

	// Recovery/catch-up path: reconciles orders stuck in a non-terminal
	// state with no recent Iris activity against the REST order book. Not
	// the primary path -- Iris push (below) is.
	recoverer := recover.NewRecoverer(db, client, func(recoverCtx context.Context, entry greeksoft.OrderBookEntry) error {
		update := normalize.ParseFromOrderBookEntry(entry)
		raw, _ := json.Marshal(entry)
		result, err := store.ApplyOrderUpdate(recoverCtx, update, raw)
		if err == persistence.ErrOrderNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if publisher != nil {
			if err := publisher.PublishOrderUpdate(result.TradeUID, update); err != nil {
				log.Printf("[RECONCILER] publish (recovery) order update failed: %v", err)
			}
		}
		return nil
	})
	go recoverer.Run(ctx, 30*time.Second)

	log.Printf("[RECONCILER] running -- consuming Iris live order-push feed")

	err = client.ReadLoopWithReconnect(ctx, db, accCfg, func(frame greeksoft.IrisFrame) {
		kind, orderPayload, tradePayload, err := ingest.Dispatch(frame)
		if err != nil {
			log.Printf("[RECONCILER] dispatch failed: %v raw=%s", err, string(frame.Raw))
			return
		}
		switch kind {
		case ingest.KindOrderResponse:
			if orderPayload == nil {
				return
			}
			applyAndPublish(normalize.Parse(orderPayload), frame.Raw)
		case ingest.KindTradeResponse:
			if tradePayload == nil {
				return
			}
			// TradeResponse carries the actual executed fill
			// (order_status "Executed", exact traded_price/traded_qty) --
			// it is NOT a duplicate of OrderResponse and must be applied
			// too, or fills silently never get recorded. See
			// normalize.ParseTradeResponse's doc comment.
			applyAndPublish(normalize.ParseTradeResponse(tradePayload), frame.Raw)
		}
	})
	if err != nil && ctx.Err() == nil {
		log.Fatalf("Iris read loop exited unexpectedly: %v", err)
	}
	log.Printf("[RECONCILER] shutting down")
}
