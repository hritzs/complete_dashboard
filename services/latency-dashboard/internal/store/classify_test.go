package store

import "testing"

func TestClassifySource(t *testing.T) {
	cases := []struct {
		iris, total int64
		want        string
	}{
		{2, 2, "IRIS_WS"},
		{1, 3, "IRIS_WS"},
		{0, 2, "REST_ONLY"},
		{0, 0, "NONE"},
	}
	for _, c := range cases {
		if got := classifySource(c.iris, c.total); got != c.want {
			t.Fatalf("classifySource(%d,%d) = %s, want %s", c.iris, c.total, got, c.want)
		}
	}
}
