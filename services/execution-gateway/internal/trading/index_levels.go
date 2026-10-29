package trading

// Index levels for the UI's NIFTY-equivalent figures: a SENSEX straddle or
// P&L per straddle x (NIFTY level / SENSEX level) reads on NIFTY's scale.
// Levels are each chain's synthetic future (put-call parity at the ATM),
// not future_ltp, which can be a later month carrying cost-of-carry.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

var indexLevelCache struct {
	mu     sync.Mutex
	at     time.Time
	levels map[string]float64
}

// IndexLevelsHandler: GET /api/index/levels -> {"levels": {"NIFTY": .., "SENSEX": ..}}
func (h *Handlers) IndexLevelsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	indexLevelCache.mu.Lock()
	defer indexLevelCache.mu.Unlock()
	if time.Since(indexLevelCache.at) > time.Second && h.Service != nil && h.Service.Snapshot != nil {
		levels := map[string]float64{}
		for _, sym := range []string{"NIFTY", "SENSEX", "BANKNIFTY"} {
			ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
			c, err := h.Service.Snapshot.GetOptionChain(ctx, sym, "")
			cancel()
			if err != nil || c == nil {
				continue
			}
			if v := c.SyntheticFuture; v > 0 {
				levels[sym] = v
			} else if c.FutureLtp > 0 {
				levels[sym] = c.FutureLtp
			}
		}
		if len(levels) > 0 {
			indexLevelCache.levels, indexLevelCache.at = levels, time.Now()
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "levels": indexLevelCache.levels})
}
