package trading

import "testing"

func TestBuildLegRatioSatisfied(t *testing.T) {
	tests := []struct {
		name                     string
		requestedCE, requestedPE int64
		verifiedCE, verifiedPE   int64
		want                     bool
	}{
		{name: "fully matched", requestedCE: 100, requestedPE: 100, verifiedCE: 100, verifiedPE: 100, want: true},
		{name: "equal partial fill", requestedCE: 100, requestedPE: 100, verifiedCE: 50, verifiedPE: 50, want: true},
		{name: "ce without pe blocked", requestedCE: 100, requestedPE: 100, verifiedCE: 100, verifiedPE: 0, want: false},
		{name: "pe without ce blocked", requestedCE: 100, requestedPE: 100, verifiedCE: 0, verifiedPE: 100, want: false},
		{name: "delta neutral ratio allowed", requestedCE: 120, requestedPE: 60, verifiedCE: 60, verifiedPE: 30, want: true},
		{name: "delta neutral ratio mismatch blocked", requestedCE: 120, requestedPE: 60, verifiedCE: 90, verifiedPE: 30, want: false},
		{name: "no exposure is okay", requestedCE: 100, requestedPE: 100, verifiedCE: 0, verifiedPE: 0, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildLegRatioSatisfied(tt.requestedCE, tt.requestedPE, tt.verifiedCE, tt.verifiedPE); got != tt.want {
				t.Fatalf("buildLegRatioSatisfied(%d,%d,%d,%d) = %v, want %v", tt.requestedCE, tt.requestedPE, tt.verifiedCE, tt.verifiedPE, got, tt.want)
			}
		})
	}
}

func TestMemoryStoreDeleteTrade(t *testing.T) {
	store := NewMemoryStore()
	store.SaveTrade(StoredTrade{TradeUID: "T-1"})
	store.DeleteTrade("T-1")
	if _, ok := store.LoadTrade("T-1"); ok {
		t.Fatal("DeleteTrade did not remove the trade")
	}
}
