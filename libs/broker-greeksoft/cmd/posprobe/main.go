// Read-only diagnostic: logs in and asks GreekSoft's own live NPRequest API
// for the account's real net positions, so they can be checked directly
// against what our own system believes was built/squared-off. Places no
// order, modifies nothing.
package main

import (
	"context"
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
	client := greeksoft.NewClient(authURL, restURL)

	loginCtx, loginCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer loginCancel()
	session, err := client.PerformFullLogin(loginCtx, &broker.AccountConfig{
		Name:       "greeksoft-posprobe-" + strings.ToLower(accountID),
		BrokerType: "greeksoft",
		APIKey:     accountID,
		APISecret:  password,
		ClientID:   accountID,
		PanDob:     panDOB,
	})
	if err != nil {
		log.Fatalf("GreekSoft login failed: %v", err)
	}
	log.Printf("[POSPROBE] login successful account=%s user_id=%s", accountID, session.UserID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := client.NPRequest(ctx)
	if err != nil {
		log.Fatalf("NPRequest failed: %v", err)
	}

	log.Printf("[POSPROBE] noofrecords=%s islast=%s stockDetails=%d",
		resp.Response.Data.NoOfRecords, resp.Response.Data.IsLast, len(resp.Response.Data.StockDetails))
	for _, p := range resp.Response.Data.StockDetails {
		if p.NetQty == "0" || p.NetQty == "" {
			continue
		}
		log.Printf("[POSPROBE] symbol=%-20s token=%-12s netQty=%-8s preNetQty=%-8s product=%s account=%s",
			p.Symbol, p.Token, p.NetQty, p.PreNetQty, p.ProductType, p.Account)
	}
	log.Printf("[POSPROBE] done")
}
