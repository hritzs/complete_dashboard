package trading

import (
	"testing"
	"time"
)

// 2026-10-07 09:17 IST: GreekSoft's straddle was the 09:16-09:17 candle
// close (CE 163.05 + PE 155.75), not the LTP seen at 09:17:00.034.
func TestLUTCloseChainUsesLastTradeBeforeBoundary(t *testing.T) {
	b := time.Date(2026, 10, 7, 9, 17, 0, 0, lutIST())
	bs := b.Unix()
	chain := &OptionChainSnapshot{SyntheticFuture: 22607.30, Chain: []OptionChainRow{{
		Strike: 22600, CELtp: 162.90, PELtp: 157.45,
		// CE: closed at 163.05 (09:16:59), 162.90 traded at 09:17:00.
		CELTT: bs, CEClose: 163.05, CECloseLTT: bs - 1, CECloseNext: bs,
		// PE: 155.75 at 09:16:59 then 157.45 at 09:17:00.
		PELTT: bs, PEClose: 155.75, PECloseLTT: bs - 1, PECloseNext: bs,
	}}}
	cc, ready := lutCloseChain(chain, b, b.Add(300*time.Millisecond))
	if !ready {
		t.Fatal("both ATM closes are final -- should be ready")
	}
	r := cc.Chain[0]
	if r.CELtp != 163.05 || r.PELtp != 155.75 || r.CELtp+r.PELtp != 318.80 {
		t.Fatalf("closes CE %v PE %v", r.CELtp, r.PELtp)
	}
	if want := 22600 + 163.05 - 155.75; cc.SyntheticFuture != want {
		t.Fatalf("synthetic %v want %v", cc.SyntheticFuture, want)
	}
	if chain.Chain[0].CELtp != 162.90 {
		t.Fatal("live chain must not be modified")
	}
}

func TestLUTCloseChainWaitsForFinalOrGrace(t *testing.T) {
	b := time.Date(2026, 10, 7, 9, 18, 0, 0, lutIST())
	bs := b.Unix()
	// No trade since the boundary yet: provisional close = last trade.
	chain := &OptionChainSnapshot{SyntheticFuture: 22611, Chain: []OptionChainRow{{
		Strike: 22600, CELtp: 166.80, PELtp: 155.85, CELTT: bs - 1, PELTT: bs - 2,
	}}}
	if _, ready := lutCloseChain(chain, b, b.Add(lutCloseGrace/2)); ready {
		t.Fatal("not final and inside the grace -- must wait")
	}
	cc, ready := lutCloseChain(chain, b, b.Add(lutCloseGrace))
	if !ready || cc.Chain[0].CELtp != 166.80 || cc.Chain[0].PELtp != 155.85 {
		t.Fatalf("after grace: ready %v CE %v PE %v", ready, cc.Chain[0].CELtp, cc.Chain[0].PELtp)
	}
}

func TestLUTCloseChainOldDecoderUnchanged(t *testing.T) {
	chain := &OptionChainSnapshot{SyntheticFuture: 22611, Chain: []OptionChainRow{{Strike: 22600, CELtp: 166.8, PELtp: 155.85}}}
	b := time.Date(2026, 10, 7, 9, 18, 0, 0, lutIST())
	if cc, ready := lutCloseChain(chain, b, b); !ready || cc != chain {
		t.Fatal("no trade times: record the chain as is, immediately")
	}
}

// 2026-10-07 09:18 was recorded with the old rounded TP (12); refreshed
// from its own inputs it is 11.4918 bps, the answer unchanged.
func TestLUTRefreshTPFromRecordedInputs(t *testing.T) {
	ev := LUTEvaluation{AdjBuildIV: 0.13985943325167563, TradingDTE: 5.992206411639957, TPBps: 12, Allowed: true}
	if !lutRefreshTP(&ev) || ev.TPBps < 11.4917 || ev.TPBps > 11.4919 || !ev.Allowed {
		t.Fatalf("TP %v allowed %v", ev.TPBps, ev.Allowed)
	}
	if lutRefreshTP(&ev) {
		t.Fatal("second refresh must be a no-op")
	}
}
