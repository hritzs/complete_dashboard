package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/lib/pq"

	"trading-platform/services/contract-master/internal/parser"
	"trading-platform/services/contract-master/internal/persistence"
)

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
	} else {
		if err := store.UpsertContracts(ctx, contracts); err != nil {
			log.Fatal(err)
		}
		log.Printf("✅ Loaded %d contracts from CSV files", len(contracts))
	}

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

	log.Printf("🌐 Contract Master running on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
