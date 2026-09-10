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

// IrisDiscoveryClient is a passive discovery client.
//
// It authenticates to Iris and reads websocket frames. It does not place,
// cancel, modify, or retry broker orders.
type IrisDiscoveryClient struct {
	conn *websocket.Conn
}

func (c *Client) NewIrisDiscoveryClient(
	ctx context.Context,
) (*IrisDiscoveryClient, error) {
	if c == nil {
		return nil, fmt.Errorf("greeksoft client is nil")
	}

	if c.Session == nil {
		return nil, fmt.Errorf("greeksoft session is nil; login first")
	}

	if c.Session.BrokerSpecific == nil {
		return nil, fmt.Errorf(
			"greeksoft broker-specific session data missing",
		)
	}

	gcid := brokerSpecificString(c.Session.BrokerSpecific, "gcid")
	sessionID := brokerSpecificString(
		c.Session.BrokerSpecific,
		"session_id",
	)
	irisIP := brokerSpecificString(
		c.Session.BrokerSpecific,
		"iris_ip",
	)
	irisPort := brokerSpecificString(
		c.Session.BrokerSpecific,
		"iris_port",
	)
	gscid := strings.TrimSpace(c.Session.UserID)

	switch {
	case gcid == "":
		return nil, fmt.Errorf("greeksoft websocket GCID missing")
	case sessionID == "":
		return nil, fmt.Errorf(
			"greeksoft websocket session ID missing",
		)
	case irisIP == "":
		return nil, fmt.Errorf("greeksoft Iris IP missing")
	case irisPort == "":
		return nil, fmt.Errorf("greeksoft Iris port missing")
	case gscid == "":
		return nil, fmt.Errorf("greeksoft GSCID missing")
	}

	wsURL, err := buildIrisWebSocketURL(irisIP, irisPort)
	if err != nil {
		return nil, err
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, response, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		status := ""
		if response != nil {
			status = response.Status
		}

		return nil, fmt.Errorf(
			"dial GreekSoft Iris websocket %s status=%s: %w",
			wsURL,
			status,
			err,
		)
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

	loginBytes, err := json.Marshal(loginRequest)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf(
			"marshal GreekSoft Iris login request: %w",
			err,
		)
	}

	if err := conn.WriteMessage(
		websocket.TextMessage,
		loginBytes,
	); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf(
			"send GreekSoft Iris login request: %w",
			err,
		)
	}

	log.Printf(
		"[GREEKSOFT IRIS] connected host=%s gscid=%s gcid=%s",
		wsURL,
		gscid,
		gcid,
	)

	return &IrisDiscoveryClient{
		conn: conn,
	}, nil
}

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

func (c *IrisDiscoveryClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
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

func buildIrisWebSocketURL(
	irisIP string,
	irisPort string,
) (string, error) {
	irisIP = strings.TrimSpace(irisIP)
	irisPort = strings.TrimSpace(irisPort)

	if irisIP == "" || irisPort == "" {
		return "", fmt.Errorf(
			"invalid GreekSoft Iris endpoint ip=%q port=%q",
			irisIP,
			irisPort,
		)
	}

	if strings.HasPrefix(irisIP, "ws://") ||
		strings.HasPrefix(irisIP, "wss://") {
		parsed, err := url.Parse(irisIP)
		if err != nil {
			return "", fmt.Errorf(
				"parse GreekSoft Iris URL: %w",
				err,
			)
		}

		if parsed.Port() == "" {
			parsed.Host = net.JoinHostPort(
				parsed.Hostname(),
				irisPort,
			)
		}

		return parsed.String(), nil
	}

	return "ws://" + net.JoinHostPort(irisIP, irisPort), nil
}
