// Read-only diagnostic: logs in and asks GreekSoft's own live
// getFullScripDetailsBySymbol_Mobile API for the real current lot size of
// each index this platform trades, so an actual broker-authoritative value
// can be cross-checked against the various hardcoded/contract-master tables
// in the codebase. Places no order.
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
		Name:       "greeksoft-lotsizeprobe-" + strings.ToLower(accountID),
		BrokerType: "greeksoft",
		APIKey:     accountID,
		APISecret:  password,
		ClientID:   accountID,
		PanDob:     panDOB,
	})
	if err != nil {
		log.Fatalf("GreekSoft login failed: %v", err)
	}
	log.Printf("[LOTSIZEPROBE] login successful account=%s user_id=%s", accountID, session.UserID)

	symbols := map[string]bool{
		"NIFTY": true, "BANKNIFTY": true, "FINNIFTY": true,
		"MIDCPNIFTY": true, "SENSEX": true, "BANKEX": true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	details, err := client.GetAllContract(ctx)
	if err != nil {
		log.Fatalf("GetAllContract failed: %v", err)
	}
	log.Printf("[LOTSIZEPROBE] GetAllContract returned %d rows total", len(details))

	// Per-symbol: distinct lotQty values seen, with one example row each.
	seen := map[string]map[int64]greeksoft.ContractDetail{}
	for _, d := range details {
		name := strings.ToUpper(strings.TrimSpace(d.Name))
		if name == "" {
			name = strings.ToUpper(strings.TrimSpace(d.ScriptName))
		}
		if !symbols[name] {
			continue
		}
		if seen[name] == nil {
			seen[name] = map[int64]greeksoft.ContractDetail{}
		}
		seen[name][d.LotQty] = d
	}
	for sym := range symbols {
		rows, ok := seen[sym]
		if !ok || len(rows) == 0 {
			log.Printf("[LOTSIZEPROBE] %-12s no rows found in GetAllContract dump", sym)
			continue
		}
		for lotQty, d := range rows {
			log.Printf("[LOTSIZEPROBE] %-12s lotQty=%d tradeSymbol=%s exchange=%s expiry_unix=%d strike=%.0f optionType=%s",
				sym, lotQty, d.TradeSymbol, d.Exchange, d.ExpiryDate, d.StrikePrice, d.OptionType)
		}
	}

	log.Printf("[LOTSIZEPROBE] done")
}
