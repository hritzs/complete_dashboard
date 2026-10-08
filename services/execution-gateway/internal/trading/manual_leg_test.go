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
