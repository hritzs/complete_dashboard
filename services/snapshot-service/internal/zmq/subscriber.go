package zmq

import (
	"encoding/json"
	"log/slog"

	"trading-platform/services/snapshot-service/internal/websocket"

	zmq "github.com/pebbe/zmq4"
)

// Subscriber listens to the C++ Feed Decoder and forwards to WebSockets
type Subscriber struct {
	socket *zmq.Socket
	ws     *websocket.Server
}

// NewSubscriber creates a new ZMQ SUB socket
func NewSubscriber(endpoint string, ws *websocket.Server) (*Subscriber, error) {
	zctx, _ := zmq.NewContext()
	socket, err := zctx.NewSocket(zmq.SUB)
	if err != nil {
		return nil, err
	}

	err = socket.Connect(endpoint)
	if err != nil {
		return nil, err
	}

	// Subscribe to all incoming topics from C++
	socket.SetSubscribe("")
	slog.Info("Snapshot Service ZMQ Subscriber connected to C++ Feed", "endpoint", endpoint)

	return &Subscriber{
		socket: socket,
		ws:     ws,
	}, nil
}

// Listen continuously pulls ZMQ messages and pushes them to the UI
func (s *Subscriber) Listen() {
	for {
		msg, err := s.socket.Recv(0)
		if err != nil {
			slog.Error("Failed to receive ZMQ message", "error", err)
			continue
		}

		// Parse the raw C++ JSON
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(msg), &payload); err == nil {
			// The UI expects the strikes array in the "data" field.
			// If C++ sends it as "chain", map it over to "data".
			if chainData, ok := payload["chain"]; ok {
				payload["data"] = chainData
			}

			// Inject the type identifier for the UI router
			payload["type"] = "option_chain"

			s.ws.Broadcast <- payload
		}
	}
}
