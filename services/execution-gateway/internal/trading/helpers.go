package trading

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
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

// orderUIDSeq makes every generated order UID unique within this process,
// even for two trades exiting in the same second (live 2026-09-30: two
// trades' TIME exits at 15:37 produced 239 identical intent IDs; the DB kept
// one row per ID, so each trade's exit fills went unmatched and the
// square-off retry bought back the "missing" quantity again).
var orderUIDSeq uint64

func BuildShortOrderUID(symbol, leg string, ts time.Time, suffix int) string {
	seq := strings.ToUpper(strconv.FormatUint(atomic.AddUint64(&orderUIDSeq, 1)%1679616, 36)) // 4 base-36 chars
	for len(seq) < 4 {
		seq = "0" + seq
	}
	// Sequence right after the timestamp, so a 32-char truncation can only
	// cut the leg text, never the part that makes the UID unique.
	base := fmt.Sprintf("%s%s%s%s", SymbolCode(symbol), ts.Format("020106150405"), seq, strings.ToUpper(leg))
	if suffix > 0 {
		base = fmt.Sprintf("%s_%d", base, suffix)
	}
	if len(base) > 32 {
		return base[:32]
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
	sym := NormalizeSymbol(symbol)
	// Index lot sizes are revised by NSE circular periodically (e.g. NIFTY 75->65, Jan 2026).
	// Hardcoding them is unsafe -- force callers to resolve from live contract master instead.
	switch sym {
	case "NIFTY", "BANKNIFTY", "FINNIFTY", "MIDCPNIFTY", "SENSEX", "BANKEX":
		log.Printf("⚠️ FALLBACK BLOCKED | %s has no safe hardcoded fallback -- resolve from live contract master (CSV/API)", sym)
		return 0
	default:
		return 1
	}
}

// CreatedTodayIST reports whether t falls on today's date in IST.
func CreatedTodayIST(t time.Time) bool {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+1800)
	}
	now := time.Now().In(loc)
	tt := t.In(loc)
	return tt.Year() == now.Year() && tt.YearDay() == now.YearDay()
}
