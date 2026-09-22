package trading

import "testing"

// Confirmed live 2026-09-22: a 40-lot NIFTY build filled ~24 lots of CE
// before every remaining order -- including all of PE, whose clips had not
// even started yet -- was rejected for insufficient margin, leaving a real,
// naked, unhedged CE position. GenerateExplicitClips must interleave legs so
// a cutoff partway through never leaves one leg far ahead of the other.
func TestGenerateExplicitClips_InterleavesLegsInsteadOfSequencingThem(t *testing.T) {
	legs := []LegData{
		{Token: 111, Symbol: "NIFTY", OptionType: "CE", Action: "SELL", TotalLots: 4, LotSize: 65, ExpectedPrice: 50},
		{Token: 222, Symbol: "NIFTY", OptionType: "PE", Action: "SELL", TotalLots: 4, LotSize: 65, ExpectedPrice: 40},
	}

	clips, err := GenerateExplicitClips("BUI_T1", legs, 1, 10000)
	if err != nil {
		t.Fatalf("GenerateExplicitClips: %v", err)
	}
	if len(clips) != 8 {
		t.Fatalf("clips = %d, want 8 (4 CE + 4 PE)", len(clips))
	}

	wantOptionTypes := []string{"CE", "PE", "CE", "PE", "CE", "PE", "CE", "PE"}
	for i, clip := range clips {
		if len(clip) != 1 {
			t.Fatalf("clip %d has %d orders, want 1", i, len(clip))
		}
		if clip[0].OptionType != wantOptionTypes[i] {
			t.Fatalf("clip %d option_type = %s, want %s (sequence: %v)", i, clip[0].OptionType, wantOptionTypes[i], optionTypeSequence(clips))
		}
	}
}

// An uneven CE/PE lot count (e.g. a hedge-adjusted build) must still
// interleave as far as it can, only falling back to sequential once the
// smaller leg is exhausted -- not stay sequential from the start.
func TestGenerateExplicitClips_UnevenLegsInterleaveThenTrail(t *testing.T) {
	legs := []LegData{
		{Token: 111, Symbol: "NIFTY", OptionType: "CE", Action: "SELL", TotalLots: 5, LotSize: 65, ExpectedPrice: 50},
		{Token: 222, Symbol: "NIFTY", OptionType: "PE", Action: "SELL", TotalLots: 2, LotSize: 65, ExpectedPrice: 40},
	}

	clips, err := GenerateExplicitClips("BUI_T1", legs, 1, 10000)
	if err != nil {
		t.Fatalf("GenerateExplicitClips: %v", err)
	}

	want := []string{"CE", "PE", "CE", "PE", "CE", "CE", "CE"}
	got := optionTypeSequence(clips)
	if len(got) != len(want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", got, want)
		}
	}
}

func optionTypeSequence(clips [][]ExecOrder) []string {
	out := make([]string, len(clips))
	for i, c := range clips {
		if len(c) > 0 {
			out[i] = c[0].OptionType
		}
	}
	return out
}
