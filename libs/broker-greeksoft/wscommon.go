package greeksoft

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// defaultHeartbeatIntervalSec is used when getFlagValues didn't report
// heartbeat_Intervals (e.g. it soft-failed during login). GreekSoft's docs
// show an observed value of 10 seconds; this default is intentionally the
// same order of magnitude, not a guess pulled from nowhere.
const defaultHeartbeatIntervalSec = 10

// defaultReconnectBackoff/MaxReconnectBackoff bound the redial loop shared
// by the Iris and Apollo streaming clients.
const (
	defaultReconnectBackoff    = 1 * time.Second
	defaultMaxReconnectBackoff = 30 * time.Second
)

func dialGreekSoftWebSocket(ctx context.Context, wsURL string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, response, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		status := ""
		if response != nil {
			status = response.Status
		}
		return nil, fmt.Errorf("dial GreekSoft websocket %s status=%s: %w", wsURL, status, err)
	}
	return conn, nil
}

func sendJSONFrame(conn *websocket.Conn, v interface{}) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal websocket frame: %w", err)
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

// heartbeatIntervalFromSession reads heartbeat_interval_sec stashed by
// PerformFullLogin (see auth.go), falling back to defaultHeartbeatIntervalSec
// if getFlagValues didn't report one.
func heartbeatIntervalFromSession(brokerSpecific map[string]interface{}) time.Duration {
	if brokerSpecific != nil {
		if raw, ok := brokerSpecific["heartbeat_interval_sec"]; ok {
			switch v := raw.(type) {
			case int:
				if v > 0 {
					return time.Duration(v) * time.Second
				}
			case int64:
				if v > 0 {
					return time.Duration(v) * time.Second
				}
			}
		}
	}
	return defaultHeartbeatIntervalSec * time.Second
}

// runHeartbeatLoop sends buildFrame() on conn every interval until ctx is
// done or a write fails. It's meant to run in its own goroutine alongside
// a ReadLoop on the same connection; a write failure here means the
// connection is dead, which the concurrent ReadLoop will also observe on
// its next read and use to trigger a reconnect.
func runHeartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration, label string, buildFrame func() interface{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendJSONFrame(conn, buildFrame()); err != nil {
				log.Printf("[GREEKSOFT %s] heartbeat send failed: %v", label, err)
				return
			}
		}
	}
}

// nextBackoff doubles cur (starting from defaultReconnectBackoff) up to
// defaultMaxReconnectBackoff.
func nextBackoff(cur time.Duration) time.Duration {
	if cur <= 0 {
		return defaultReconnectBackoff
	}
	next := cur * 2
	if next > defaultMaxReconnectBackoff {
		return defaultMaxReconnectBackoff
	}
	return next
}

func brokerSpecificString(
	values map[string]interface{},
	key string,
) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func buildGreekSoftWebSocketURL(ip string, port string) (string, error) {
	ip = strings.TrimSpace(ip)
	port = strings.TrimSpace(port)

	if ip == "" || port == "" {
		return "", fmt.Errorf("invalid GreekSoft websocket endpoint ip=%q port=%q", ip, port)
	}

	if strings.HasPrefix(ip, "ws://") || strings.HasPrefix(ip, "wss://") {
		parsed, err := url.Parse(ip)
		if err != nil {
			return "", fmt.Errorf("parse GreekSoft websocket URL: %w", err)
		}
		if parsed.Port() == "" {
			parsed.Host = net.JoinHostPort(parsed.Hostname(), port)
		}
		return parsed.String(), nil
	}

	return "ws://" + net.JoinHostPort(ip, port), nil
}
