package trading

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func NormalizeSymbol(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func SymbolCode(symbol string) string {
	switch NormalizeSymbol(symbol) {
	case "NIFTY":
		return "NIF"
	case "BANKNIFTY":
		return "BNF"
	case "FINNIFTY":
		return "FNF"
	case "MIDCPNIFTY":
		return "MID"
	case "SENSEX":
		return "SNX"
	case "BANKEX":
		return "BKX"
	default:
		s := NormalizeSymbol(symbol)
		if len(s) >= 3 {
			return s[:3]
		}
		return s
	}
}

func ResolveExchangeSegment(symbol, requested string) string {
	req := strings.ToUpper(strings.TrimSpace(requested))
	if req != "" {
		return req
	}

	switch NormalizeSymbol(symbol) {
	case "SENSEX", "BANKEX":
		return "BSEFO"
	default:
		return "NSEFO"
	}
}

func BuildTradeUID(userID, brokerName, accountID, symbol, expiry string, strike float64, ts time.Time) string {
	cleanExpiry := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(expiry), "-", ""))
	return fmt.Sprintf(
		"TRD_%s_%s_%s_%s_%s_%.0f_%s",
		strings.ToUpper(strings.TrimSpace(userID)),
		strings.ToUpper(strings.TrimSpace(brokerName)),
		strings.ToUpper(strings.TrimSpace(accountID)),
		strings.ToUpper(strings.TrimSpace(symbol)),
		cleanExpiry,
		strike,
		ts.Format("20060102150405"),
	)
}

func BuildShortOrderUID(symbol, leg string, ts time.Time, suffix int) string {
	base := fmt.Sprintf("%s%s%s", SymbolCode(symbol), ts.Format("020106150405"), strings.ToUpper(leg))
	if suffix > 0 {
		base = fmt.Sprintf("%s_%d", base, suffix)
	}
	if len(base) > 20 {
		return base[:20]
	}
	return base
}

func FindATMRow(chain OptionChainSnapshot) (*OptionChainRow, error) {
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if row.IsATM || row.Strike == chain.ATM {
			return row, nil
		}
	}
	return nil, fmt.Errorf("ATM row not found")
}

func FindRowByStrike(chain OptionChainSnapshot, strike int) (*OptionChainRow, error) {
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if int(math.Round(row.Strike)) == strike {
			return row, nil
		}
	}
	return nil, fmt.Errorf("strike %d not found in chain", strike)
}

func GetFallbackLotSize(symbol string) int {
	switch NormalizeSymbol(symbol) {
	case "NIFTY":
		return 25
	case "BANKNIFTY":
		return 15
	case "FINNIFTY":
		return 25
	case "MIDCPNIFTY":
		return 50
	case "SENSEX":
		return 10
	case "BANKEX":
		return 15
	default:
		return 1
	}
}

func MaxOrderQtyForSymbol(symbol string) int {
	s := NormalizeSymbol(symbol)
	switch {
	case strings.Contains(s, "SENSEX"):
		return 5000
	case strings.Contains(s, "BANKEX"):
		return 4000
	default:
		return 1800
	}
}
