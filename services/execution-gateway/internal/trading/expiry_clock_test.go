package trading

import (
	"math"
	"testing"
	"time"
)

// Values worked by hand from services/feed-decoder/src/chain_builder.cpp calculate_dte.
func TestFeedDTEMatchesTheDecoder(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{{Date: "2026-11-08", Name: "Diwali Muhurat", PreOpen: "14:05", Open: "14:20", Close: "15:40"}})
	ist := lutIST()
	exp := time.Date(2026, 11, 10, 0, 0, 0, 0, ist)
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	for _, c := range []struct {
		name string
		at   time.Time
		want float64
	}{
		{"normal day 13:00 -> 4 days + 160/385", time.Date(2026, 11, 6, 13, 0, 0, 0, ist), 4 + 160.0/385},
		{"normal day 15:30 -> 10/385 left", time.Date(2026, 11, 6, 15, 30, 0, 0, ist), 4 + 10.0/385},
		{"normal day before the open -> + a full day", time.Date(2026, 11, 6, 8, 0, 0, 0, ist), 5},
		{"normal day 15:40 -> nothing left today", time.Date(2026, 11, 6, 15, 40, 0, 0, ist), 4},
		{"Muhurat 14:30 -> 2 days + 70/80 (the session is the whole day)", time.Date(2026, 11, 8, 14, 30, 0, 0, ist), 2 + 70.0/80},
		{"Muhurat at its open -> a whole day ahead", time.Date(2026, 11, 8, 14, 20, 0, 0, ist), 3},
		{"Muhurat before its open -> a whole day ahead", time.Date(2026, 11, 8, 14, 0, 0, 0, ist), 3},
		{"Muhurat 15:37 (exit) -> 3/80", time.Date(2026, 11, 8, 15, 37, 0, 0, ist), 2 + 3.0/80},
		{"Muhurat after its close", time.Date(2026, 11, 8, 15, 45, 0, 0, ist), 2},
		{"expiry day 15:00 -> 40/385", time.Date(2026, 11, 10, 15, 0, 0, 0, ist), 40.0 / 385},
		{"expiry day after the close -> the floor", time.Date(2026, 11, 10, 15, 45, 0, 0, ist), 0.000001},
	} {
		if got, _, _, _, _ := feedDTEDays(c.at, exp); !near(got, c.want) {
			t.Errorf("%s: %.6f, want %.6f", c.name, got, c.want)
		}
	}
	r, ok := expiryClockRow(time.Date(2026, 11, 8, 14, 30, 0, 0, ist), "10-NOV-26")
	if !ok || !near(r.NormalDTE, 2+70.0/385) || !near(r.LUTDTE, 2+70.0/80) || !near(r.Years, (2+70.0/80)/365) || r.MinutesLeft != 70 {
		t.Fatalf("Muhurat row %+v", r)
	}
}

// IV on the feed's clock recovers the vol the prices were made with; at 14:30 a
// normal day has less of the day left (70/385) than the Muhurat day (70/80), so
// the same prices give a higher IV on the normal-day clock.
func TestExpiryClockIVOnTheFeedClock(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{{Date: "2026-11-08", Name: "Diwali Muhurat", Open: "14:20", Close: "15:40"}})
	now := time.Date(2026, 11, 8, 14, 30, 0, 0, lutIST())
	r, ok := expiryClockRow(now, "10-NOV-26")
	if !ok {
		t.Fatal("row")
	}
	F, K, vol := 24750.0, 24750.0, 0.16
	T := r.DTE / 365
	ce, _, _ := simBlack76(true, F, K, T, vol)
	pe, _, _ := simBlack76(false, F, K, T, vol)
	c := &OptionChainSnapshot{Symbol: "NIFTY", Expiry: "10-NOV-26", SyntheticFuture: F, ATM: K,
		Chain: []OptionChainRow{{Strike: K, CELtp: ce, PELtp: pe, CEIV: 16, PEIV: 16}}}
	r.withIV(c)
	if math.Abs(r.IV-vol) > 1e-4 {
		t.Fatalf("IV on the feed clock %.5f, want %.2f", r.IV, vol)
	}
	if !(r.IVNormal > r.IV) || math.Abs(r.FeedIV-0.16) > 1e-9 || r.Gamma >= r.GammaN {
		t.Fatalf("normal-clock IV %.5f gamma %.6f vs %.6f, feed IV %.4f", r.IVNormal, r.Gamma, r.GammaN, r.FeedIV)
	}
}

// One clock: the feed's DTE and the LUT's DTE are the same, normal day and Muhurat.
func TestFeedAndLUTClocksAgree(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{{Date: "2026-11-08", Name: "Diwali Muhurat", Open: "14:20", Close: "15:40"}})
	ist := lutIST()
	exp := time.Date(2026, 11, 10, 0, 0, 0, 0, ist)
	for _, at := range []time.Time{time.Date(2026, 11, 6, 8, 0, 0, 0, ist), time.Date(2026, 11, 6, 11, 7, 30, 0, ist), time.Date(2026, 11, 6, 15, 35, 0, 0, ist),
		time.Date(2026, 11, 8, 14, 5, 0, 0, ist), time.Date(2026, 11, 8, 14, 50, 0, 0, ist), time.Date(2026, 11, 8, 15, 39, 0, 0, ist)} {
		f, _, _, _, _ := feedDTEDays(at, exp)
		if l := lutRawDTE(at, exp); math.Abs(f-l) > 1e-9 {
			t.Errorf("%s: feed %.6f vs LUT %.6f", at.Format("01-02 15:04:05"), f, l)
		}
	}
}
