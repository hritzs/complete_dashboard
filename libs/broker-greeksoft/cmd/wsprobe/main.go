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

func main() {
	authURL := requiredEnv("GREEK_API_AUTH_URL")
	restURL := requiredEnv("GREEK_API_REST_URL")
	password := requiredEnv("GREEK_PASSWORD")
	panDOB := requiredEnv("GREEK_PAN_DOB")

	accountID := strings.ToUpper(strings.TrimSpace(os.Getenv("GREEK_ACCOUNT_ID")))
	if accountID == "" {
		accountID = "HRITIK"
	}

	client := greeksoft.NewClient(authURL, restURL)

	loginCtx, loginCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer loginCancel()

	session, err := client.PerformFullLogin(loginCtx, &broker.AccountConfig{
		Name:       "greeksoft-wsprobe-" + strings.ToLower(accountID),
		BrokerType: "greeksoft",
		APIKey:     accountID,
		APISecret:  password,
		ClientID:   accountID,
		PanDob:     panDOB,
	})
	if err != nil {
		log.Fatalf("GreekSoft login failed: %v", err)
	}

	log.Printf("[WSPROBE] login successful account=%s user_id=%s", accountID, session.UserID)

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer probeCancel()

	iris, err := client.NewIrisDiscoveryClient(probeCtx)
	if err != nil {
		log.Fatalf("Iris websocket connection/login failed: %v", err)
	}
	defer func() {
		if err := iris.Close(); err != nil {
			log.Printf("[WSPROBE] close warning: %v", err)
		}
	}()

	log.Printf("[WSPROBE] Iris login request sent; reading frames only; no orders will be placed")

	err = iris.ReadLoop(probeCtx, func(frame greeksoft.IrisFrame) {
		log.Printf("[WSPROBE EVENT] type=%s service=%s", frame.StreamingType, frame.ServiceName)
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		log.Fatalf("Iris read loop failed: %v", err)
	}

	log.Printf("[WSPROBE] passive capture complete")
}
