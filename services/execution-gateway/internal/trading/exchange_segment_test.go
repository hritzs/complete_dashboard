package trading

import "testing"

func TestResolveExchangeSegment(t *testing.T) {
	for _, c := range []struct{ symbol, requested, want string }{
		{"SENSEX", "NSEFO", "BSEFO"}, // the UI default must not send a BSE token to NSE
		{"BANKEX", "NSEFO", "BSEFO"},
		{"SENSEX", "", "BSEFO"},
		{"sensex", "nsefo", "BSEFO"},
		{"NIFTY", "NSEFO", "NSEFO"},
		{"NIFTY", "", "NSEFO"},
		{"BANKNIFTY", "", "NSEFO"},
	} {
		if got := ResolveExchangeSegment(c.symbol, c.requested); got != c.want {
			t.Errorf("ResolveExchangeSegment(%q, %q) = %q, want %q", c.symbol, c.requested, got, c.want)
		}
	}
}
