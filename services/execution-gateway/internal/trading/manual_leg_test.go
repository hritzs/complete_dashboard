package trading

import (
	"context"
	"strings"
	"testing"
)

// A real order is never sent without the typed confirmation, whatever else
// the request says.
func TestManualLeg_RequiresTypedConfirm(t *testing.T) {
	s := &Service{Store: NewMemoryStore()}
	for _, c := range []string{"", "confirm", "YES"} {
		_, err := s.ManualLeg(context.Background(), ManualLegRequest{TradeUID: "T", Action: "ADD", Strike: 22700, OptionType: "CE", Side: "BUY", Lots: 1, Confirm: c})
		if err == nil || !strings.Contains(err.Error(), manualLegConfirm) {
			t.Fatalf("confirm %q: expected a confirmation error, got %v", c, err)
		}
	}
}

// A manual leg moves the wings of its own type: a SELL grows the net short
// (buy wings), a BUY shrinks it (sell held wings).
func TestManualLegWingChange(t *testing.T) {
	cases := []struct {
		side, opt      string
		filled         int64
		wantCE, wantPE int64
	}{
		{"SELL", "CE", 130, 130, 0},
		{"BUY", "CE", 65, -65, 0},
		{"SELL", "PE", 195, 0, 195},
		{"BUY", "pe", 130, 0, -130},
	}
	for _, c := range cases {
		ce, pe := manualLegWingChange(c.side, c.opt, c.filled)
		if ce != c.wantCE || pe != c.wantPE {
			t.Fatalf("%s %s %d: got %d / %d, want %d / %d", c.side, c.opt, c.filled, ce, pe, c.wantCE, c.wantPE)
		}
	}
}

// After an ADD on a build leg, its entry is the fill-based average of the
// non-wing part; legs without an entry keep the current price.
func TestBuildLegEntries(t *testing.T) {
	legs := []OpenLeg{
		{Token: 101, Qty: -11635, EntryPrice: 137.69},
		{Token: 202, Qty: -14430, EntryPrice: 999, WingQty: 1300, RealEntry: 111.44, WingEntry: 5},
		{Token: 303, Qty: 1625, EntryPrice: 103.32},
	}
	ce, pe := buildLegEntries(legs, 101, 202, 136.50, 112.25)
	if ce != 137.69 || pe != 111.44 {
		t.Fatalf("entries %.2f / %.2f, want 137.69 / 111.44", ce, pe)
	}
	if ce, pe := buildLegEntries(nil, 101, 202, 136.50, 112.25); ce != 136.50 || pe != 112.25 {
		t.Fatalf("no legs: %.2f / %.2f, want the current entries kept", ce, pe)
	}
}
