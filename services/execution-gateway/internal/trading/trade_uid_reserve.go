package trading

import (
	"fmt"
	"log"
	"sync"
)

var (
	tradeUIDMu       sync.Mutex
	tradeUIDInFlight = map[string]bool{}
)

// reserveTradeUID returns uid, or uid_2, uid_3 ... if uid is already used
// by a stored trade or by a build in flight in this process.
func (s *Service) reserveTradeUID(uid string) string {
	tradeUIDMu.Lock()
	defer tradeUIDMu.Unlock()
	cand := uid
	for n := 2; ; n++ {
		taken := tradeUIDInFlight[cand]
		if !taken && s.Store != nil {
			_, taken = s.Store.LoadTrade(cand)
		}
		if !taken {
			break
		}
		cand = fmt.Sprintf("%s_%d", uid, n)
	}
	if cand != uid {
		log.Printf("[BUILD] trade uid %s already in use -- this build is %s (its own trade)", uid, cand)
	}
	tradeUIDInFlight[cand] = true
	return cand
}

func releaseTradeUID(uid string) {
	tradeUIDMu.Lock()
	delete(tradeUIDInFlight, uid)
	tradeUIDMu.Unlock()
}
