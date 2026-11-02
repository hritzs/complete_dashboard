package trading

import (
	"testing"
	"time"
)

func TestFeedCloseOK(t *testing.T) {
	b := time.Unix(1_800_000_000, 0)
	chain := func(ce, pe int64) *OptionChainSnapshot {
		return &OptionChainSnapshot{Chain: []OptionChainRow{{Strike: 23000, CEToken: 1, PEToken: 2, CELtp: 100, PELtp: 90, CELTT: ce, PELTT: pe}}}
	}
	core := map[int64]bool{1: true, 2: true}
	if !feedCloseOK(chain(b.Unix()-2, b.Unix()-5), b, core) {
		t.Error("a fresh ATM pair must use the feed")
	}
	if feedCloseOK(chain(b.Unix()-2, b.Unix()-60), b, core) {
		t.Error("one stale leg must fall back to GreekSoft")
	}
	c := chain(b.Unix()-2, b.Unix()-2)
	c.Chain[0].PELtp = 0
	if feedCloseOK(c, b, core) {
		t.Error("a missing price must fall back to GreekSoft")
	}
	if feedCloseOK(chain(b.Unix(), b.Unix()), b, map[int64]bool{9: true}) || feedCloseOK(nil, b, core) {
		t.Error("no matching tokens or no chain must fall back to GreekSoft")
	}
}

func TestMinuteClosePrimarySwitch(t *testing.T) {
	b := time.Unix(1_800_000_000, 0)
	c := &OptionChainSnapshot{Chain: []OptionChainRow{{CEToken: 1, CELtp: 100, CELTT: b.Unix()}}}
	core := map[int64]bool{1: true}
	t.Setenv("MINUTE_CLOSE_PRIMARY", "")
	if !useFeedClose(c, b, core) {
		t.Error("the feed must be the default main source")
	}
	t.Setenv("MINUTE_CLOSE_PRIMARY", "gs")
	if useFeedClose(c, b, core) {
		t.Error("MINUTE_CLOSE_PRIMARY=gs must put GreekSoft first")
	}
}
