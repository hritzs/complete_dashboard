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
	"github.com/nats-io/nats.go"
	zmq "github.com/pebbe/zmq4"

	gs "trading-platform/libs/broker-greeksoft"
	xts "trading-platform/libs/broker-xts"
	"trading-platform/libs/broker-xts/interactive"
	"trading-platform/libs/contracts"
	broker "trading-platform/libs/go-broker"
	"trading-platform/libs/go-common/events"

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
	installSplitLogs()
	log.Println("Starting Execution Gateway...")

	appConfig := LoadConfig()

	var store trading.Store = trading.NewMemoryStore()
	// Hoisted out of the if-block below (not just local to it) because the
	// GreekSoft executor factory further down also needs it, to share a
	// GreekSoft login with reconciler/greeksoft-feed-bridge via
	// broker_sessions instead of each independently authenticating -- see
	// libs/broker-greeksoft/shared_session.go.
	var sharedDB *sql.DB
	if dsn := strings.TrimSpace(os.Getenv("POSTGRES_DSN")); dsn != "" {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			log.Printf("[SQL STORE] open failed err=%v; using memory store", err)
		} else if err := db.Ping(); err != nil {
			log.Printf("[SQL STORE] ping failed err=%v; using memory store", err)
		} else {
			store = trading.NewPostgresBackedStore(db)
			sharedDB = db
			log.Printf("[SQL STORE] postgres-backed store enabled")
		}
	} else {
		log.Printf("[SQL STORE] POSTGRES_DSN empty; using memory store")
	}

	service := trading.NewService(store, appConfig.XTSClientID)
	service.Snapshot = &trading.SnapshotClient{BaseURL: appConfig.SnapshotServiceURL}
	// Platform state (rules, runs, LUT, paper, portfolio rule) also in
	// postgres; before the engines so a missing file is restored from it.
	service.StartStateDB()
	// Always-on LUT entry check: PAPER only (YES/NO), never places orders.
	service.StartLUTEngine()
	// Straddle-target build: live straddle preview + SHADOW builds (no orders).
	service.StartSBEngine()
	service.StartBrokerAutoSync()
	service.StartPortfolioMTM()

	natsURL := strings.TrimSpace(os.Getenv("NATS_URL"))
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	nc, natsErr := nats.Connect(
		natsURL,
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			service.OrderEvents.SetHealthy(false)
			log.Printf("[IRIS-NATS] disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			service.OrderEvents.SetHealthy(true)
			log.Printf("[IRIS-NATS] reconnected url=%s", conn.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			service.OrderEvents.SetHealthy(false)
			log.Printf("[IRIS-NATS] connection closed")
		}),
	)
	if natsErr != nil {
		log.Printf("[IRIS-NATS] connect failed: %v", natsErr)
	} else {
		defer nc.Close()
		_, subErr := events.Subscribe[contracts.OrderUpdate](nc, events.TopicOrderUpdates, func(update contracts.OrderUpdate) { service.OrderEvents.Publish(update) }, func(err error) { log.Printf("[IRIS-NATS] decode failed: %v", err) })
		if subErr != nil {
			log.Printf("[IRIS-NATS] subscribe failed: %v", subErr)
		} else if err := nc.Flush(); err != nil {
			log.Printf("[IRIS-NATS] flush failed: %v", err)
		} else {
			service.OrderEvents.SetHealthy(true)
			log.Printf("[IRIS-NATS] subscribed subject=%s health=ready", events.TopicOrderUpdates)
		}
	}

	// Resume only database-reconciled ACTIVE trades. A stale header without
	// persisted CE/PE legs, fills, contracts, and filled orders must never
	// restart a monitor or be treated as a live position after a restart.
	for _, tr := range store.AllTrades() {
		switch tr.Status {
		case "ACTIVE":
			if pgStore, ok := store.(*trading.PostgresBackedStore); ok {
				// Re-derive legs from fills first (one count per order):
				// legs written by the old raw-sum logic were double-counted,
				// and resuming from them ran a 1040/910 trade as 2080/1820.
				if err := pgStore.RecomputeTradeLegs(context.Background(), tr.TradeUID); err != nil {
					log.Printf("[BOOT] monitor blocked trade=%s reason=leg recompute failed: %v", tr.TradeUID, err)
					continue
				}
				eligibility, err := pgStore.ValidateTradeForRuntime(
					context.Background(),
					tr.TradeUID,
				)
				if err != nil {
					log.Printf(
						"[BOOT] monitor blocked trade=%s status=%s reason=%v",
						tr.TradeUID,
						tr.Status,
						err,
					)
					continue
				}

				// The runtime monitor and snapshot builder use CELtp/PELtp as
				// the open-leg entry prices (legacy field names). Never resume
				// from the pre-trade quote/limit values held in the stored header:
				// overwrite them with durable broker-fill reconciliation values.
				if (tr.CEQty != int(eligibility.CEQty) || tr.PEQty != int(eligibility.PEQty)) && tr.LotSize > 0 {
					// The exchange fills disagree with the stored size (e.g. two
					// builds filed under one trade): the position is what
					// filled -- size the trade (lots too: PnL per straddle, SL,
					// TP and the exit all use it) to the fills, and persist it.
					log.Printf("[BOOT] ⚠ trade=%s stored CE %d / PE %d (lots %d) but exchange fills say CE %d / PE %d -- trade resized to the fills",
						tr.TradeUID, tr.CEQty, tr.PEQty, tr.Lots, eligibility.CEQty, eligibility.PEQty)
					tr.CEQty = int(eligibility.CEQty)
					tr.PEQty = int(eligibility.PEQty)
					if lots := (tr.CEQty + tr.PEQty) / (2 * tr.LotSize); lots > 0 {
						tr.Lots = lots
					}
					service.Store.UpdateTrade(tr)
				}
				tr.CEQty = int(eligibility.CEQty)
				tr.PEQty = int(eligibility.PEQty)
				tr.CELtp = eligibility.CEEntry
				tr.PELtp = eligibility.PEEntry

				log.Printf(
					"[BOOT] resuming reconciled runtime trade=%s ce_qty=%d pe_qty=%d ce_entry=%.2f pe_entry=%.2f",
					eligibility.TradeUID,
					eligibility.CEQty,
					eligibility.PEQty,
					eligibility.CEEntry,
					eligibility.PEEntry,
				)
			} else {
				log.Printf(
					"[BOOT] monitor blocked trade=%s reason=postgres-backed store unavailable",
					tr.TradeUID,
				)
				continue
			}

			service.ResumeRuntime(tr)

		case "PARTIAL", "RECONCILIATION_REQUIRED":
			// Only TODAY's trades: an older RECONCILIATION_REQUIRED record
			// may no longer match the broker, and must never auto-start a
			// monitor that could place orders on it.
			if tr.Config.ExitHalted {
				log.Printf("[BOOT] monitor not resumed trade=%s status=%s reason=exit was halted on unconfirmed orders, needs manual review", tr.TradeUID, tr.Status)
				continue
			}
			if tr.Status == "RECONCILIATION_REQUIRED" && !trading.CreatedTodayIST(tr.CreatedAt) {
				log.Printf("[BOOT] monitor not resumed trade=%s status=RECONCILIATION_REQUIRED reason=not from today, needs manual review", tr.TradeUID)
				continue
			}
			// A build that stopped part-way (e.g. margin rejection) holds a
			// real position: track it as ACTIVE (partial) at what really
			// filled, from exchange fills, instead of leaving it unmonitored.
			if promoted, ok := service.PromotePartialTrade(context.Background(), tr); ok {
				log.Printf("[BOOT] resuming ACTIVE (partial) trade=%s ce_qty=%d pe_qty=%d", promoted.TradeUID, promoted.CEQty, promoted.PEQty)
				service.ResumeRuntime(promoted)
			} else {
				log.Printf("[BOOT] monitor NOT resumed trade=%s status=PARTIAL reason=filled legs need manual review (see [PARTIAL] line above)", tr.TradeUID)
			}

		case "BUILDING", "ROLLING", "SQUARING_OFF", "PARTIAL-SQF":
			log.Printf(
				"[BOOT] monitor not resumed trade=%s status=%s reason=non-active recovery requires reconciliation",
				tr.TradeUID,
				tr.Status,
			)
		}
	}
	service.LotSize = &trading.LotSizeClient{BaseURL: appConfig.ContractMasterURL}

	xtsClient := xts.NewClient()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	factory := trading.NewDefaultBrokerFactory()

	factory.Register(
		"XTS",
		func(userID string, accountID string) (trading.Executor, error) {
			return &xtsExecutor{client: xtsClient}, nil
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
			// GREEK_BROKER_ID is optional for live 147
			if strings.TrimSpace(appConfig.GreekPanDob) == "" {
				return nil, fmt.Errorf("GREEK_PAN_DOB is required")
			}

			gsClient := gs.NewClient(appConfig.GreekAuthURL, appConfig.GreekRestURL)

			loginCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// LoginShared reuses a session reconciler/greeksoft-feed-bridge
			// already established (via broker_sessions) when one is fresh
			// enough, instead of always logging in fresh -- each process
			// independently authenticating for the same account was
			// confirmed to invalidate the others' sessions. sharedDB may be
			// nil (no POSTGRES_DSN), in which case this always does a
			// normal fresh login, unchanged from before.
			session, err := gsClient.LoginShared(loginCtx, sharedDB, &broker.AccountConfig{
				Name:       "greeksoft-" + strings.ToLower(accountID),
				BrokerType: "greeksoft",
				APIKey:     accountID,
				APISecret:  appConfig.GreekPassword,
				ClientID:   accountID,
				PanDob:     appConfig.GreekPanDob,
			}, 10*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("greeksoft login failed for account %s: %w", accountID, err)
			}

			log.Printf("Greeksoft executor ready account=%s user_id=%s", accountID, session.UserID)

			executor := greeksoftbroker.NewExecutor(gsClient)

			// Fill verification tries the reconciler-persisted (Iris-sourced)
			// Postgres path first, falling back to REST order-book polling
			// automatically on any error -- see
			// internal/brokers/greeksoft/omsfeed.go and
			// Executor.GetVerifiedFills. This reads Postgres only; it does
			// NOT open a second Iris connection (an earlier version did,
			// and that collided with services/reconciler's own Iris
			// connection live on 2026-09-17 -- see docs/TODO.md Phase 10
			// for the full incident). Only wired up when sharedDB is
			// available; otherwise GetVerifiedFills silently stays
			// REST-only, unchanged from before.
			if sharedDB != nil {
				reconcilerHealthPort := strings.TrimSpace(os.Getenv("RECONCILER_HEALTH_PORT"))
				if reconcilerHealthPort == "" {
					reconcilerHealthPort = "8021"
				}
				reconcilerHealthURL := "http://localhost:" + reconcilerHealthPort + "/api/health"
				executor.OMSFeed = greeksoftbroker.NewOMSFeed(sharedDB, reconcilerHealthURL)
				log.Printf("Greeksoft executor account=%s: fill verification tries reconciler DB first, REST fallback (health=%s)", accountID, reconcilerHealthURL)
			}

			greekExecutors[accountID] = executor

			return executor, nil
		},
	)

	service.BrokerFactory = factory

	// Log in to GreekSoft now, not on the first order. The executor is
	// created lazily and cached, so the first order after every restart
	// used to pay the ~1s login inline -- confirmed live 2026-09-28: a
	// minute-end hedge decided at 10:18:00 only went out at 10:18:01.
	// Warms every GreekSoft account that has a trade, plus GREEK_ACCOUNT_ID.
	go func() {
		accounts := map[string]string{} // account -> user
		if acc := strings.TrimSpace(os.Getenv("GREEK_ACCOUNT_ID")); acc != "" {
			accounts[strings.ToUpper(acc)] = "U001"
		}
		for _, tr := range store.AllTrades() {
			if strings.EqualFold(strings.TrimSpace(tr.BrokerName), "GREEKSOFT") && strings.TrimSpace(tr.AccountID) != "" {
				accounts[strings.ToUpper(strings.TrimSpace(tr.AccountID))] = tr.UserID
			}
		}
		freezeLoaded := false
		for acc, user := range accounts {
			exec, err := factory.GetExecutor(user, "GREEKSOFT", acc)
			if err != nil {
				log.Printf("[BOOT] GreekSoft warm-up login failed account=%s (first order will retry): %v", acc, err)
				continue
			}
			log.Printf("[BOOT] GreekSoft executor warmed account=%s", acc)

			// Freeze qty is per underlying, not per account: load it once,
			// here at startup, so no order ever waits on the quote API.
			if !freezeLoaded {
				freezeLoaded = true
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				service.PreloadFreezeQty(ctx, exec, []string{"NIFTY", "BANKNIFTY", "FINNIFTY", "MIDCPNIFTY", "SENSEX", "BANKEX"})
				cancel()
			}
		}
	}()

	handlers := trading.NewHandlers(service, store)

	go startHTTPServer(appConfig, handlers)

	go waitForShutdown(cancel)
	go startZMQListener(ctx, appConfig, xtsClient)

	<-ctx.Done()
	log.Println("Execution Gateway stopped")
}

func startHTTPServer(cfg *Config, handlers *trading.Handlers) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", handlers.Health)
	mux.HandleFunc("/api/manual/order", handlers.ManualOrder)
	mux.HandleFunc("/api/manual/order/modify", handlers.ManualModifyOrder)

	mux.HandleFunc("/api/admin/reset-trading-data", handlers.ResetTradingData)
	mux.HandleFunc("/api/trade/straddle", handlers.DeployStraddle)
	mux.HandleFunc("/api/straddle/sell", handlers.DeployStraddle)
	mux.HandleFunc("/api/trade/straddle/automated", handlers.ConfigBuild)
	mux.HandleFunc("/api/trade/straddle/scheduled", handlers.ListScheduledBuilds)
	mux.HandleFunc("/api/trade/straddle/scheduled/cancel", handlers.CancelScheduledBuild)
	mux.HandleFunc("/api/trade/straddle/custom", handlers.CustomSell)
	// LUT build -- SIMULATION ONLY (never places orders).
	mux.HandleFunc("/api/lut/state", handlers.LUTStateHandler)
	mux.HandleFunc("/api/lut/config", handlers.LUTConfigHandler)
	mux.HandleFunc("/api/lut/start-now", handlers.LUTStartNowHandler)
	// Paper-trade simulation off the stored minute data -- never places orders.
	mux.HandleFunc("/api/paper/sim", handlers.PaperSimHandler)
	mux.HandleFunc("/api/paper/start", handlers.PaperStartHandler)
	mux.HandleFunc("/api/paper/remove", handlers.PaperRemoveHandler)
	// Straddle-target build, SHADOW mode (no orders).
	mux.HandleFunc("/api/sbuild/shadow/start", handlers.SBStart)
	mux.HandleFunc("/api/sbuild/shadow/stop", handlers.SBStop)
	mux.HandleFunc("/api/sbuild/shadow/state", handlers.SBStateHandler)
	mux.HandleFunc("/api/sbuild/rule", handlers.SBRuleHandler)
	mux.HandleFunc("/api/sbuild/rule/delete", handlers.SBRuleDelete)
	mux.HandleFunc("/api/sbuild/exit", handlers.SBExit)
	mux.HandleFunc("/api/sbuild/pause", handlers.SBPause)
	mux.HandleFunc("/api/build/stop", handlers.StopBuild)
	mux.HandleFunc("/api/trade/broker-sync", handlers.TradeBrokerSync)
	mux.HandleFunc("/api/lut/build", handlers.LUTBuildHandler)
	mux.HandleFunc("/api/lut/build/test", handlers.LUTBuildTestHandler)
	mux.HandleFunc("/api/trade/open-legs", handlers.TradeOpenLegs)
	mux.HandleFunc("/api/trade/manual-leg", handlers.ManualLegHandler)
	mux.HandleFunc("/api/sbuild/expiries", handlers.SBExpiries)
	mux.HandleFunc("/api/sbuild/quote", handlers.SBQuote)

	mux.HandleFunc("/api/portfolio/today", handlers.PortfolioToday)
	mux.HandleFunc("/api/portfolio/mtm-exit", handlers.PortfolioMTMHandler)
	mux.HandleFunc("/api/portfolio/square-off-all", handlers.PortfolioSquareOffAll)
	mux.HandleFunc("/api/straddles", handlers.GetStraddles)
	mux.HandleFunc("/api/straddles/active", handlers.GetActiveStraddles)
	mux.HandleFunc("/api/snapshots/", handlers.GetSnapshot)
	mux.HandleFunc("/api/pnl/", handlers.GetPnL)
	mux.HandleFunc("/api/orders/", handlers.GetOrders)

	mux.HandleFunc("/api/debug/greeksoft/fills", handlers.DebugGreeksoftFills)
	mux.HandleFunc("/api/admin/reconcile/greeksoft-fills", handlers.ReconcileGreeksoftFills)
	mux.HandleFunc("/api/debug/greeksoft/quote", handlers.DebugGreeksoftQuote)
	mux.HandleFunc("/api/debug/greeksoft/marketdata", handlers.DebugGreeksoftMarketData)
	mux.HandleFunc("/api/debug/trade/reconciliation", handlers.GetTradeReconciliation)
	mux.HandleFunc("/api/positions", handlers.BrokerPositions)
	mux.HandleFunc("/api/positions/check", handlers.PositionCheck)
	mux.HandleFunc("/api/trades", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")

		pgStore, ok := handlers.Store.(*trading.PostgresBackedStore)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "postgres-backed portfolio store is unavailable",
			})
			return
		}

		trades, err := pgStore.LoadActivePortfolio(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		_ = json.NewEncoder(w).Encode(trades)
	})

	mux.HandleFunc("/api/trade/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sync-preview"):
			handlers.TradeSyncPreview(w, r)
		case strings.HasSuffix(r.URL.Path, "/square-off"):
			handlers.SquareOff(w, r)
		case strings.HasSuffix(r.URL.Path, "/partial-square-off"):
			handlers.PartialSquareOff(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-test"):
			handlers.ManualHedgeTest(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-execute"):
			handlers.ManualHedgeExecute(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-hedge"):
			handlers.ManualHedge(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-roll"):
			handlers.ManualRoll(w, r)
		case strings.HasSuffix(r.URL.Path, "/manual-verify"):
			handlers.ManualVerify(w, r)
		case strings.HasSuffix(r.URL.Path, "/cancel-action"):
			handlers.CancelAction(w, r)
		case strings.HasSuffix(r.URL.Path, "/modify"):
			handlers.ModifyTrade(w, r)
		default:
			http.NotFound(w, r)
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
