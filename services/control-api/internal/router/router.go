package router

import (
	"net/http"
	"trading-platform/services/control-api/internal/handlers"
)

func New(h *handlers.Handlers) *http.ServeMux {
	r := http.NewServeMux()
	r.HandleFunc("POST /orders", h.HandleCreateOrder)
	r.HandleFunc("POST /login/greeksoft", h.HandleGreeksoftLogin)
	return r
}
