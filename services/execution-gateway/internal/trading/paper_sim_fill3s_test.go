package trading

import (
	"math"
	"testing"
)

func TestSimFill3s(t *testing.T) {
	m := simMinute{Time: "09:23", secs: []simSec{{T: "09:23:01", F: 22716.25}, {T: "09:23:02", F: 22719.0}, {T: "09:23:03", F: 22720.2}}}
	// minute-end: the close at :00 plus :01 and :02
	if got, want := simFill3s(m, "09:23:00", 22717.15), (22717.15+22716.25+22719.0)/3; math.Abs(got-want) > 1e-9 {
		t.Fatalf("minute-end fill %.4f, want %.4f", got, want)
	}
	// intra breach at :02: :02, :03 and nothing after -> two seconds
	if got, want := simFill3s(m, "09:23:02", 22719.0), (22719.0+22720.2)/2; math.Abs(got-want) > 1e-9 {
		t.Fatalf("intra fill %.4f, want %.4f", got, want)
	}
	// no recorded seconds: the trigger price
	if got := simFill3s(simMinute{}, "09:23:00", 22717.15); got != 22717.15 {
		t.Fatalf("no seconds: %.4f", got)
	}
}
