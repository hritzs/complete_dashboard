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

// sharedSessionMaxAge bounds how old a session reused from broker_sessions
// is trusted to still be valid, for LoginShared callers. GreekSoft's own
// session token is requested with ValidFor "30d" (see auth.go's
// sessionTokenRequest), but jloginNew's underlying session/gcid pairing may
// not last anywhere near that long in practice, so this is deliberately
// conservative rather than trusting the full 30-day window.
const sharedSessionMaxAge = 10 * time.Minute

// IrisDiscoveryClient is a passive discovery client.
//
// It authenticates to Iris and reads websocket frames. It does not place,
// cancel, modify, or retry broker orders.
type IrisDiscoveryClient struct {
	mu              sync.Mutex
	conn            *websocket.Conn
	heartbeatCancel context.CancelFunc
	heartbeatDone   chan struct{}
	gscid           string
	gcid            string
	sessionID       string
	heartbeatIntvl  time.Duration
}

func (c *Client) dialAndLoginIris(ctx context.Context) (*websocket.Conn, string, string, string, error) {
	if c.Session == nil || c.Session.BrokerSpecific == nil {
		return nil, "", "", "", fmt.Errorf("greeksoft session is nil; login first")
	}

	gcid := brokerSpecificString(c.Session.BrokerSpecific, "gcid")
	sessionID := brokerSpecificString(c.Session.BrokerSpecific, "session_id")
	irisIP := brokerSpecificString(c.Session.BrokerSpecific, "iris_ip")
	irisPort := brokerSpecificString(c.Session.BrokerSpecific, "iris_port")
	gscid := strings.TrimSpace(c.Session.UserID)

	switch {
	case gcid == "":
		return nil, "", "", "", fmt.Errorf("greeksoft websocket GCID missing")
	case sessionID == "":
		return nil, "", "", "", fmt.Errorf("greeksoft websocket session ID missing")
	case irisIP == "":
		return nil, "", "", "", fmt.Errorf("greeksoft Iris IP missing")
	case irisPort == "":
		return nil, "", "", "", fmt.Errorf("greeksoft Iris port missing")
	case gscid == "":
		return nil, "", "", "", fmt.Errorf("greeksoft GSCID missing")
	}

	wsURL, err := buildGreekSoftWebSocketURL(irisIP, irisPort)
	if err != nil {
		return nil, "", "", "", err
	}

	conn, err := dialGreekSoftWebSocket(ctx, wsURL)
	if err != nil {
		return nil, "", "", "", err
	}

	loginRequest := IrisLoginRequest{
		Request: IrisLoginRequestEnvelope{
			Data: IrisLoginData{
				GSCID:      gscid,
				GCID:       gcid,
				SessionID:  sessionID,
				DeviceType: "0",
			},
			ResponseFormat: "json",
			RequestType:    "subscribe",
			StreamingType:  "login",
		},
	}

	if err := sendJSONFrame(conn, loginRequest); err != nil {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("send GreekSoft Iris login request: %w", err)
	}

	// Validate the login ack instead of firing-and-forgetting: read the
	// first frame and require it to be a successful LoginResponse before
	// treating the connection as usable.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("set GreekSoft Iris login-ack deadline: %w", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("read GreekSoft Iris login ack: %w", err)
	}
	ack := classifyIrisFrame(raw)
	if !strings.EqualFold(ack.StreamingType, StreamingTypeLoginResponse) {
		_ = conn.Close()
		return nil, "", "", "", fmt.Errorf("greeksoft Iris login not acknowledged: streaming_type=%s raw=%s", ack.StreamingType, string(raw))
	}
	_ = conn.SetReadDeadline(time.Time{})

	log.Printf("[GREEKSOFT IRIS] connected+logged-in host=%s gscid=%s gcid=%s", wsURL, gscid, gcid)

	return conn, gscid, gcid, sessionID, nil
}

// NewIrisDiscoveryClient dials Iris, logs in, and validates the login ack.
// It also starts the mandatory heartbeat loop GreekSoft's websocket docs
// require (a HeartBeat frame every heartbeat_Intervals seconds, from
// getFlagValues) -- a connection that doesn't heartbeat is expected to be
// dropped by the broker. Use ReadLoopWithReconnect for a long-lived
// consumer; use ReadLoop directly only for a short, bounded probe like
// cmd/wsprobe.
func (c *Client) NewIrisDiscoveryClient(ctx context.Context) (*IrisDiscoveryClient, error) {
	if c == nil {
		return nil, fmt.Errorf("greeksoft client is nil")
	}

	conn, gscid, gcid, sessionID, err := c.dialAndLoginIris(ctx)
	if err != nil {
		return nil, err
	}

	client := &IrisDiscoveryClient{
		conn:           conn,
		gscid:          gscid,
		gcid:           gcid,
		sessionID:      sessionID,
		heartbeatIntvl: heartbeatIntervalFromSession(c.Session.BrokerSpecific),
	}
	client.startHeartbeat()

	return client, nil
}

func (c *IrisDiscoveryClient) startHeartbeat() {
	hbCtx, cancel := context.WithCancel(context.Background())
	c.heartbeatCancel = cancel
	c.heartbeatDone = make(chan struct{})

	go func() {
		defer close(c.heartbeatDone)
		runHeartbeatLoop(hbCtx, c.conn, c.heartbeatIntvl, "IRIS", func() interface{} {
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

// ReadLoop reads frames from the current connection until ctx is done or
// the connection breaks. It does not reconnect -- use
// ReadLoopWithReconnect for a long-lived consumer (e.g. the reconciler).
func (c *IrisDiscoveryClient) ReadLoop(
	ctx context.Context,
	handler func(IrisFrame),
) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("GreekSoft Iris websocket is not connected")
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if deadline, ok := ctx.Deadline(); ok {
			if err := c.conn.SetReadDeadline(deadline); err != nil {
				return fmt.Errorf(
					"set GreekSoft Iris read deadline: %w",
					err,
				)
			}
		}

		messageType, raw, err := c.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			return fmt.Errorf(
				"read GreekSoft Iris websocket: %w",
				err,
			)
		}

		if messageType != websocket.TextMessage &&
			messageType != websocket.BinaryMessage {
			continue
		}

		frame := classifyIrisFrame(raw)

		log.Printf(
			"[GREEKSOFT IRIS RX] streaming_type=%s service=%s raw=%s",
			frame.StreamingType,
			frame.ServiceName,
			string(frame.Raw),
		)

		if handler != nil {
			handler(frame)
		}
	}
}

// ReadLoopWithReconnect runs ReadLoop and, on any connection failure
// (other than ctx cancellation), re-authenticates and reconnects with
// exponential backoff, restarting the heartbeat each time, until ctx is
// done. This is what a long-lived consumer (the reconciler) should use
// instead of ReadLoop directly.
//
// db enables session sharing via broker_sessions (see shared_session.go):
// re-authenticating reuses a still-fresh session another process already
// established instead of always performing a brand new login, which
// itself invalidates every other process's session. Pass nil to always do
// a fresh PerformFullLogin (no sharing) -- e.g. for cmd/wsprobe, a
// short-lived probe with no DB access.
//
// Re-authenticating on every reconnect (not just redialing with the
// previous session) matters: GreekSoft appears to allow only one valid
// session per account, so any other process logging into the same
// account (e.g. execution-gateway placing an order) invalidates this
// session -- the server then closes the Iris connection with a graceful
// "close 1000 (normal)", not a network error. Confirmed live: with only a
// redial-and-reuse-the-old-session retry (no re-login), the reconnect
// loop span forever hitting the same immediate close, and only a full
// process restart (which re-logs in) recovered it. accCfg must be the
// same config originally passed to PerformFullLogin.
func (c *Client) ReadLoopWithReconnect(ctx context.Context, db *sql.DB, accCfg *broker.AccountConfig, handler func(IrisFrame)) error {
	connect := func() (*IrisDiscoveryClient, error) {
		if _, err := c.LoginShared(ctx, db, accCfg, sharedSessionMaxAge); err != nil {
			return nil, fmt.Errorf("re-authenticate before Iris connect: %w", err)
		}
		return c.NewIrisDiscoveryClient(ctx)
	}

	iris, err := connect()
	if err != nil {
		return fmt.Errorf("initial GreekSoft Iris connect failed: %w", err)
	}

	backoff := time.Duration(0)
	for {
		err := iris.ReadLoop(ctx, handler)
		_ = iris.Close()

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// The session that just failed might still look "fresh" by age to
		// the next reader of broker_sessions -- mark it invalidated so
		// nobody (including this process's own next attempt) reuses a
		// session already known to be dead.
		InvalidateShared(ctx, db, accCfg.ClientID)

		backoff = nextBackoff(backoff)
		log.Printf("[GREEKSOFT IRIS] connection lost (%v); re-authenticating and reconnecting in %s", err, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		iris, err = connect()
		if err != nil {
			log.Printf("[GREEKSOFT IRIS] reconnect attempt failed: %v", err)
			iris = nil
			continue
		}
	}
}

func (c *IrisDiscoveryClient) Close() error {
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
		websocket.FormatCloseMessage(
			websocket.CloseNormalClosure,
			"discovery probe complete",
		),
		time.Now().Add(time.Second),
	)

	closeErr := c.conn.Close()
	c.conn = nil

	if err != nil {
		return err
	}

	return closeErr
}
