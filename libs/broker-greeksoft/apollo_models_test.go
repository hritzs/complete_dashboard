package greeksoft

import "testing"

// Live-captured 2026-09-22 via cmd/apolloprobe (RELIANCE, token 101002885):
// response.BCastTime is a quoted epoch-seconds string, distinct from the
// "DD-MM-YYYY HH:MM:SS" ltt/lut fields inside data.
const liveApolloMarketPictureSample = `{"response":{"svcName":"Broadcast","BCastTime":"1790070820","streaming_type":"marketPicture","data":{"ltp":"1244.50","symbol":"101002885","exch":"NSE","ltt":"22-09-2026 15:14:59","lut":"22-09-2026 15:23:40","name":"RELIANCE"}}}`

func TestClassifyApolloFrame_ParsesBCastTimeFromLiveSample(t *testing.T) {
	frame := classifyApolloFrame([]byte(liveApolloMarketPictureSample))
	if frame.StreamingType != "marketPicture" {
		t.Fatalf("StreamingType = %q, want marketPicture", frame.StreamingType)
	}
	if frame.BCastTime != 1790070820 {
		t.Fatalf("BCastTime = %d, want 1790070820", frame.BCastTime)
	}
}

func TestParseApolloEpochField(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"quoted string (live format)", `"1790070820"`, 1790070820},
		{"bare number", `1790070820`, 1790070820},
		{"empty", ``, 0},
		{"null", `null`, 0},
		{"garbage", `"not-a-number"`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseApolloEpochField([]byte(c.raw))
			if got != c.want {
				t.Fatalf("parseApolloEpochField(%q) = %d, want %d", c.raw, got, c.want)
			}
		})
	}
}
