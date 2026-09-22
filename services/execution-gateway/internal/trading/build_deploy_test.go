package trading

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeChainSnapshot struct{}

func (fakeChainSnapshot) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	return &OptionChainSnapshot{
		Symbol: "NIFTY", ATM: 23400, Expiry: "22-SEP-26", LotSize: 65,
		SyntheticSpot: 23405, SyntheticFuture: 23405,
		Chain: []OptionChainRow{{
			Strike: 23400, IsATM: true, CEToken: 111, PEToken: 222,
			CELtp: 100, PELtp: 90, CEDelta: 0.52, PEDelta: -0.48,
		}},
	}, nil
}

func (fakeChainSnapshot) PushSnapshot(ctx context.Context, snap TradeSnapshot) error { return nil }

// An automated (config) build must arrive with its exit time, SL and divisors
// already on the trade, and with the same NRML product a manual GreekSoft
// build gets -- not the hardcoded defaults and forced MIS it used to get.
func TestExecuteFinalBuild_AppliesRiskConfigAndDefaultProduct(t *testing.T) {
	store := NewMemoryStore()
	svc := &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}},
		Snapshot:      fakeChainSnapshot{},
	}

	resp, err := svc.ExecuteFinalBuild(context.Background(), FinalBuildRequest{
		Mode: BuildModeConfig, BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Lots: 1,
		Risk: &BuildRiskConfig{ExitTime: "23:59:00", SlBps: 14, BuyBuffer: 6, SellBuffer: 6, HedgeDiv: 50, StraddleDiv: 3},
	})
	if err != nil {
		t.Fatalf("ExecuteFinalBuild: %v", err)
	}
	t.Cleanup(func() {
		if rt, ok := store.LoadRuntime(resp.TradeUID); ok {
			close(rt.StopCh)
			<-rt.DoneCh
		}
	})

	tr, ok := store.LoadTrade(resp.TradeUID)
	if !ok {
		t.Fatal("built trade not stored")
	}
	c := tr.Config
	if c.SLPnLBpsOfSpot != 14 || c.BuyBuffer != 6 || c.SellBuffer != 6 || c.HedgeDiv != 50 || c.StraddleDiv != 3 {
		t.Fatalf("risk settings not on the built trade: %+v", c)
	}
	if c.SquareOffHardTime.IsZero() || c.SquareOffHardTime.Hour() != 23 || c.SquareOffHardTime.Minute() != 59 {
		t.Fatalf("exit time not on the built trade: %v", c.SquareOffHardTime)
	}
	if tr.ProductType != "NRML" {
		t.Fatalf("product = %q, want NRML (the manual GreekSoft default), not a forced MIS", tr.ProductType)
	}
	_ = time.Now
}

func TestExecuteFinalBuild_BadExitTimePlacesNothing(t *testing.T) {
	exec := &fakeSLExecutor{}
	svc := &Service{
		Store:         NewMemoryStore(),
		BrokerFactory: &fakeBrokerFactory{executor: exec},
		Snapshot:      fakeChainSnapshot{},
	}
	_, err := svc.ExecuteFinalBuild(context.Background(), FinalBuildRequest{
		Mode: BuildModeConfig, BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Lots: 1,
		Risk: &BuildRiskConfig{ExitTime: "junk"},
	})
	if err == nil {
		t.Fatal("want an error for an unparseable exit time")
	}
	if len(exec.submitted) != 0 {
		t.Fatalf("placed %d orders despite invalid risk config", len(exec.submitted))
	}
}

// GetFallbackLotSize logs a loud [FALLBACK BLOCKED] warning by design for
// index symbols -- it is meant to fire only when the real lot size could not
// be resolved and this path was genuinely taken. DeployStraddle's own
// diagnostic log line used to call it a SECOND time purely to fill an unused
// "fallback=%d" field, so the warning fired on every successful deploy even
// though the chain had already resolved the real lot size moments earlier.
func TestDeployStraddle_DoesNotLogFallbackWarningWhenChainResolvesLotSize(t *testing.T) {
	store := NewMemoryStore()
	svc := &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}},
		Snapshot:      fakeChainSnapshot{}, // LotSize: 65, a real resolved value
	}

	out := captureLog(t, func() {
		_, err := svc.DeployStraddle(context.Background(), DeployStraddleRequest{
			BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Lots: 1,
		})
		if err != nil {
			t.Fatalf("DeployStraddle: %v", err)
		}
	})

	if strings.Contains(out, "FALLBACK BLOCKED") {
		t.Fatalf("logged a false FALLBACK BLOCKED warning although the chain resolved a real lot size:\n%s", out)
	}
	if !strings.Contains(out, "LOT SIZE RESOLVED") || !strings.Contains(out, "LotSize: 65") {
		t.Fatalf("expected a LOT SIZE RESOLVED line with LotSize: 65:\n%s", out)
	}
}
