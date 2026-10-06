package trading

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// rawMarketDataProvider is implemented by the GreekSoft executor: read-only
// historical candles and current depth, returned exactly as GreekSoft sends
// them.
type rawMarketDataProvider interface {
	RawOHLC(ctx context.Context, token int64, interval int, date string, days int) (map[string]interface{}, int64, error)
	RawMBP(ctx context.Context, token int64, exchange string) (map[string]interface{}, int64, error)
}

// describeShape summarises a JSON value: for arrays, the length and the
// first two elements -- enough to see a candle's fields and the time step
// between consecutive candles without scrolling the whole payload.
func describeShape(v interface{}, depth int) interface{} {
	if depth > 4 {
		return "..."
	}
	switch t := v.(type) {
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, child := range t {
			out[k] = describeShape(child, depth+1)
		}
		return out
	case []interface{}:
		d := map[string]interface{}{"array_len": len(t)}
		if len(t) > 0 {
			d["first"] = t[0]
		}
		if len(t) > 1 {
			d["second"] = t[1]
			d["last"] = t[len(t)-1]
		}
		return d
	case string:
		if len(t) > 200 {
			return t[:200] + "..."
		}
		return t
	default:
		return t
	}
}

// DebugGreeksoftMarketData: GET /api/debug/greeksoft/marketdata
//
//	?kind=ohlc&token=40701&interval=1&date=2026-10-01&days=1
//	?kind=mbp&token=40701[&exchange=NSE]
//	&user_id=U001&broker_name=GREEKSOFT&account_id=147
//
// Read-only: never touches orders.
func (h *Handlers) DebugGreeksoftMarketData(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token, err := strconv.ParseInt(strings.TrimSpace(q.Get("token")), 10, 64)
	if err != nil || token <= 0 {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "positive token is required"})
		return
	}
	userID, broker, account := q.Get("user_id"), strings.ToUpper(q.Get("broker_name")), q.Get("account_id")
	if userID == "" {
		userID = "U001"
	}
	if broker == "" {
		broker = "GREEKSOFT"
	}
	if account == "" {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "account_id is required"})
		return
	}
	executor, err := h.Service.BrokerFactory.GetExecutor(userID, broker, account)
	if err != nil {
		lutJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	p, ok := executor.(rawMarketDataProvider)
	if !ok {
		lutJSON(w, http.StatusNotImplemented, map[string]interface{}{"success": false, "error": "raw market data not available for this broker"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var (
		resp   map[string]interface{}
		gtoken int64
	)
	kind := strings.ToLower(q.Get("kind"))
	switch kind {
	case "ohlc", "":
		kind = "ohlc"
		interval, _ := strconv.Atoi(q.Get("interval"))
		if interval <= 0 {
			interval = 1
		}
		days, _ := strconv.Atoi(q.Get("days"))
		if days <= 0 {
			days = 1
		}
		date := q.Get("date")
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		resp, gtoken, err = p.RawOHLC(ctx, token, interval, date, days)
	case "mbp":
		resp, gtoken, err = p.RawMBP(ctx, token, strings.ToUpper(q.Get("exchange")))
	default:
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": fmt.Sprintf("unknown kind %q (ohlc or mbp)", kind)})
		return
	}
	if err != nil {
		lutJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "kind": kind, "gtoken": gtoken, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "kind": kind, "token": token, "gtoken": gtoken,
		"shape": describeShape(resp, 0),
		"raw":   resp,
	})
}
