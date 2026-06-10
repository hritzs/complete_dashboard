package socketio

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"trading-platform/services/market-data-gateway/internal/publisher"

	"github.com/gorilla/websocket"
)

type XTSClient struct {
	baseURL string
	token   string
	userID  string
	pub     *publisher.Publisher
	conn    *websocket.Conn
	mu      sync.Mutex
}

func NewXTSClient(baseURL, token, userID string, pub *publisher.Publisher) (*XTSClient, error) {
	return &XTSClient{
		baseURL: baseURL,
		token:   token,
		userID:  userID,
		pub:     pub,
	}, nil
}

func (c *XTSClient) Connect() error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}

	// Match python-socketio v5 perfectly by using Engine.IO v4
	connectURL := fmt.Sprintf("%s://%s/apimarketdata/socket.io/?EIO=4&transport=websocket&token=%s&userID=%s&publishFormat=JSON&broadcastMode=Full",
		scheme, u.Host, c.token, c.userID)

	slog.Info("🌐 Dialing XTS Socket.IO (EIO=4)...", "host", u.Host)

	conn, _, err := websocket.DefaultDialer.Dial(connectURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial error: %w", err)
	}
	c.conn = conn
	slog.Info("✅ Connected to XTS Socket.IO successfully!")

	go c.readPump()

	return nil
}

func (c *XTSClient) sendRaw(msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	}
	return fmt.Errorf("connection not active")
}

func (c *XTSClient) readPump() {
	defer c.conn.Close()
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			slog.Error("❌ WebSocket read error", "error", err)
			break
		}

		strMsg := string(message)

		if strings.HasPrefix(strMsg, "2") {
			// Server sent Ping ("2"), we MUST reply with Pong ("3") + payload
			c.sendRaw("3")
			continue
		} else if strings.HasPrefix(strMsg, "3") {
			// Server pong ("3"), keep connection alive!
			continue
		}

		// Engine.IO protocol parsing
		if strings.HasPrefix(strMsg, "0") {
			slog.Info("🟢 Engine.IO Session Established. Sending Socket.IO Handshake...")
			c.sendRaw("40")
		} else if strings.HasPrefix(strMsg, "40") {
			slog.Info("✅ Socket.IO Namespace Connected! Ready for Subscriptions.")
		} else if strings.HasPrefix(strMsg, "42") {
			// Socket.IO event: 42["event_name", data]

			// 🔥 ZERO ALLOCATION RAW FORWARDING 🔥
			// Bypassing massive json.Unmarshal allocations to prevent OOM and blocking
			if strings.Contains(strMsg, "1512-json") || strings.Contains(strMsg, "1510-json") || strings.Contains(strMsg, "1502-json") {
				idx := strings.Index(strMsg, ",")
				if idx != -1 {
					dataPart := strings.TrimSpace(strMsg[idx+1 : len(strMsg)-1])

					// If XTS stringifies the JSON payload, unquote it
					if strings.HasPrefix(dataPart, `"`) && strings.HasSuffix(dataPart, `"`) {
						if unquoted, err := strconv.Unquote(dataPart); err == nil {
							c.pub.Publish([]byte(unquoted))
							continue
						}
					}
					c.pub.Publish([]byte(dataPart))
				}
			} else if strings.Contains(strMsg, `"joined"`) {
				slog.Info("✅ XTS Confirmed Subscription")
			} else if strings.Contains(strMsg, `"error"`) {
				slog.Error("❌ XTS Socket Error", "details", strMsg)
			}
		}
	}
}

func (c *XTSClient) Subscribe(instruments []map[string]interface{}, msgCode int) error {
	// 🔥 Prevent XTS Error 1005 Disconnects: Block unsupported Cash Index streams
	if msgCode == 1510 || msgCode == 1502 || msgCode == 1501 {
		slog.Warn("⚠️ Suppressed restricted Cash Index subscription to keep Socket.IO alive", "msgCode", msgCode)
		return nil
	}

	payload := map[string]interface{}{"instruments": instruments, "xtsMessageCode": msgCode}
	eventBytes, _ := json.Marshal([]interface{}{"join", payload})
	msg := "42" + string(eventBytes) // '4' is Engine.IO Message, '2' is Socket.IO Event
	slog.Info("📡 Sending Subscription Request...", "msgCode", msgCode, "count", len(instruments))
	return c.sendRaw(msg)
}
