package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	greeksoft "trading-platform/libs/broker-greeksoft"
	broker "trading-platform/libs/go-broker"
)

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("required environment variable %s is empty", name)
	}
	return value
}

// resolveGreekAccountID matches the rest of the platform's .env
// convention (GREEK_CLIENT_ID/GREEK_USERNAME, e.g. "147") rather than
// requiring a separate GREEK_ACCOUNT_ID variable nothing else sets.
func resolveGreekAccountID() string {
	for _, key := range []string{"GREEK_ACCOUNT_ID", "GREEK_CLIENT_ID", "GREEK_USERNAME"} {
		if v := strings.ToUpper(strings.TrimSpace(os.Getenv(key))); v != "" {
			return v
		}
	}
	return "HRITIK"
}

func main() {
	authURL := requiredEnv("GREEK_API_AUTH_URL")
	restURL := requiredEnv("GREEK_API_REST_URL")
	password := requiredEnv("GREEK_PASSWORD")
	panDOB := requiredEnv("GREEK_PAN_DOB")

	accountID := resolveGreekAccountID()

	// Default token is NIFTY 25MAY23 FUTIDX from GreekSoft's own docs
	// example -- override with a live token via GREEK_PROBE_TOKENS
	// (comma-separated) once one is known for the current expiry.
	tokens := strings.Split(strings.TrimSpace(os.Getenv("GREEK_PROBE_TOKENS")), ",")
	if len(tokens) == 1 && tokens[0] == "" {
		tokens = []string{"101002885"} // RELIANCE, per the docs' marketPicture example
	}

	client := greeksoft.NewClient(authURL, restURL)

	loginCtx, loginCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer loginCancel()

	session, err := client.PerformFullLogin(loginCtx, &broker.AccountConfig{
		Name:       "greeksoft-apolloprobe-" + strings.ToLower(accountID),
		BrokerType: "greeksoft",
		APIKey:     accountID,
		APISecret:  password,
		ClientID:   accountID,
		PanDob:     panDOB,
	})
	if err != nil {
		log.Fatalf("GreekSoft login failed: %v", err)
	}

	log.Printf("[APOLLOPROBE] login successful account=%s user_id=%s", accountID, session.UserID)

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer probeCancel()

	apollo, err := client.NewApolloMarketDataClient(probeCtx)
	if err != nil {
		log.Fatalf("Apollo websocket connection/login failed: %v", err)
	}
	defer func() {
		if err := apollo.Close(); err != nil {
			log.Printf("[APOLLOPROBE] close warning: %v", err)
		}
	}()

	if err := apollo.Subscribe(tokens); err != nil {
		log.Fatalf("Apollo subscribe failed: %v", err)
	}
	log.Printf("[APOLLOPROBE] subscribed tokens=%v; reading frames for 45s", tokens)

	err = apollo.ReadLoop(probeCtx, func(frame greeksoft.ApolloFrame) {
		log.Printf("[APOLLOPROBE EVENT] type=%s service=%s raw=%s", frame.StreamingType, frame.ServiceName, string(frame.Raw))
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		log.Fatalf("Apollo read loop failed: %v", err)
	}

	log.Printf("[APOLLOPROBE] capture complete")
}
