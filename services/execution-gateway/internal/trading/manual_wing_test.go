package trading

import "testing"

// Wing rows' Add / Close act on the wing part only: ADD buys more, CLOSE
// sells from what is held; a hedge or straddle token is refused.
func TestManualWingLeg(t *testing.T) {
	legs := []OpenLeg{
		{Token: 51356, OptionType: "CE", Strike: 23100, Qty: 11440, WingQty: 11440},    // CE wing
		{Token: 51317, OptionType: "PE", Strike: 22200, Qty: 13520, WingQty: 13520},    // PE wing
		{Token: 51340, OptionType: "CE", Strike: 22650, Qty: -11440, WingQty: 0},       // short straddle CE
		{Token: 51338, OptionType: "CE", Strike: 22600, Qty: 2015 + 130, WingQty: 130}, // hedge + a little wing
	}
	if leg, side, held, err := manualWingLeg(legs, 51356, "CLOSE"); err != nil || side != "SELL" || held != 11440 || leg.Strike != 23100 {
		t.Fatalf("close CE wing: %v %s %d", err, side, held)
	}
	if _, side, _, err := manualWingLeg(legs, 51317, "ADD"); err != nil || side != "BUY" {
		t.Fatalf("add PE wing: %v %s", err, side)
	}
	if _, _, held, err := manualWingLeg(legs, 51338, "CLOSE"); err != nil || held != 130 {
		t.Fatalf("mixed token closes only its wing part: %v %d", err, held)
	}
	if _, _, _, err := manualWingLeg(legs, 51340, "CLOSE"); err == nil {
		t.Fatal("a straddle leg is not a wing")
	}
	if _, _, _, err := manualWingLeg(legs, 99999, "ADD"); err == nil {
		t.Fatal("unknown token")
	}
}
