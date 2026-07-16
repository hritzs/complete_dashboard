package parser

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trading-platform/services/contract-master/internal/persistence"
)

type MasterParseConfig struct {
	Delimiter         string
	IdxExchange       int
	IdxBrokerToken    int
	IdxSymbol         int
	IdxTradingSymbol  int
	IdxInstrumentType int
	IdxTickSize       int
	IdxLotSize        int
	IdxExpiry         int
}

type LotSizeLookup map[string]int

func ParseConfigFromEnv() MasterParseConfig {
	return MasterParseConfig{
		Delimiter:         getenvDefault("XTS_MASTER_DELIMITER", "|"),
		IdxExchange:       getenvInt("XTS_MASTER_IDX_EXCHANGE", 0),
		IdxBrokerToken:    getenvInt("XTS_MASTER_IDX_TOKEN", 1),
		IdxSymbol:         getenvInt("XTS_MASTER_IDX_SYMBOL", 3),
		IdxTradingSymbol:  getenvInt("XTS_MASTER_IDX_TRADINGSYMBOL", 4),
		IdxInstrumentType: getenvInt("XTS_MASTER_IDX_TYPE", 5),
		IdxTickSize:       getenvInt("XTS_MASTER_IDX_TICKSIZE", 11),
		IdxLotSize:        getenvInt("XTS_MASTER_IDX_LOTSIZE", 12),
		IdxExpiry:         getenvInt("XTS_MASTER_IDX_EXPIRY", 16),
	}
}

func ParseMaster(raw map[string]interface{}, cfg MasterParseConfig) ([]persistence.Contract, error) {
	result, ok := raw["result"]
	if !ok {
		return nil, fmt.Errorf("master response missing result")
	}

	lines, err := extractLines(result)
	if err != nil {
		return nil, err
	}

	var contracts []persistence.Contract

	maxIdx := maxInt(
		cfg.IdxExchange,
		cfg.IdxBrokerToken,
		cfg.IdxSymbol,
		cfg.IdxTradingSymbol,
		cfg.IdxInstrumentType,
		cfg.IdxTickSize,
		cfg.IdxLotSize,
		cfg.IdxExpiry,
	)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		cols := strings.Split(line, cfg.Delimiter)
		if len(cols) <= maxIdx {
			continue
		}

		exchange := strings.ToUpper(strings.TrimSpace(cols[cfg.IdxExchange]))
		tokenStr := strings.TrimSpace(cols[cfg.IdxBrokerToken])
		symbol := strings.ToUpper(strings.TrimSpace(cols[cfg.IdxSymbol]))
		tradingSymbol := strings.TrimSpace(cols[cfg.IdxTradingSymbol])
		instrumentType := strings.ToUpper(strings.TrimSpace(cols[cfg.IdxInstrumentType]))
		tickStr := strings.TrimSpace(cols[cfg.IdxTickSize])
		lotStr := strings.TrimSpace(cols[cfg.IdxLotSize])
		expiryStr := strings.TrimSpace(cols[cfg.IdxExpiry])

		if exchange == "" || tokenStr == "" || symbol == "" || lotStr == "" {
			continue
		}

		token, err := strconv.ParseInt(tokenStr, 10, 64)
		if err != nil || token == 0 {
			continue
		}

		lotSize, err := strconv.Atoi(lotStr)
		if err != nil || lotSize <= 0 {
			continue
		}

		tickSize, _ := strconv.ParseFloat(tickStr, 64)

		expiry, err := parseExpiryFlexible(expiryStr)
		if err != nil {
			continue
		}

		optionType := deriveOptionType(tradingSymbol)
		strike := deriveStrike(instrumentType, tradingSymbol)

		rawRow, _ := json.Marshal(map[string]string{
			"source": "xts-master",
			"line":   line,
		})

		contracts = append(contracts, persistence.Contract{
			BrokerToken:    token,
			Exchange:       exchange,
			Symbol:         symbol,
			InstrumentType: instrumentType,
			ExpiryDate:     expiry,
			StrikePrice:    strike,
			OptionType:     optionType,
			LotSize:        lotSize,
			TickSize:       tickSize,
			RawDetails:     rawRow,
		})
	}

	return contracts, nil
}

func BuildLotSizeLookup(rows []persistence.Contract) LotSizeLookup {
	out := make(LotSizeLookup)

	for _, r := range rows {
		if r.LotSize <= 0 {
			continue
		}

		k1 := lotKey(r.Exchange, r.Symbol, r.ExpiryDate, "")
		out[k1] = r.LotSize

		k2 := lotKey(r.Exchange, r.Symbol, r.ExpiryDate, r.InstrumentType)
		out[k2] = r.LotSize
	}

	return out
}

func ParseTokenCSVFile(path string, lots LotSizeLookup) ([]persistence.Contract, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true

	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv %s: %w", path, err)
	}

	contracts := make([]persistence.Contract, 0, len(rows))

	for _, row := range rows {
		contract, ok := parseTokenCSVRow(path, row, lots)
		if !ok {
			continue
		}
		contracts = append(contracts, contract)
	}

	return contracts, nil
}

func parseTokenCSVRow(source string, row []string, lots LotSizeLookup) (persistence.Contract, bool) {
	if len(row) < 8 {
		return persistence.Contract{}, false
	}

	tokenStr := cleanCell(row[0])
	exchangeRaw := cleanCell(row[1])
	instrumentType := cleanCell(row[2])
	symbol := cleanCell(row[3])
	expiryStr := cleanCell(row[4])
	strikeStr := cleanCell(row[5])
	optionTypeRaw := cleanCell(row[6])

	if tokenStr == "" || exchangeRaw == "" || symbol == "" || expiryStr == "" {
		return persistence.Contract{}, false
	}

	if looksLikeHeader(tokenStr, exchangeRaw, instrumentType, symbol) {
		return persistence.Contract{}, false
	}

	token, err := strconv.ParseInt(cleanNumeric(tokenStr), 10, 64)
	if err != nil || token <= 0 {
		return persistence.Contract{}, false
	}

	expiry, err := parseExpiryFlexible(expiryStr)
	if err != nil {
		return persistence.Contract{}, false
	}

	strike := 0.0
	if strikeStr != "" && !strings.EqualFold(strikeStr, "NA") {
		strike, _ = strconv.ParseFloat(cleanNumeric(strikeStr), 64)
	}

	exchange := normalizeCSVExchange(exchangeRaw)
	instType := normalizeCSVInstrumentType(exchange, instrumentType)
	optionType := normalizeOptionType(optionTypeRaw)

	lotSize := lookupLotSize(lots, exchange, symbol, expiry, instType)
	if lotSize <= 0 {
		lotSize = fallbackLotSize(symbol)
	}
	if lotSize <= 0 {
		return persistence.Contract{}, false
	}

	tickSize := inferTickSizeFromRow(row)

	rawRow, _ := json.Marshal(map[string]any{
		"source": source,
		"row":    row,
	})

	return persistence.Contract{
		BrokerToken:    token,
		Exchange:       exchange,
		Symbol:         strings.ToUpper(symbol),
		InstrumentType: instType,
		ExpiryDate:     expiry,
		StrikePrice:    strike,
		OptionType:     optionType,
		LotSize:        lotSize,
		TickSize:       tickSize,
		RawDetails:     rawRow,
	}, true
}

func lookupLotSize(lots LotSizeLookup, exchange, symbol string, expiry time.Time, instrumentType string) int {
	keys := []string{
		lotKey(exchange, symbol, expiry, instrumentType),
		lotKey(exchange, symbol, expiry, ""),
	}

	for _, k := range keys {
		if v, ok := lots[k]; ok && v > 0 {
			return v
		}
	}

	return 0
}

func fallbackLotSize(symbol string) int {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "NIFTY":
		return 65
	case "BANKNIFTY":
		return 15
	case "FINNIFTY":
		return 40
	case "MIDCPNIFTY":
		return 65
	case "SENSEX":
		return 20
	case "BANKEX":
		return 15
	default:
		return 0
	}
}

func lotKey(exchange, symbol string, expiry time.Time, instrumentType string) string {
	return strings.ToUpper(strings.TrimSpace(exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(symbol)) + "|" +
		expiry.Format("2006-01-02") + "|" +
		strings.ToUpper(strings.TrimSpace(instrumentType))
}

func normalizeCSVExchange(v string) string {
	s := strings.ToUpper(cleanCell(v))
	switch s {
	case "NSE":
		return "NSEFO"
	case "BSE":
		return "BSEFO"
	default:
		return s
	}
}

func normalizeCSVInstrumentType(exchange, v string) string {
	s := strings.ToUpper(cleanCell(v))

	if exchange == "NSEFO" && s == "OPTIDX" {
		return "OPTIDX"
	}
	if exchange == "BSEFO" && s == "OPTION" {
		return "OPTION"
	}
	return s
}

func normalizeOptionType(v string) string {
	s := strings.ToUpper(cleanCell(v))
	switch s {
	case "CE", "CALL":
		return "CE"
	case "PE", "PUT":
		return "PE"
	default:
		return ""
	}
}

func inferTickSizeFromRow(row []string) float64 {
	for _, idx := range []int{8, 9, 10, 11, 12} {
		if idx >= len(row) {
			continue
		}
		v := cleanCell(row[idx])
		if v == "" || strings.EqualFold(v, "NA") {
			continue
		}
		f, err := strconv.ParseFloat(cleanNumeric(v), 64)
		if err == nil && f > 0 && f <= 100 {
			return f
		}
	}
	return 0.05
}

func looksLikeHeader(parts ...string) bool {
	joined := strings.ToUpper(strings.Join(parts, " "))
	return strings.Contains(joined, "TOKEN") ||
		strings.Contains(joined, "EXCHANGE") ||
		strings.Contains(joined, "SYMBOL") ||
		strings.Contains(joined, "INSTRUMENT")
}

func deriveOptionType(tradingSymbol string) string {
	ts := strings.ToUpper(strings.TrimSpace(tradingSymbol))
	switch {
	case strings.HasSuffix(ts, "CE"):
		return "CE"
	case strings.HasSuffix(ts, "PE"):
		return "PE"
	default:
		return ""
	}
}

func deriveStrike(instrumentType, tradingSymbol string) float64 {
	inst := strings.ToUpper(strings.TrimSpace(instrumentType))
	ts := strings.ToUpper(strings.TrimSpace(tradingSymbol))

	if strings.HasPrefix(inst, "FUT") {
		return 0
	}

	re := regexp.MustCompile(`(\d+)(CE|PE)$`)
	m := re.FindStringSubmatch(ts)
	if len(m) != 3 {
		return 0
	}

	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return v
}

func extractLines(result any) ([]string, error) {
	switch v := result.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out, nil

	case string:
		var out []string
		for _, line := range strings.Split(v, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				out = append(out, line)
			}
		}
		return out, nil

	default:
		b, _ := json.Marshal(result)
		return nil, fmt.Errorf("unsupported master result format: %s", string(b))
	}
}

func parseExpiryFlexible(v string) (time.Time, error) {
	candidates := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
		"02-JAN-06",
		"02-JAN-2006",
		"02-Jan-06",
		"02-Jan-2006",
		"2-JAN-2006",
		"2-Jan-2006",
	}

	val := cleanCell(v)
	for _, layout := range candidates {
		if t, err := time.Parse(layout, val); err == nil {
			return t, nil
		}
		if t, err := time.Parse(layout, strings.ToUpper(val)); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported expiry format: %s", v)
}

func cleanCell(v string) string {
	return strings.Trim(strings.TrimSpace(v), `"`)
}

func cleanNumeric(v string) string {
	s := cleanCell(v)
	s = strings.ReplaceAll(s, ",", "")
	return s
}

func getenvDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func maxInt(vals ...int) int {
	m := 0
	for i, v := range vals {
		if i == 0 || v > m {
			m = v
		}
	}
	return m
}
