package trading

import "testing"

func TestHedgeCutPlan(t *testing.T) {
	chain := &OptionChainSnapshot{Chain: []OptionChainRow{
		{Strike: 22950, CEToken: 1, PEToken: 2, CEDelta: 0.72, PEDelta: -0.28},
		{Strike: 23100, CEToken: 3, PEToken: 4, CEDelta: 0.53, PEDelta: -0.47},
	}}
	legs := []mtmLeg{
		{Leg: "CE", Token: 1, Strike: 22950, NetShort: 1300},
		{Leg: "PE", Token: 2, Strike: 22950, NetShort: 1300},
		{Leg: "CE", Token: 3, Strike: 23100, NetShort: -650}, // long: never cut
		{Leg: "PE", Token: 4, Strike: 23100, NetShort: 650},
	}
	// Need +delta 1000 -> buy back short CE only (highest delta 0.72): 1000/0.72 = 1389 -> 21 lots = 1365, capped at 1300 held.
	p := hedgeCutPlan(legs, chain, "CE", 1000, 65)
	if len(p) != 1 || p[0].leg.Token != 1 || p[0].qty != 1300 {
		t.Fatalf("+delta plan %+v, want buy back 1300 of CE 22950", p)
	}
	// Need -delta 400 -> short PEs, highest |delta| first: PE 23100 (0.47): 400/0.47 = 851 -> 13 lots = 845, capped at 650;
	// then PE 22950 (0.28) for the rest: (400 - 650*0.47) / 0.28 = 337 -> 5 lots = 325.
	p = hedgeCutPlan(legs, chain, "PE", 400, 65)
	if len(p) != 2 || p[0].leg.Token != 4 || p[0].qty != 650 || p[1].leg.Token != 2 || p[1].qty != 325 {
		t.Fatalf("-delta plan %+v", p)
	}
	// Less than half a lot of delta: nothing.
	if p := hedgeCutPlan(legs, chain, "CE", 20, 65); len(p) != 0 {
		t.Fatalf("tiny need should cut nothing: %+v", p)
	}
}
