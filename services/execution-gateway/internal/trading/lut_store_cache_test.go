package trading

import (
	"os"
	"path/filepath"
	"testing"
)

// The chain file cache reads appended lines only, ignores a partial last
// line until it is complete, and reads a rewritten file in full.
func TestLUTChainFileCache(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x_chain.jsonl")
	write := func(s string, app bool) {
		fl := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if app {
			fl = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, _ := os.OpenFile(p, fl, 0o644)
		_, _ = f.WriteString(s)
		_ = f.Close()
	}
	write(`{"t":"09:15","f":1}`+"\n", false)
	if m := lutLoadChainFile(p); len(m) != 1 || m["09:15"].Future != 1 {
		t.Fatalf("first %+v", m)
	}
	write(`{"t":"09:16","f":2}`+"\n"+`{"t":"09:17","f"`, true) // partial last line
	if m := lutLoadChainFile(p); len(m) != 2 || m["09:16"].Future != 2 {
		t.Fatalf("append %+v", m)
	}
	write(`:3}`+"\n", true)
	if m := lutLoadChainFile(p); len(m) != 3 || m["09:17"].Future != 3 {
		t.Fatalf("completed line %+v", m)
	}
	write(`{"t":"09:20","f":9}`+"\n", false) // rewritten
	if m := lutLoadChainFile(p); len(m) != 1 || m["09:20"].Future != 9 {
		t.Fatalf("rewrite %+v", m)
	}
}
