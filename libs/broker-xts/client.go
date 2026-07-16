package xts

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Client struct {
	BaseURL   string
	AppKey    string
	SecretKey string
	Token     string
	Mu        *sync.Mutex
	HTTP      *http.Client
}

func NewClient() *Client {
	baseURL := strings.TrimRight(os.Getenv("XTS_API_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "https" + "://developers.symphonyfintech.in"
	}

	return &Client{
		BaseURL:   baseURL,
		AppKey:    os.Getenv("XTS_API_KEY"),
		SecretKey: os.Getenv("XTS_API_SECRET"),
		Mu:        &sync.Mutex{},
		HTTP:      &http.Client{Timeout: 10 * time.Second},
	}
}
