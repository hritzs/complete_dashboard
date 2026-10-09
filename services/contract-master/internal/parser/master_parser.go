package parser

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
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

	greeksoftGTokenStr := cleanCell(row[0])
	exchangeRaw := cleanCell(row[1])
	instrumentType := cleanCell(row[2])
	symbol := cleanCell(row[3])
	expiryStr := cleanCell(row[4])
	strikeStr := cleanCell(row[5])
	optionTypeRaw := cleanCell(row[6])
	shortTokenStr := cleanCell(row[7])

	if greeksoftGTokenStr == "" || shortTokenStr == "" ||
		exchangeRaw == "" || symbol == "" || expiryStr == "" {
		return persistence.Contract{}, false
	}

	if looksLikeHeader(greeksoftGTokenStr, exchangeRaw, instrumentType, symbol) {
		return persistence.Contract{}, false
	}

	// NSE rows provide a numeric short Token at column 7; that is the token
	// used by the platform's feed, snapshots, and OrderIntent. BSE rows use
	// the numeric xx.Token at column 0 because column 7 contains values such
	// as "IO", not an instrument token.
	normalizedExchange := normalizeCSVExchange(exchangeRaw)

	var token int64
	var greeksoftGToken int64
	var err error

	if normalizedExchange == "NSEFO" {
		token, err = strconv.ParseInt(cleanNumeric(shortTokenStr), 10, 64)
		if err != nil || token <= 0 {
			return persistence.Contract{}, false
		}

		greeksoftGToken, _ = strconv.ParseInt(
			cleanNumeric(greeksoftGTokenStr),
			10,
			64,
		)
	} else if normalizedExchange == "BSEFO" {
		token, err = strconv.ParseInt(cleanNumeric(greeksoftGTokenStr), 10, 64)
		if err != nil || token <= 0 {
			return persistence.Contract{}, false
		}

		greeksoftGToken = token
	} else {
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

	exchange := normalizedExchange
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
		"source":           source,
		"row":              row,
		"short_token":      token,
		"greeksoft_gtoken": greeksoftGToken,
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

// fallbackLotSize's values are verified live against GreekSoft's own
// getAllContract scrip master (2026-09-23, via libs/broker-greeksoft/cmd/
// lotsizeprobe) -- consistent across every contract row for each symbol,
// no ambiguity. A previous version of this table had BANKNIFTY=15,
// FINNIFTY=40, MIDCPNIFTY=65, BANKEX=15, all wrong (off by 2x/1.5x/1.5x/2x)
// -- confirmed wrong live when IRIS rejected a real BANKNIFTY order sized
// off it ("Lot size and Qty is Not Matched In Request"). Whatever fed those
// numbers (this table, or whatever it was copied from) was never checked
// against the broker's own contract master. Re-verify against
// lotsizeprobe before changing any of these again.
func fallbackLotSize(symbol string) int {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "NIFTY":
		return 65
	case "BANKNIFTY":
		return 30
	case "FINNIFTY":
		return 60
	case "MIDCPNIFTY":
		return 120
	case "SENSEX":
		return 20
	case "BANKEX":
		return 30
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
