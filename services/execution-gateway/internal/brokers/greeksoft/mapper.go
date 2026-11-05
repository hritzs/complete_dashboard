package greeksoft

import (
	"encoding/csv"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	broker "trading-platform/libs/go-broker"

	"execution-gateway/internal/trading"
)

var (
	gtokenOnce         sync.Once
	gtokenMap          map[int64]int64
	shortTokenByGToken map[int64]int64
	bseExchangeToken   map[int64]bool // BSE rows of BSEIndexTokens.csv: their exchange tokens
)

func MapOrderIntent(intent trading.OrderIntent) *broker.OrderIntent {
	limitPrice := 0.0
	if intent.LimitPrice != nil {
		limitPrice = *intent.LimitPrice
	}
	tif := strings.ToUpper(strings.TrimSpace(intent.TimeInForce))
	if tif != "IOC" {
		tif = "DAY"
	}

	return &broker.OrderIntent{
		TradeUID:        intent.TradeUID,
		IntentID:        intent.IntentID,
		Symbol:          intent.Symbol,
		InstrumentToken: int(resolveGreeksoftGToken(intent.ExchangeSegment, intent.Token)),
		ExchangeSegment: intent.ExchangeSegment,
		Side:            intent.Side,
		Quantity:        int(intent.Quantity),
		LotSize:         int(intent.LotSize),
		OrderType:       intent.OrderType,
		ProductType:     intent.ProductType,
		TimeInForce:     tif,
		ClientID:        intent.AccountID,
		LimitPrice:      limitPrice,
		StopPrice:       0,
		DisclosedQty:    0,
	}
}

func resolveGreeksoftGToken(exchangeSegment string, token int64) int64 {
	if token <= 0 {
		return token
	}

	loadGreeksoftTokenMapOnce()

	// BSE (SENSEX / BANKEX): GreekSoft's own token, from its contract master
	// (bse_tokens.go). There is no formula for it.
	seg := strings.ToUpper(exchangeSegment)
	if strings.Contains(seg, "BSE") || (!strings.Contains(seg, "NSE") && bseExchangeToken[token]) {
		g, ok, loaded := bseTokenFor(token)
		switch {
		case ok:
			log.Printf("[GREEKSOFT TOKEN] BSE exchange_token=%d greek_gtoken=%d (GreekSoft contract master) exchange_segment=%s", token, g, exchangeSegment)
			return g
		case !loaded:
			log.Printf("[GREEKSOFT TOKEN] ⚠️ BSE exchange_token=%d: GreekSoft's contract master is not loaded yet -- GreekSoft will refuse this token", token)
		default:
			log.Printf("[GREEKSOFT TOKEN] ⚠️ BSE exchange_token=%d is not in GreekSoft's contract master -- GreekSoft will refuse it", token)
		}
		return token
	}

	if gtokenMap != nil {
		if mapped, ok := gtokenMap[token]; ok && mapped > 0 {
			log.Printf(
				"[GREEKSOFT TOKEN] mapped short_token=%d greek_gtoken=%d exchange_segment=%s",
				token,
				mapped,
				exchangeSegment,
			)
			return mapped
		}
	}

	log.Printf(
		"[GREEKSOFT TOKEN] no map hit for token=%d exchange_segment=%s, using original",
		token,
		exchangeSegment,
	)

	return token
}

func loadGreeksoftTokenMapOnce() {
	gtokenOnce.Do(func() {
		gtokenMap = make(map[int64]int64)
		shortTokenByGToken = make(map[int64]int64)
		bseExchangeToken = make(map[int64]bool)

		paths := candidateGreeksoftTokenCSVPaths()

		seen := map[string]bool{}

		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}

			if seen[path] {
				continue
			}
			seen[path] = true

			loadGreeksoftTokenCSV(path)
		}

		log.Printf("[GREEKSOFT TOKEN] loaded mappings=%d", len(gtokenMap))
	})
}

func candidateGreeksoftTokenCSVPaths() []string {
	paths := []string{
		os.Getenv("INDEX_TOKENS_PATH"),
		os.Getenv("INDEX_TOKENS_CSV"),
		os.Getenv("BSE_INDEX_TOKENS_PATH"),
		os.Getenv("BSE_INDEX_TOKENS_CSV"),

		"/mnt/shared/IndexTokens.csv",
		"/mnt/shared/BSEIndexTokens.csv",

		"../../IndexTokens.csv",
		"../../BSEIndexTokens.csv",

		"IndexTokens.csv",
		"BSEIndexTokens.csv",
	}

	return paths
}

func loadGreeksoftTokenCSV(path string) {
	file, err := os.Open(path)
	if err != nil {
		log.Printf("[GREEKSOFT TOKEN] unable to open csv path=%s err=%v", path, err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	rows, err := reader.ReadAll()
	if err != nil {
		log.Printf("[GREEKSOFT TOKEN] unable to read csv path=%s err=%v", path, err)
		return
	}

	loaded := 0

	for _, row := range rows {
		if len(row) < 8 {
			continue
		}

		fullToken, err1 := parseInt64Cell(row[0])
		shortToken, err2 := parseInt64Cell(row[7])

		// BSE rows carry the exchange token in the first column and "IO" in the
		// eighth, with no GreekSoft token. GreekSoft's token for them is NOT a
		// formula of the exchange token (202000000 + it matched 0 of 38,499 BSEFO
		// rows on 2026-11-03): it is looked up in GreekSoft's contract master
		// (bse_tokens.go). Here they are only marked as BSE.
		if err1 == nil && err2 != nil && strings.EqualFold(strings.Trim(strings.TrimSpace(row[1]), "\""), "BSE") && fullToken > 0 {
			bseExchangeToken[fullToken] = true
			continue
		}

		if err1 != nil || err2 != nil {
			continue
		}

		if fullToken <= 0 || shortToken <= 0 {
			continue
		}

		gtokenMap[shortToken] = fullToken
		shortTokenByGToken[fullToken] = shortToken
		loaded++
	}

	log.Printf(
		"[GREEKSOFT TOKEN] loaded path=%s rows=%d mappings_added=%d",
		path,
		len(rows),
		loaded,
	)
}

func parseInt64Cell(value string) (int64, error) {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"")

	var builder strings.Builder

	for _, ch := range value {
		if ch >= '0' && ch <= '9' {
			builder.WriteRune(ch)
		}
	}

	cleaned := builder.String()
	if cleaned == "" {
		return 0, strconv.ErrSyntax
	}

	return strconv.ParseInt(cleaned, 10, 64)
}

func resolveGreeksoftShortToken(brokerToken int64) int64 {
	if brokerToken <= 0 {
		return brokerToken
	}

	loadGreeksoftTokenMapOnce()

	if shortTokenByGToken != nil {
		if shortToken, ok := shortTokenByGToken[brokerToken]; ok && shortToken > 0 {
			return shortToken
		}
	}
	if e, ok := bseExchangeTokenFor(brokerToken); ok && e > 0 { // BSE: from GreekSoft's contract master
		return e
	}

	// Preserve the original value when no mapping exists; do not invent a token.
	return brokerToken
}
