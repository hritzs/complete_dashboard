package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"trading-platform/services/contract-master/internal/parser"
	"trading-platform/services/contract-master/internal/persistence"
)

// loadTokenCSVs parses both token CSV files and upserts whatever it finds
// into the contracts table. It never fails hard on a missing/unreadable
// file (that leg is just skipped and logged) so a bad re-sync of one file
// never blocks the other from refreshing.
func loadTokenCSVs(ctx context.Context, store *persistence.Store, indexPath, bseIndexPath string) {
	lotLookup := map[string]int{}

	indexContracts, err := parser.ParseTokenCSVFile(indexPath, lotLookup)
	if err != nil {
		log.Printf("failed to load IndexTokens CSV from %s: %v", indexPath, err)
	}

	bseContracts, err := parser.ParseTokenCSVFile(bseIndexPath, lotLookup)
	if err != nil {
		log.Printf("failed to load BSEIndexTokens CSV from %s: %v", bseIndexPath, err)
	}

	log.Printf("📘 IndexTokens.csv contracts: %d", len(indexContracts))
	log.Printf("📙 BSEIndexTokens.csv contracts: %d", len(bseContracts))

	contracts := make([]persistence.Contract, 0, len(indexContracts)+len(bseContracts))
	contracts = append(contracts, indexContracts...)
	contracts = append(contracts, bseContracts...)

	if len(contracts) == 0 {
		log.Printf("⚠️ no contracts loaded from token CSV files")
		return
	}

	if err := store.UpsertContracts(ctx, contracts); err != nil {
		log.Printf("❌ failed to upsert contracts: %v", err)
		return
	}
	log.Printf("✅ Loaded %d contracts from CSV files", len(contracts))
}

// watchTokenCSVsForChanges re-ingests the token CSVs whenever their mtime
// moves forward, so lot sizes (and everything else sourced from these
// files) stay current for this long-running process without needing a
// restart -- lot sizes are revised periodically by exchange circular and
// the sync script that refreshes these files from /mnt/shared can run at
// any time, including overnight. Mirrors the same
// "compare mtime, don't just trust a one-time check" fix already applied
// to scripts/update_index_tokens_daily.sh.
func watchTokenCSVsForChanges(store *persistence.Store, indexPath, bseIndexPath string, interval time.Duration) {
	var lastIndexMTime, lastBSEMTime time.Time
	if fi, err := os.Stat(indexPath); err == nil {
		lastIndexMTime = fi.ModTime()
	}
	if fi, err := os.Stat(bseIndexPath); err == nil {
		lastBSEMTime = fi.ModTime()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		changed := false

		if fi, err := os.Stat(indexPath); err == nil && fi.ModTime().After(lastIndexMTime) {
			lastIndexMTime = fi.ModTime()
			changed = true
		}
		if fi, err := os.Stat(bseIndexPath); err == nil && fi.ModTime().After(lastBSEMTime) {
			lastBSEMTime = fi.ModTime()
			changed = true
		}

		if !changed {
			continue
		}

		log.Printf("🔄 token CSV(s) changed on disk, re-ingesting")
		loadTokenCSVs(context.Background(), store, indexPath, bseIndexPath)
	}
}

func main() {
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_DSN"))
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/trading?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	store := persistence.NewStore(db)
	ctx := context.Background()

	indexPath := strings.TrimSpace(os.Getenv("INDEX_TOKENS_PATH"))
	if indexPath == "" {
		indexPath = strings.TrimSpace(os.Getenv("INDEX_TOKENS_CSV"))
	}
	if indexPath == "" {
		indexPath = "./IndexTokens.csv"
	}

	bseIndexPath := strings.TrimSpace(os.Getenv("BSE_INDEX_TOKENS_PATH"))
	if bseIndexPath == "" {
		bseIndexPath = strings.TrimSpace(os.Getenv("BSE_INDEX_TOKENS_CSV"))
	}
	if bseIndexPath == "" {
		bseIndexPath = "./BSEIndexTokens.csv"
	}

	port := strings.TrimSpace(os.Getenv("CONTRACT_MASTER_PORT"))
	if port == "" {
		port = "8010"
	}

	log.Println("🚀 Loading contracts from token CSV files...")
	log.Printf("📂 INDEX_TOKENS path: %s", indexPath)
	log.Printf("📂 BSE_INDEX_TOKENS path: %s", bseIndexPath)

	loadTokenCSVs(ctx, store, indexPath, bseIndexPath)

	// Re-check the files' mtimes hourly and re-ingest on change, so a lot
	// size (or anything else in these files) revised while this process
	// keeps running -- including overnight -- is picked up automatically
	// instead of only at the next restart.
	go watchTokenCSVsForChanges(store, indexPath, bseIndexPath, time.Hour)

	writeJSON := func(w http.ResponseWriter, status int, payload map[string]interface{}) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}

	getLotSize := func(w http.ResponseWriter, r *http.Request, symbol, expiry string) {
		symbol = strings.TrimSpace(symbol)
		expiry = strings.TrimSpace(expiry)

		if symbol == "" || expiry == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "symbol and expiry required",
			})
			return
		}

		lotSize, err := store.GetLotSize(ctx, symbol, expiry)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"symbol":   symbol,
			"expiry":   expiry,
			"lot_size": lotSize,
		})
	}

	getToken := func(w http.ResponseWriter, r *http.Request, symbol, expiry, optionType string, strike float64) {
		symbol = strings.TrimSpace(symbol)
		expiry = strings.TrimSpace(expiry)
		optionType = strings.TrimSpace(optionType)

		if symbol == "" || expiry == "" || optionType == "" || strike == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "symbol, expiry, optionType, and strike required",
			})
			return
		}

		token, err := store.GetContractToken(ctx, symbol, expiry, optionType, strike)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":     true,
			"symbol":      symbol,
			"expiry":      expiry,
			"option_type": optionType,
			"strike":      strike,
			"token":       token,
		})
	}

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"service": "contract-master",
		})
	})

	http.HandleFunc("/api/lot-size/", func(w http.ResponseWriter, r *http.Request) {
		pathPrefix := "/api/lot-size/"
		symbol := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, pathPrefix))
		expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))

		if symbol == "" {
			symbol = strings.TrimSpace(r.URL.Query().Get("symbol"))
		}

		getLotSize(w, r, symbol, expiry)
	})

	http.HandleFunc("/api/lot-size", func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
		expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))

		getLotSize(w, r, symbol, expiry)
	})

	http.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		pathPrefix := "/api/token/"
		symbol := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, pathPrefix))
		expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
		optionType := strings.TrimSpace(r.URL.Query().Get("option_type"))
		strikeStr := strings.TrimSpace(r.URL.Query().Get("strike"))

		if symbol == "" {
			symbol = strings.TrimSpace(r.URL.Query().Get("symbol"))
		}

		strike, err := strconv.ParseFloat(strikeStr, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid strike price",
			})
			return
		}

		getToken(w, r, symbol, expiry, optionType, strike)
	})

	http.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
		expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
		optionType := strings.TrimSpace(r.URL.Query().Get("option_type"))
		strikeStr := strings.TrimSpace(r.URL.Query().Get("strike"))

		strike, err := strconv.ParseFloat(strikeStr, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid strike price",
			})
			return
		}

		getToken(w, r, symbol, expiry, optionType, strike)
	})

	log.Printf("🌐 Contract Master running on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, corsMiddleware(http.DefaultServeMux)))
}

// corsMiddleware allows the UI (served from a different origin/port) to call
// this service directly, matching execution-gateway's and snapshot-service's
// existing CORS handling.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
