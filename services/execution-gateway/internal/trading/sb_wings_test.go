package trading

import (
	"context"
	"strings"
	"testing"
	"time"
)

// sbWingChain: NIFTY strikes 22000-24000 every 50, CE token = strike*10+1,
// PE token = strike*10+2.
type sbWingChain struct{}

func (sbWingChain) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	c := &OptionChainSnapshot{Symbol: "NIFTY", ATM: 22950, Expiry: "19-OCT-26", LotSize: 65, SyntheticFuture: 22950}
	for k := 22000.0; k <= 24000; k += 50 {
		c.Chain = append(c.Chain, OptionChainRow{Strike: k, CEToken: int64(k)*10 + 1, PEToken: int64(k)*10 + 2, CELtp: 5, PELtp: 5, CEBid: 4.9, CEAsk: 5.1, PEBid: 4.9, PEAsk: 5.1})
	}
	return c, nil
}
func (sbWingChain) PushSnapshot(ctx context.Context, snap TradeSnapshot) error { return nil }

func sbWingService(t *testing.T, wingPct float64) (*Service, *liveHedgeExecutor, *sbRunner) {
	t.Helper()
	store := NewMemoryStore()
	tr := StoredTrade{TradeUID: "SB-W-" + t.Name(), UserID: sbLiveUser, BrokerName: sbLiveBroker, AccountID: "147",
		Symbol: "NIFTY", Expiry: "19-OCT-26", LotSize: 65, Lots: 2, Status: sbStatusLive}
	tr.Config.WingPct = wingPct
	store.SaveTrade(tr)
	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := &liveHedgeExecutor{registry: registry}
	svc := &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}, OrderEvents: registry,
		Snapshot: sbWingChain{}, freezeQtyBySymbol: map[string]int64{"NIFTY": 1755}}
	r := newSBRunner(SBConfig{ID: "W", Symbol: "NIFTY", TargetStraddle: 200, Straddles: 130, WingPct: wingPct})
	r.isLive, r.tradeUID, r.lotSize = true, tr.TradeUID, 65
	return svc, exec, r
}

func (e *liveHedgeExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.submitted)
}

// A LIVE run with wings buys them after a confirmed sell: CE at strike + 2 %,
// PE at strike - 2 %, WING phase, the sold quantity -- in the background.
func TestSBWingsAfterFill_BuysWings(t *testing.T) {
	svc, exec, r := sbWingService(t, 2)
	svc.sbWingsAfterFill(r, 130, 65, 22950, 22950, "test tranche")
	deadline := time.Now().Add(3 * time.Second)
	for exec.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.submitted) != 2 {
		t.Fatalf("wing orders = %d, want 2 (one CE, one PE)", len(exec.submitted))
	}
	for _, in := range exec.submitted {
		if in.Phase != wingPhase || in.Side != "BUY" || in.OrderType != "MARKET" {
			t.Fatalf("wing order %+v: want phase WING, BUY, MARKET", in)
		}
		switch in.LegType {
		case "CE":
			if in.Quantity != 130 || in.Token != 234001 { // 22950 x 1.02 = 23409 -> 23400
				t.Fatalf("CE wing qty %d token %d, want 130 at 23400 (234001)", in.Quantity, in.Token)
			}
		case "PE":
			if in.Quantity != 65 || in.Token != 225002 { // 22950 x 0.98 = 22491 -> 22500
				t.Fatalf("PE wing qty %d token %d, want 65 at 22500 (225002)", in.Quantity, in.Token)
			}
		default:
			t.Fatalf("unexpected leg %q", in.LegType)
		}
	}
}

// No wings: a SHADOW run, a run with wing % 0, or nothing filled.
func TestSBWingsAfterFill_NoOrders(t *testing.T) {
	svc, exec, r := sbWingService(t, 2)
	r.isLive = false
	svc.sbWingsAfterFill(r, 130, 65, 22950, 22950, "shadow")
	svc.sbWingsAfterFill(&sbRunner{isLive: true, tradeUID: r.tradeUID}, 0, 0, 22950, 22950, "nothing filled")
	svc0, exec0, r0 := sbWingService(t, 0)
	svc0.sbWingsAfterFill(r0, 130, 65, 22950, 22950, "no wings")
	time.Sleep(200 * time.Millisecond)
	if exec.count() != 0 || exec0.count() != 0 {
		t.Fatalf("wing orders sent: %d / %d, want none", exec.count(), exec0.count())
	}
}

// The wing % is validated on the rule and stays on the run's trade.
func TestSBRuleWingPctValidated(t *testing.T) {
	t.Setenv("SBUILD_RULES_PATH", t.TempDir()+"/sbuild_rules.json")
	s := &Service{}
	if _, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 290, Straddles: 650, WingPct: 25}); err == nil || !strings.Contains(err.Error(), "wings") {
		t.Fatalf("wing %% 25: err %v, want refused on wings (max 20)", err)
	}
	if _, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 290, Straddles: 650, WingPct: -1}); err == nil || !strings.Contains(err.Error(), "wings") {
		t.Fatalf("wing %% -1: err %v, want refused on wings", err)
	}
	c, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 290, Straddles: 650, WingPct: 2})
	if err != nil || c.WingPct != 2 {
		t.Fatalf("wing %% 2: err %v saved %v", err, c.WingPct)
	}
	defer sbEng.remove(c.ID)
	st := &Service{Store: NewMemoryStore()}
	tr, err := st.sbLiveCreateTrade(c, "19-OCT-26", 65)
	if err != nil || tr.Config.WingPct != 2 {
		t.Fatalf("run trade wing %% = %v (err %v), want 2", tr.Config.WingPct, err)
	}
}
