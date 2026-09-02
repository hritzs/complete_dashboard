package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"trading-platform/services/control-api/internal/client"
	"trading-platform/services/control-api/internal/config"
	"trading-platform/services/control-api/internal/session"
)

// Handlers holds dependencies for the HTTP handlers.
type Handlers struct {
	cfg             *config.Config
	gatewayClient   *client.GatewayClient
	greeksoftClient *client.GreeksoftClient
	sessionStore    *session.Store
}

// New creates a new Handlers struct.
func New(cfg *config.Config, gw *client.GatewayClient, gs *client.GreeksoftClient, store *session.Store) *Handlers {
	return &Handlers{
		cfg:             cfg,
		gatewayClient:   gw,
		greeksoftClient: gs,
		sessionStore:    store,
	}
}

// HandleCreateOrder is a placeholder for creating an order.
func (h *Handlers) HandleCreateOrder(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte("Not Implemented"))
}

// HandleGreeksoftLogin triggers the login process with the Greeksoft broker.
func (h *Handlers) HandleGreeksoftLogin(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request to log into Greeksoft...")

	loginResp, err := h.greeksoftClient.Login(r.Context())
	if err != nil {
		log.Printf("Error during Greeksoft login: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Here you would typically store the session token securely.
	// For now, we just log it and return it.
	log.Printf("Successfully logged into Greeksoft. Session token: %s", loginResp.SessionToken)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(loginResp)
}
