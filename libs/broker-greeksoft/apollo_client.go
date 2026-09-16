package greeksoft

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	broker "trading-platform/libs/go-broker"
)

// ApolloMarketDataClient is GreekSoft's market-data websocket counterpart
// to IrisDiscoveryClient. It authenticates, sends the mandatory heartbeat,
// and lets a caller subscribe/unsubscribe symbols for "marketPicture"
// broadcasts. Unlike Iris it is meant to be a real consumer (not a
// passive probe) -- it's intended for use as a GreekSoft-backed backup to
// the platform's primary market-data feed, activated only when that
// primary feed goes stale (see docs/greeksoft-integration-architecture.md
// section 3).
type ApolloMarketDataClient struct {
	mu              sync.Mutex
	conn            *websocket.Conn
	heartbeatCancel context.CancelFunc
	heartbeatDone   chan struct{}
	gscid           string
	gcid            string
	sessionID       string
	heartbeatIntvl  time.Duration
}

func (c *Client) dialAndLoginApollo(ctx context.Context) (*websocket.Conn, string, string, string, error) {
	if c.Session == nil || c.Session.BrokerSpecific == nil {
		return nil, "", "", "", fmt.Errorf("greeksoft session is nil; login first")
	}

	gcid := brokerSpecificString(c.Session.BrokerSpecific, "gcid")
	sessionID := brokerSpecificString(c.Session.BrokerSpecific, "session_id")
	apolloIP := brokerSpecificString(c.Session.BrokerSpecific, "apollo_ip")
	apolloPort := brokerSpecificString(c.Session.BrokerSpecific, "apollo_port")
	gscid := strings.TrimSpace(c.Session.UserID)

	switch {
	case gcid == "":
		return nil, "", "", "", fmt.Errorf("greeksoft websocket GCID missing")
	case sessionID == "":
		return nil, "", "", "", fmt.Errorf("greeksoft websocket session ID missing")
	case apolloIP == "":
		return nil, "", "", "", fmt.Errorf("greeksoft Apollo IP missing")
	case apolloPort == "":
		return nil, "", "", "", fmt.Errorf("greeksoft Apollo port missing")
	case gscid == "":
		return nil, "", "", "", fmt.Errorf("greeksoft GSCID missing")
	}

	wsURL, err := buildGreekSoftWebSocketURL(apolloIP, apolloPort)
	if err != nil {
		return nil, "", "", "", err
	}

	conn, err := dialGreekSoftWebSocket(ctx, wsURL)
	if err != nil {
		return nil, "", "", "", err
	}

	loginRequest := ApolloLoginRequest{
		Request: ApolloLoginRequestEnvelope{
			Data: ApolloLoginData{
				GSCID:      gscid,
				GCID:       gcid,
				SessionID:  sessionID,
				DeviceType: "0",
			},
			ResponseFormat: "json",
			RequestType:    "subscribe",
			StreamingType:  StreamingTypeLogin,
		},
	}

	if err := sendJSONFrame(conn, loginRequest); err != nil {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("send GreekSoft Apollo login request: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("set GreekSoft Apollo login-ack deadline: %w", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("read GreekSoft Apollo login ack: %w", err)
	}
	ack := classifyApolloFrame(raw)
	if !strings.EqualFold(ack.StreamingType, StreamingTypeLoginResponse) {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("greeksoft Apollo login not acknowledged: streaming_type=%s raw=%s", ack.StreamingType, string(raw))
	}
	_ = conn.SetReadDeadline(time.Time{})

	log.Printf("[GREEKSOFT APOLLO] connected+logged-in host=%s gscid=%s gcid=%s", wsURL, gscid, gcid)

	return conn, gscid, gcid, sessionID, nil
}

// NewApolloMarketDataClient dials Apollo, logs in, validates the login
// ack, and starts the mandatory heartbeat loop. Use
// ReadLoopWithReconnect for a long-lived consumer.
func (c *Client) NewApolloMarketDataClient(ctx context.Context) (*ApolloMarketDataClient, error) {
	if c == nil {
		return nil, fmt.Errorf("greeksoft client is nil")
	}

	conn, gscid, gcid, sessionID, err := c.dialAndLoginApollo(ctx)
	if err != nil {
		return nil, err
	}

	client := &ApolloMarketDataClient{
		conn:           conn,
		gscid:          gscid,
		gcid:           gcid,
		sessionID:      sessionID,
		heartbeatIntvl: heartbeatIntervalFromSession(c.Session.BrokerSpecific),
	}
	client.startHeartbeat()

	return client, nil
}

func (c *ApolloMarketDataClient) startHeartbeat() {
	hbCtx, cancel := context.WithCancel(context.Background())
	c.heartbeatCancel = cancel
	c.heartbeatDone = make(chan struct{})

	go func() {
		defer close(c.heartbeatDone)
		runHeartbeatLoop(hbCtx, c.conn, c.heartbeatIntvl, "APOLLO", func() interface{} {
			return IrisHeartbeatRequest{
				Request: IrisHeartbeatRequestEnvelope{
					Data:           IrisHeartbeatData{GCID: c.gcid, SessionID: c.sessionID},
					ResponseFormat: "json",
					RequestType:    "subscribe",
					StreamingType:  StreamingTypeHeartBeat,
				},
			}
		})
	}()
}

// Subscribe requests marketPicture broadcasts for the given instrument
// tokens (GreekSoft's numeric "symbol" tokens, as strings).
func (c *ApolloMarketDataClient) Subscribe(tokens []string) error {
	return c.sendSubscription(tokens, "subscribe")
}

// Unsubscribe stops marketPicture broadcasts for the given tokens.
func (c *ApolloMarketDataClient) Unsubscribe(tokens []string) error {
	return c.sendSubscription(tokens, "unsubscribe")
}

func (c *ApolloMarketDataClient) sendSubscription(tokens []string, requestType string) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("GreekSoft Apollo websocket is not connected")
	}

	symbols := make([]ApolloSubscribeSymbol, 0, len(tokens))
	for _, t := range tokens {
		symbols = append(symbols, ApolloSubscribeSymbol{Symbol: t})
	}

	req := ApolloSubscribeRequest{
		Request: ApolloSubscribeRequestEnvelope{
			Data:           ApolloSubscribeData{Symbols: symbols},
			ResponseFormat: "json",
			Gscid:          c.gscid,
			Gcid:           c.gcid,
			RequestType:    requestType,
			// GreekSoft's own websocket docs show this streaming_type with a
			// leading space (" marketPicture"); trimmed here as the sane
			// interpretation, but not yet confirmed against a live capture --
			// if subscriptions silently don't take, check this first.
			StreamingType: StreamingTypeMarketPicture,
		},
	}

	return sendJSONFrame(c.conn, req)
}

// ReadLoop reads frames from the current connection until ctx is done or
// the connection breaks. Does not reconnect -- use ReadLoopWithReconnect
// for a long-lived consumer.
func (c *ApolloMarketDataClient) ReadLoop(ctx context.Context, handler func(ApolloFrame)) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("GreekSoft Apollo websocket is not connected")
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if deadline, ok := ctx.Deadline(); ok {
			if err := c.conn.SetReadDeadline(deadline); err != nil {
				return fmt.Errorf("set GreekSoft Apollo read deadline: %w", err)
			}
		}

		messageType, raw, err := c.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read GreekSoft Apollo websocket: %w", err)
		}

		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}

		frame := classifyApolloFrame(raw)
		if handler != nil {
			handler(frame)
		}
	}
}

// ReadLoopWithReconnect runs ReadLoop and, on any connection failure
// (other than ctx cancellation), redials/re-logs-in/re-subscribes with
// exponential backoff until ctx is done. resubscribe is called after
// every successful (re)connect, including the first, so the caller's
// active token list is always requested again after a reconnect.
// accCfg must be the same config originally passed to PerformFullLogin --
// see wsclient.go's ReadLoopWithReconnect doc comment for why
// re-authenticating on every reconnect (not just redialing with the
// previous session) matters: another process logging into the same
// GreekSoft account invalidates this one's session, and only a fresh
// login recovers from that. db enables session sharing (nil to always do
// a fresh, unshared login) -- see shared_session.go.
func (c *Client) ApolloReadLoopWithReconnect(ctx context.Context, db *sql.DB, accCfg *broker.AccountConfig, resubscribe func(*ApolloMarketDataClient) error, handler func(ApolloFrame)) error {
	connectOnce := func() (*ApolloMarketDataClient, error) {
		if _, err := c.LoginShared(ctx, db, accCfg, sharedSessionMaxAge); err != nil {
			return nil, fmt.Errorf("re-authenticate before Apollo connect: %w", err)
		}
		client, err := c.NewApolloMarketDataClient(ctx)
		if err != nil {
			return nil, err
		}
		if resubscribe != nil {
			if err := resubscribe(client); err != nil {
				_ = client.Close()
				return nil, fmt.Errorf("resubscribe after connect: %w", err)
			}
		}
		return client, nil
	}

	apollo, err := connectOnce()
	if err != nil {
		return fmt.Errorf("initial GreekSoft Apollo connect failed: %w", err)
	}

	backoff := time.Duration(0)
	for {
		err := apollo.ReadLoop(ctx, handler)
		_ = apollo.Close()

		if ctx.Err() != nil {
			return ctx.Err()
		}

		InvalidateShared(ctx, db, accCfg.ClientID)

		backoff = nextBackoff(backoff)
		log.Printf("[GREEKSOFT APOLLO] connection lost (%v); reconnecting in %s", err, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		apollo, err = connectOnce()
		if err != nil {
			log.Printf("[GREEKSOFT APOLLO] reconnect attempt failed: %v", err)
			continue
		}
	}
}

func (c *ApolloMarketDataClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heartbeatCancel != nil {
		c.heartbeatCancel()
		<-c.heartbeatDone
	}

	err := c.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "apollo client closing"),
		time.Now().Add(time.Second),
	)

	closeErr := c.conn.Close()
	c.conn = nil

	if err != nil {
		return err
	}
	return closeErr
}
