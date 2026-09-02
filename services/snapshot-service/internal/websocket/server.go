package websocket

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for MVP UI
	},
}

// Server handles WebSocket broadcasts to UI clients
type Server struct {
	clients   map[*websocket.Conn]bool
	clientsMu sync.Mutex
	Broadcast chan interface{}
}

// NewServer initializes the WebSocket broadcast server
func NewServer() *Server {
	return &Server{
		clients:   make(map[*websocket.Conn]bool),
		Broadcast: make(chan interface{}),
	}
}

// HandleConnections upgrades HTTP requests to WebSocket and registers the client
func (s *Server) HandleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade connection", "error", err)
		return
	}

	s.clientsMu.Lock()
	s.clients[ws] = true
	s.clientsMu.Unlock()

	slog.Info("New WebSocket UI client connected", "addr", ws.RemoteAddr().String())

	// Read loop to detect client disconnects
	go func() {
		defer func() {
			s.clientsMu.Lock()
			delete(s.clients, ws)
			s.clientsMu.Unlock()
			ws.Close()
			slog.Info("WebSocket UI client disconnected")
		}()
		for {
			// We don't expect incoming messages from the UI on this read-only feed,
			// but reading is required to process WebSocket close control messages.
			if _, _, err := ws.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

// HandleMessages continuously reads from the Broadcast channel and blasts to all connected clients
func (s *Server) HandleMessages() {
	for {
		msg := <-s.Broadcast

		// slog.Info("Broadcasting WebSocket Message to UI", "payload", msg) // Silenced to prevent terminal flood

		s.clientsMu.Lock()
		for client := range s.clients {
			var err error
			switch v := msg.(type) {
			case []byte:
				err = client.WriteMessage(websocket.TextMessage, v)
			case string:
				err = client.WriteMessage(websocket.TextMessage, []byte(v))
			case [][]byte:
				// Extract the actual payload from the ZeroMQ multipart message
				if len(v) > 0 {
					err = client.WriteMessage(websocket.TextMessage, v[len(v)-1])
				}
			case []string:
				if len(v) > 0 {
					err = client.WriteMessage(websocket.TextMessage, []byte(v[len(v)-1]))
				}
			default:
				err = client.WriteJSON(v)
			}

			if err != nil {
				slog.Error("Failed to send message to client, closing connection", "error", err)
				client.Close()
				delete(s.clients, client)
			}
		}
		s.clientsMu.Unlock()
	}
}
