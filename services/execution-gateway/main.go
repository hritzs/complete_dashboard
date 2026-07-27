package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	zmq "github.com/pebbe/zmq4"

	gs "trading-platform/libs/broker-greeksoft"
	xts "trading-platform/libs/broker-xts"
	"trading-platform/libs/broker-xts/interactive"
	broker "trading-platform/libs/go-broker"

	greeksoftbroker "execution-gateway/internal/brokers/greeksoft"
	"execution-gateway/internal/trading"
)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type xtsExecutor struct {
	client *xts.Client
}

func (x *xtsExecutor) ExecuteOrderIntent(ctx context.Context, intent trading.OrderIntent) (*trading.ExecutionResult, error) {
	orderUID := strings.TrimSpace(intent.OrderUID)
	if orderUID == "" {
		orderUID = strings.TrimSpace(intent.IntentID)
	}
	if orderUID == "" {
		orderUID = fmt.Sprintf("XTS-%d", time.Now().UnixNano())
	}

	orderType := strings.ToUpper(strings.TrimSpace(intent.OrderType))
	if orderType == "" {
		orderType = "MARKET"
	}

	productType := strings.ToUpper(strings.TrimSpace(intent.ProductType))
	if productType == "" {
		productType = "MIS"
	}

	side := strings.ToUpper(strings.TrimSpace(intent.Side))
	if side == "" {
		side = "SELL"
	}

	clientID := strings.TrimSpace(intent.AccountID)
	if clientID == "" {
		clientID = strings.TrimSpace(os.Getenv("XTS_CLIENT_ID"))
	}

	limitPrice := 0.0
	if intent.LimitPrice != nil {
		limitPrice = *intent.LimitPrice
	}

	exchangeSegment := strings.ToUpper(strings.TrimSpace(intent.ExchangeSegment))
	if exchangeSegment == "" {
		exchangeSegment = "NSEFO"
	}

	bIntent := broker.OrderIntent{
		TradeUID:        intent.TradeUID,
		IntentID:        intent.IntentID,
		InstrumentToken: int(intent.Token),
		ExchangeSegment: exchangeSegment,
		Side:            side,
		Quantity:        int(intent.Quantity),
		OrderType:       orderType,
		ProductType:     productType,
		TimeInForce:     "DAY",
		ClientID:        clientID,
		LimitPrice:      limitPrice,
		StopPrice:       0,
		DisclosedQty:    0,
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if err := interactive.PlaceOrder(x.client, bIntent, orderUID, limitPrice); err != nil {
		return nil, err
	}

	return &trading.ExecutionResult{
		IntentID:      intent.IntentID,
		BrokerOrderID: orderUID,
		Status:        "SUBMITTED",
		FilledQty:     0,
		FillPrice:     0,
		EventReason:   "XTS_ORDER_SENT",
	}, nil
}

func main() {
	log.Println("Starting Execution Gateway...")

	appConfig := LoadConfig()
	var sqlDB *sql.DB
	var store trading.Store = trading.NewMemoryStore()
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_DSN"))
	if dsn != "" {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			log.Printf("[SQL STORE] open failed err=%v; using memory store", err)
		} else if err := db.Ping(); err != nil {
			log.Printf("[SQL STORE] ping failed err=%v; using memory store", err)
		} else {
			sqlDB = db
			store = trading.NewPostgresBackedStore(db)
			log.Printf("[SQL STORE] postgres-backed store enabled")
		}
	} else {
		log.Printf("[SQL STORE] POSTGRES_DSN empty; using memory store")
	}

	service := trading.NewService(store, appConfig.XTSClientID)
	service.Snapshot = &trading.SnapshotClient{BaseURL: appConfig.SnapshotServiceURL}
	service.LotSize = &trading.LotSizeClient{BaseURL: appConfig.ContractMasterURL}

	xtsClient := xts.NewClient()

	factory := trading.NewDefaultBrokerFactory()

	factory.Register(
		"XTS",
		func(userID string, accountID string) (trading.Executor, error) {
			// This is a placeholder and would need a real implementation
			return &xtsExecutor{client: xtsClient}, nil
		},
	)

	factory.Register(
		"SIM",
		func(userID string, accountID string) (trading.Executor, error) {
			if sqlDB == nil {
				return nil, fmt.Errorf("SIM broker requires a postgres database connection")
			}
			return trading.NewSimExecutor(sqlDB, store)
		},
	)

	var greekMu sync.Mutex
	greekExecutors := map[string]trading.Executor{}

	factory.Register(
		"GREEKSOFT",
		func(userID string, accountID string) (trading.Executor, error) {
			greekMu.Lock()
			defer greekMu.Unlock()

			accountID = strings.ToUpper(strings.TrimSpace(accountID))
			if accountID == "" {
				accountID = "HRITIK"
			}

			if existing, ok := greekExecutors[accountID]; ok && existing != nil {
				return existing, nil
			}

			if strings.TrimSpace(appConfig.GreekAuthURL) == "" {
				return nil, fmt.Errorf("GREEK_API_AUTH_URL is required")
			}
			if strings.TrimSpace(appConfig.GreekRestURL) == "" {
				return nil, fmt.Errorf("GREEK_API_REST_URL is required")
			}
			if strings.TrimSpace(appConfig.GreekPassword) == "" {
				return nil, fmt.Errorf("GREEK_PASSWORD is required")
			}
			if appConfig.GreekBrokerID <= 0 {
				return nil, fmt.Errorf("GREEK_BROKER_ID is required")
			}
			if strings.TrimSpace(appConfig.GreekPanDob) == "" {
				return nil, fmt.Errorf("GREEK_PAN_DOB is required")
			}

			gsClient := gs.NewClient(appConfig.GreekAuthURL, appConfig.GreekRestURL)

			loginCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			session, err := gsClient.PerformFullLogin(loginCtx, &broker.AccountConfig{
				Name:       "greeksoft-" + strings.ToLower(accountID),
				BrokerType: "greeksoft",
				APIKey:     accountID,
				APISecret:  appConfig.GreekPassword,
				ClientID:   accountID,
				BrokerID:   appConfig.GreekBrokerID,
				PanDob:     appConfig.GreekPanDob,
			})
			if err != nil {
				return nil, fmt.Errorf("greeksoft login failed for account %s: %w", accountID, err)
			}

			log.Printf("Greeksoft executor ready account=%s user_id=%s", accountID, session.UserID)

			executor := greeksoftbroker.NewExecutor(gsClient)
			greekExecutors[accountID] = executor

			return executor, nil
		},
	)

	service.BrokerFactory = factory

	handlers := trading.NewHandlers(service, store)

	go startHTTPServer(appConfig, handlers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go waitForShutdown(cancel)
	go startZMQListener(ctx, appConfig, xtsClient)

	<-ctx.Done()
	log.Println("Execution Gateway stopped")
}

func startHTTPServer(cfg *Config, handlers *trading.Handlers) {
	mux := http.NewServeMux()

	if pgStore, ok := handlers.Store.(*trading.PostgresBackedStore); ok && pgStore.DB() != nil {
		trading.RegisterSimOrderBookRoutes(mux, pgStore.DB())
	}

	mux.HandleFunc("/api/health", handlers.Health)
	mux.HandleFunc("/api/trade/straddle", handlers.DeployStraddle)
	mux.HandleFunc("/api/straddle/sell", handlers.DeployStraddle)
	mux.HandleFunc("/api/trade/straddle/automated", handlers.ConfigBuild)
	mux.HandleFunc("/api/trade/straddle/custom", handlers.CustomSell)

	mux.HandleFunc("/api/straddles", handlers.GetStraddles)
	mux.HandleFunc("/api/straddles/active", handlers.GetActiveStraddles)
	mux.HandleFunc("/api/snapshots/", handlers.GetSnapshot)
	mux.HandleFunc("/api/pnl/", handlers.GetPnL)
	mux.HandleFunc("/api/orders/", handlers.GetOrders)
	mux.HandleFunc("/api/trade/execution-summary", handlers.ExecutionSummary)
	mux.HandleFunc("/api/trade/squareoff-plan", handlers.SquareOffPlan)

	mux.HandleFunc("/api/positions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "positions endpoint not implemented", http.StatusNotImplemented)
	})

	mux.HandleFunc("/api/trade/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/square-off"):
			handlers.SquareOff(w, r)
		case strings.HasSuffix(r.URL.Path, "/partial-square-off"):
			handlers.PartialSquareOff(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-hedge"):
			handlers.ManualHedge(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-roll"):
			handlers.ManualRoll(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-verify"):
			handlers.ManualVerify(w, r)
		case strings.HasSuffix(r.URL.Path, "/cancel-action"):
			handlers.CancelAction(w, r)
		default:
			handlers.TradeStatus(w, r)
		}
	})

	log.Printf("Control API listening on %s", cfg.ControlAPIPort)
	if err := http.ListenAndServe(cfg.ControlAPIPort, corsMiddleware(mux)); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

func waitForShutdown(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	log.Println("Shutting down Execution Gateway...")
	cancel()
}

func startZMQListener(ctx context.Context, cfg *Config, xtsClient *xts.Client) {
	socket, err := zmq.NewSocket(zmq.SUB)
	if err != nil {
		log.Fatalf("Failed to create ZMQ socket: %v", err)
	}
	defer socket.Close()

	if err := socket.Bind(cfg.ZMQEndpoint); err != nil {
		log.Fatalf("Failed to bind ZMQ to %s: %v", cfg.ZMQEndpoint, err)
	}
	if err := socket.SetSubscribe(""); err != nil {
		log.Fatalf("Failed to subscribe to ZMQ topic: %v", err)
	}

	log.Printf("Listening for OrderIntents on ZMQ %s", cfg.ZMQEndpoint)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := socket.Recv(0)
			if err != nil {
				continue
			}

			var intent broker.OrderIntent
			if err := json.Unmarshal([]byte(msg), &intent); err != nil {
				log.Printf("Failed to unmarshal OrderIntent: %v | raw: %s", err, msg)
				continue
			}

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
				intent.ClientID = cfg.XTSClientID
			}
			if intent.Side == "" {
				intent.Side = "SELL"
			}

			uid := "ZMQ_" + time.Now().Format("150405.000000")

			go func(i broker.OrderIntent, u string) {
				if err := interactive.PlaceOrder(xtsClient, i, u, i.LimitPrice); err != nil {
					log.Printf("ZMQ Execution Failed: %v", err)
				} else {
					log.Printf("ZMQ Order Sent: %s", u)
				}
			}(intent, uid)
		}
	}
}
