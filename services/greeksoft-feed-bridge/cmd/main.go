// greeksoft-feed-bridge subscribes to GreekSoft's Apollo market-data
// websocket and republishes it to feed-decoder's ZMQ ingest -- but only
// when the platform's existing primary feed (direct exchange UDP
// multicast + the XTS ZMQ fallback feed-decoder already merges on
// tcp://127.0.0.1:5555) has gone stale. It runs as its own process so a
// slow Apollo reconnect can never add latency to the primary hot path.
//
// See docs/greeksoft-integration-architecture.md section 3 for the design.
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
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	zmq "github.com/pebbe/zmq4"

	greeksoft "trading-platform/libs/broker-greeksoft"
	broker "trading-platform/libs/go-broker"
)

// decodeApolloData unmarshals the "data" object out of a raw Apollo
// envelope frame ({"response":{...,"data":{...}}}) into target.
func decodeApolloData(raw []byte, target interface{}) error {
	var envelope struct {
		Response struct {
			Data json.RawMessage `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode apollo envelope: %w", err)
	}
	if len(envelope.Response.Data) == 0 {
		return fmt.Errorf("apollo frame has no data")
	}
	return json.Unmarshal(envelope.Response.Data, target)
}

func envOrDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func requiredEnv(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		log.Fatalf("required environment variable %s is empty", name)
	}
	return v
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

func envDurationSeconds(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		log.Printf("[BRIDGE] ignoring invalid %s=%q, using default %s", name, raw, def)
		return def
	}
	return time.Duration(secs) * time.Second
}

// primaryLivenessWatcher tracks how stale the platform's actual primary
// feed (direct exchange UDP multicast + the XTS ZMQ fallback) is, using
// feed-decoder's own reported primary_feed_age_ms -- NOT just "have I seen
// any traffic on a port," which was the previous (wrong) approach: it
// watched tcp://127.0.0.1:5555 (the XTS fallback ingest port), but nothing
// publishes there in this deployment (market-data-gateway isn't started
// by start_platform.sh), so it always read as stale even while the real
// primary UDP feed was flowing fine. feed-decoder's option-chain publisher
// on :5556 is timer-driven and republishes last-known values every 100ms
// regardless of upstream liveness, so raw traffic there isn't a valid
// signal either -- the age it now reports inside each message is.
type primaryLivenessWatcher struct {
	lastMessageUnixNano atomic.Int64
	lastReportedAgeMs   atomic.Int64 // -1 = feed-decoder running but has never seen a primary tick
	messageCount        atomic.Int64
}

func (w *primaryLivenessWatcher) staleSince(threshold time.Duration) (bool, time.Duration) {
	lastMsg := w.lastMessageUnixNano.Load()
	if lastMsg == 0 {
		// Never received a status message from feed-decoder at all
		// (it may not be running) -- treat as stale immediately.
		return true, 0
	}
	sinceLastMsg := time.Since(time.Unix(0, lastMsg))
	reportedAgeMs := w.lastReportedAgeMs.Load()
	if reportedAgeMs < 0 {
		return true, sinceLastMsg
	}
	totalAge := time.Duration(reportedAgeMs)*time.Millisecond + sinceLastMsg
	return totalAge > threshold, totalAge
}

func (w *primaryLivenessWatcher) run(ctx context.Context, endpoint string) error {
	sock, err := zmq.NewSocket(zmq.SUB)
	if err != nil {
		return fmt.Errorf("create primary-liveness SUB socket: %w", err)
	}
	defer sock.Close()

	if err := sock.Connect(endpoint); err != nil {
		return fmt.Errorf("connect primary-liveness SUB socket to %s: %w", endpoint, err)
	}
	if err := sock.SetSubscribe(""); err != nil {
		return fmt.Errorf("subscribe primary-liveness SUB socket: %w", err)
	}
	// Poll with a short timeout so ctx cancellation is checked regularly
	// instead of blocking forever in Recv.
	if err := sock.SetRcvtimeo(500 * time.Millisecond); err != nil {
		return fmt.Errorf("set primary-liveness recv timeout: %w", err)
	}

	log.Printf("[BRIDGE] watching primary feed liveness via feed-decoder's option-chain publish on %s", endpoint)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw, err := sock.RecvBytes(0)
		if err != nil {
			continue // EAGAIN on timeout, expected -- loop back to check ctx
		}

		var status struct {
			PrimaryFeedAgeMs *int64 `json:"primary_feed_age_ms"`
		}
		if jsonErr := json.Unmarshal(raw, &status); jsonErr != nil || status.PrimaryFeedAgeMs == nil {
			continue // older feed-decoder build without this field, or an unrelated message shape
		}

		w.lastMessageUnixNano.Store(time.Now().UnixNano())
		w.lastReportedAgeMs.Store(*status.PrimaryFeedAgeMs)
		w.messageCount.Add(1)
	}
}

type backupPublisher struct {
	sock           *zmq.Socket
	forwardedCount atomic.Int64
}

func newBackupPublisher(endpoint string) (*backupPublisher, error) {
	sock, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		return nil, fmt.Errorf("create backup PUB socket: %w", err)
	}
	if err := sock.Bind(endpoint); err != nil {
		_ = sock.Close()
		return nil, fmt.Errorf("bind backup PUB socket to %s: %w", endpoint, err)
	}
	log.Printf("[BRIDGE] backup feed publishing on %s", endpoint)
	return &backupPublisher{sock: sock}, nil
}

func (p *backupPublisher) publishInstrumentTick(exchangeInstrumentID string, lastTradedPrice string) {
	// feed-decoder's ZMQ-fallback JSON parser (message_parser.cpp
	// parse_json) does a plain substring search for these exact keys --
	// it is not a real JSON parser, so extra fields are harmless and key
	// order doesn't matter, but the key names/quoting must match exactly.
	payload := fmt.Sprintf(`{"ExchangeInstrumentID":%s,"LastTradedPrice":%s,"source":"greeksoft-apollo-backup"}`, exchangeInstrumentID, lastTradedPrice)
	if _, err := p.sock.SendBytes([]byte(payload), 0); err != nil {
		log.Printf("[BRIDGE] publish instrument tick failed: %v", err)
		return
	}
	p.forwardedCount.Add(1)
}

func (p *backupPublisher) publishIndexTick(indexName string, indexValue string) {
	payload := fmt.Sprintf(`{"IndexName":%q,"IndexValue":%s,"source":"greeksoft-apollo-backup"}`, indexName, indexValue)
	if _, err := p.sock.SendBytes([]byte(payload), 0); err != nil {
		log.Printf("[BRIDGE] publish index tick failed: %v", err)
		return
	}
	p.forwardedCount.Add(1)
}

func main() {
	authURL := requiredEnv("GREEK_API_AUTH_URL")
	restURL := requiredEnv("GREEK_API_REST_URL")
	password := requiredEnv("GREEK_PASSWORD")
	panDOB := requiredEnv("GREEK_PAN_DOB")
	accountID := resolveGreekAccountID()

	// GREEK_APOLLO_TOKENS is optional: Apollo pushes some data (e.g. the
	// "index" broadcast for Nifty 50/Nifty Bank -- confirmed live)
	// unsolicited, without any explicit subscription. Per-instrument
	// marketPicture ticks do need an explicit token subscription, so set
	// this once the specific tokens to back up are known.
	var tokens []string
	if raw := strings.TrimSpace(os.Getenv("GREEK_APOLLO_TOKENS")); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			tokens = append(tokens, strings.TrimSpace(t))
		}
	} else {
		log.Printf("[BRIDGE] GREEK_APOLLO_TOKENS not set -- only unsolicited broadcasts (e.g. index ticks) will be available as backup, not per-instrument marketPicture ticks")
	}

	// :5556 (feed-decoder's option-chain publish, carrying primary_feed_age_ms)
	// -- NOT :5555 (the XTS ZMQ fallback ingest port), which nothing
	// publishes to in this deployment and would always read as stale.
	primaryEndpoint := envOrDefault("PRIMARY_ZMQ_SUB_ENDPOINT", "tcp://127.0.0.1:5556")
	backupEndpoint := envOrDefault("BACKUP_ZMQ_PUB_ENDPOINT", "tcp://*:5560")
	stalenessThreshold := envDurationSeconds("STALENESS_THRESHOLD_SEC", 5*time.Second)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	healthPort := envOrDefault("BRIDGE_HEALTH_PORT", "8022")
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","service":"greeksoft-feed-bridge"}`))
		})
		if err := http.ListenAndServe(":"+healthPort, mux); err != nil {
			log.Printf("[BRIDGE] health server exited: %v", err)
		}
	}()

	// A DB connection is optional here (this bridge's core function doesn't
	// need Postgres) but enables session sharing with reconciler/
	// execution-gateway via broker_sessions -- without it, each process
	// independently logging into the same GreekSoft account was confirmed
	// to invalidate the others' sessions. Falls back to nil (always a
	// fresh, unshared login) if POSTGRES_DSN is unset or unreachable.
	var db *sql.DB
	if dsn := strings.TrimSpace(os.Getenv("POSTGRES_DSN")); dsn != "" {
		if conn, err := sql.Open("postgres", dsn); err != nil {
			log.Printf("[BRIDGE] postgres open failed, continuing without session sharing: %v", err)
		} else if err := conn.PingContext(ctx); err != nil {
			log.Printf("[BRIDGE] postgres ping failed, continuing without session sharing: %v", err)
			_ = conn.Close()
		} else {
			db = conn
			defer db.Close()
		}
	}

	client := greeksoft.NewClient(authURL, restURL)
	accCfg := &broker.AccountConfig{
		Name:       "greeksoft-feed-bridge-" + strings.ToLower(accountID),
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
	log.Printf("[BRIDGE] login successful account=%s user_id=%s", accountID, session.UserID)

	watcher := &primaryLivenessWatcher{}
	go func() {
		if err := watcher.run(ctx, primaryEndpoint); err != nil && ctx.Err() == nil {
			log.Printf("[BRIDGE] primary liveness watcher exited: %v", err)
		}
	}()

	publisher, err := newBackupPublisher(backupEndpoint)
	if err != nil {
		log.Fatalf("backup publisher setup failed: %v", err)
	}
	defer publisher.sock.Close()

	var apolloFrameCount atomic.Int64

	resubscribe := func(apollo *greeksoft.ApolloMarketDataClient) error {
		if len(tokens) == 0 {
			return nil
		}
		if err := apollo.Subscribe(tokens); err != nil {
			return err
		}
		log.Printf("[BRIDGE] subscribed Apollo tokens=%v", tokens)
		return nil
	}

	var loggedStaleTransition atomic.Bool

	handler := func(frame greeksoft.ApolloFrame) {
		apolloFrameCount.Add(1)

		stale, age := watcher.staleSince(stalenessThreshold)
		if !stale {
			loggedStaleTransition.Store(false)
			return // primary feed is fresh -- don't forward backup data
		}

		// Log the stale->forwarding transition once, not per-tick -- at
		// broadcast volume (observed: the same index tick can arrive
		// several times in quick succession) per-tick logging would flood
		// production logs. Ongoing volume is visible via the per-minute
		// health summary instead.
		if loggedStaleTransition.CompareAndSwap(false, true) {
			log.Printf("[BRIDGE] primary feed stale (age=%s) -- backup forwarding active", age)
		}

		switch frame.StreamingType {
		case greeksoft.StreamingTypeMarketPicture:
			var tick struct {
				Symbol string `json:"symbol"`
				LTP    string `json:"ltp"`
			}
			if err := decodeApolloData(frame.Raw, &tick); err != nil || tick.Symbol == "" || tick.LTP == "" {
				return
			}
			publisher.publishInstrumentTick(tick.Symbol, tick.LTP)
		case greeksoft.StreamingTypeIndex:
			var tick struct {
				Name string `json:"name"`
				LTP  string `json:"ltp"`
			}
			if err := decodeApolloData(frame.Raw, &tick); err != nil || tick.Name == "" || tick.LTP == "" {
				return
			}
			publisher.publishIndexTick(tick.Name, tick.LTP)
		}
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stale, age := watcher.staleSince(stalenessThreshold)
				log.Printf(
					"[BRIDGE] health: primary_status_msgs=%d apollo_frames=%d forwarded=%d primary_stale=%v primary_age=%s",
					watcher.messageCount.Load(), apolloFrameCount.Load(), publisher.forwardedCount.Load(), stale, age,
				)
			}
		}
	}()

	log.Printf("[BRIDGE] running: staleness_threshold=%s primary=%s backup_pub=%s tokens=%v", stalenessThreshold, primaryEndpoint, backupEndpoint, tokens)

	err = client.ApolloReadLoopWithReconnect(ctx, db, accCfg, resubscribe, handler)
	if err != nil && ctx.Err() == nil {
		log.Fatalf("Apollo read loop exited unexpectedly: %v", err)
	}
	log.Printf("[BRIDGE] shutting down")
}
