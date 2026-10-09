package trading

// Hedge count and history per trade: every hedge attempt (the minute-end
// AUTO hedge and every manual one -- all go through ManualHedgeLots) is
// saved on the trade (Config.HedgeCount / HedgeEvents, in the trades row)
// and as a platform_events row ("hedge").

import (
	"encoding/json"
	"log"
	"time"
)

// hedgeTriggerKey marks a hedge started by the minute-end check (value =
// the decision's reason); absent = manual.
type hedgeTriggerKey struct{}

const maxHedgeEvents = 500

// recordHedge saves one attempt. Caller holds the trade lock.
func (s *Service) recordHedge(tradeUID string, ev HedgeEvent, err error) {
	switch {
	case err == nil && ev.CEFilled+ev.PEFilled > 0:
		ev.Result = "FILLED"
	case ev.CEFilled+ev.PEFilled > 0:
		ev.Result = "PARTIAL"
	case err != nil && len(err.Error()) >= 13 && err.Error()[:13] == "hedge refused":
		ev.Result = "REFUSED"
	default:
		ev.Result = "FAILED"
	}
	if err != nil {
		ev.Error = err.Error()
	}
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return
	}
	if ev.CEFilled+ev.PEFilled > 0 {
		tr.Config.HedgeCount++
	}
	tr.Config.HedgeEvents = append(tr.Config.HedgeEvents, ev)
	if n := len(tr.Config.HedgeEvents); n > maxHedgeEvents {
		tr.Config.HedgeEvents = tr.Config.HedgeEvents[n-maxHedgeEvents:]
	}
	tr.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(tr)
	if b, jerr := json.Marshal(map[string]interface{}{"trade_uid": tradeUID, "hedge_count": tr.Config.HedgeCount, "event": ev}); jerr == nil {
		stateEvent("hedge", tradeUID, time.Now().In(lutIST()).Format("2006-01-02"), b)
	}
	log.Printf("[HEDGE-RECORD] trade=%s #%d %s %s: %d lot(s) CE %s %d / PE %s %d at %.0f, delta before %+.2f, %d tranche(s)%s",
		tradeUID, tr.Config.HedgeCount, ev.Trigger, ev.Result, ev.Lots, ev.CESide, ev.CEFilled, ev.PESide, ev.PEFilled, ev.Strike, ev.DeltaBefore, ev.Tranches,
		map[bool]string{true: " -- " + ev.Error, false: ""}[ev.Error != ""])
}
