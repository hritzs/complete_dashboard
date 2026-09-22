package trading

import "testing"

func TestPlanBuildPairRoundsPE53CE47(t *testing.T) {
	rounds, err := PlanBuildPairRounds(47, 53)
	if err != nil {
		t.Fatalf("PlanBuildPairRounds: %v", err)
	}

	if len(rounds) != 47 {
		t.Fatalf("round count = %d, want 47", len(rounds))
	}

	totalCE := 0
	totalPE := 0
	extraPERounds := []int{}

	for _, round := range rounds {
		totalCE += round.CELots
		totalPE += round.PELots

		if !round.HasExcessLot {
			continue
		}

		switch round.ExcessSide {
		case "PE":
			totalPE++
			extraPERounds = append(extraPERounds, round.Index)

		case "CE":
			totalCE++

		default:
			t.Fatalf(
				"unexpected excess side %q",
				round.ExcessSide,
			)
		}
	}

	if totalCE != 47 {
		t.Fatalf("CE total = %d, want 47", totalCE)
	}

	if totalPE != 53 {
		t.Fatalf("PE total = %d, want 53", totalPE)
	}

	wantRounds := []int{8, 16, 24, 32, 40, 47}

	if len(extraPERounds) != len(wantRounds) {
		t.Fatalf(
			"extra PE rounds = %v, want %v",
			extraPERounds,
			wantRounds,
		)
	}

	for i := range wantRounds {
		if extraPERounds[i] != wantRounds[i] {
			t.Fatalf(
				"extra PE rounds = %v, want %v",
				extraPERounds,
				wantRounds,
			)
		}
	}
}

func TestPlanBuildPairRoundsCE53PE47(t *testing.T) {
	rounds, err := PlanBuildPairRounds(53, 47)
	if err != nil {
		t.Fatalf("PlanBuildPairRounds: %v", err)
	}

	totalCE := 0
	totalPE := 0
	extraCE := 0

	for _, round := range rounds {
		totalCE += round.CELots
		totalPE += round.PELots

		if !round.HasExcessLot {
			continue
		}

		switch round.ExcessSide {
		case "CE":
			totalCE++
			extraCE++
		case "PE":
			totalPE++
		}
	}

	if totalCE != 53 || totalPE != 47 {
		t.Fatalf(
			"totals CE=%d PE=%d, want CE=53 PE=47",
			totalCE,
			totalPE,
		)
	}

	if extraCE != 6 {
		t.Fatalf(
			"extra CE lots = %d, want 6",
			extraCE,
		)
	}
}

func TestPlanBuildPairRoundsEqual(t *testing.T) {
	rounds, err := PlanBuildPairRounds(10, 10)
	if err != nil {
		t.Fatalf("PlanBuildPairRounds: %v", err)
	}

	if len(rounds) != 10 {
		t.Fatalf("round count = %d, want 10", len(rounds))
	}

	for _, round := range rounds {
		if round.HasExcessLot {
			t.Fatalf(
				"round %d unexpectedly contains excess %s",
				round.Index,
				round.ExcessSide,
			)
		}
	}
}

func TestPlanBuildPairRoundsRejectsMissingLeg(t *testing.T) {
	tests := []struct {
		ce int
		pe int
	}{
		{0, 5},
		{5, 0},
		{0, 0},
		{-1, 5},
		{5, -1},
	}

	for _, tc := range tests {
		if _, err := PlanBuildPairRounds(tc.ce, tc.pe); err == nil {
			t.Fatalf(
				"CE=%d PE=%d: expected error",
				tc.ce,
				tc.pe,
			)
		}
	}
}
