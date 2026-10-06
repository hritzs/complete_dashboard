// Read-only diagnostic: logs in and asks GreekSoft's own live
// getQuoteForSingleSymbol_V2 API for the real current freeze quantity
// (freezQty) of one near-month option per index this platform trades, so
// the broker-authoritative value can be cross-checked against the
// value the gateway loads at startup (PreloadFreezeQty) and the live
// lookup execution-gateway now uses (see trading.Service.resolveMaxOrderQty
// and greeksoft.Executor.GetFreezeQty). Places no order.
package main

import (
	"context"
	"log"
	"os"
	"strconv"
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
		Name:       "greeksoft-freezeqtyprobe-" + strings.ToLower(accountID),
		BrokerType: "greeksoft",
		APIKey:     accountID,
		APISecret:  password,
		ClientID:   accountID,
		PanDob:     panDOB,
	})
	if err != nil {
		log.Fatalf("GreekSoft login failed: %v", err)
	}
	log.Printf("[FREEZEQTYPROBE] login successful account=%s user_id=%s", accountID, session.UserID)

	gcidValue, ok := session.BrokerSpecific["gcid"]
	if !ok {
		log.Fatalf("session missing gcid")
	}
	gcid := strings.TrimSpace(strconv.FormatInt(toInt64(gcidValue), 10))
	if gcid == "" || gcid == "0" {
		log.Fatalf("session gcid is empty/zero")
	}

	symbols := []string{"NIFTY", "BANKNIFTY", "FINNIFTY", "MIDCPNIFTY", "SENSEX", "BANKEX"}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	details, err := client.GetAllContract(ctx)
	if err != nil {
		log.Fatalf("GetAllContract failed: %v", err)
	}
	log.Printf("[FREEZEQTYPROBE] GetAllContract returned %d rows total", len(details))

	// Pick one representative option token per symbol: the nearest-expiry
	// row seen for that name, any strike/type -- freeze qty is set per
	// underlying/series, not per individual strike, so any live token for
	// that symbol is representative.
	best := map[string]greeksoft.ContractDetail{}
	for _, d := range details {
		name := strings.ToUpper(strings.TrimSpace(d.Name))
		if name == "" {
			name = strings.ToUpper(strings.TrimSpace(d.ScriptName))
		}
		match := ""
		for _, sym := range symbols {
			if name == sym {
				match = sym
				break
			}
		}
		if match == "" || d.Token == 0 || d.OptionType == "" {
			continue
		}
		existing, seen := best[match]
		if !seen || (d.ExpiryDate > 0 && (existing.ExpiryDate == 0 || d.ExpiryDate < existing.ExpiryDate)) {
			best[match] = d
		}
	}

	for _, sym := range symbols {
		d, ok := best[sym]
		if !ok {
			log.Printf("[FREEZEQTYPROBE] %-12s no representative token found in GetAllContract dump", sym)
			continue
		}

		resp, err := client.GetQuoteForSingleSymbolV2(ctx, strconv.FormatInt(d.Token, 10), "option", gcid)
		if err != nil {
			log.Printf("[FREEZEQTYPROBE] %-12s token=%d quote fetch FAILED: %v", sym, d.Token, err)
			continue
		}

		log.Printf(
			"[FREEZEQTYPROBE] %-12s token=%d tradeSymbol=%s freezQty=%d authorizedQty=%d sqOffQty=%d lot=%d",
			sym, d.Token, d.TradeSymbol,
			resp.Response.Data.FreezeQty,
			resp.Response.Data.AuthorizedQty,
			resp.Response.Data.SqOffQty,
			resp.Response.Data.Lot,
		)
	}

	log.Printf("[FREEZEQTYPROBE] done")
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	default:
		return 0
	}
}
