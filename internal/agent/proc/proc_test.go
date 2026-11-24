package proc

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEnv(t *testing.T) {
	t.Setenv("PATH", "/bin")
	t.Setenv("SECRET_TOKEN", "s")
	got := Env(map[string]string{"HOME": "/h", "EMPTY": ""}, map[string]string{"X": "1", "DROP": "d"}, "DROP")
	want := []string{"HOME=/h", "PATH=/bin", "X=1"}
	for _, kv := range got {
		if strings.HasPrefix(kv, "SECRET_TOKEN=") || strings.HasPrefix(kv, "DROP=") || strings.HasPrefix(kv, "EMPTY=") {
			t.Errorf("unexpected %s", kv)
		}
	}
	for _, kv := range want {
		if !slices.Contains(got, kv) {
			t.Errorf("missing %s in %q", kv, got)
		}
	}
}

func TestTruncate(t *testing.T) {
	s := strings.Repeat("é", 2000) // 4000 bytes
	got := Truncate(s, 2047)
	if !utf8.ValidString(got) || !strings.Contains(got, "truncated") || len(got) > 2100 {
		t.Errorf("bad truncation: %d bytes", len(got))
	}
	if Truncate("short", 10) != "short" {
		t.Error("short string changed")
	}
}

func TestLinesSplitOnLFOnly(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	p, err := Start(Options{
		Argv:     []string{"sh", "-c", `printf 'a b\r\nc\n\nd'`},
		Env:      []string{"PATH=" + pathEnv(t)},
		OnStdout: func(b []byte) { mu.Lock(); lines = append(lines, string(b)); mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 3 || lines[1] != "c" || lines[2] != "d" || strings.HasSuffix(lines[0], "\r") {
		t.Errorf("lines = %q", lines)
	}
	if p.PID() != 0 {
		t.Error("PID after exit")
	}
}

func TestEmitterOrderAndFinish(t *testing.T) {
	e := NewEmitter()
	for i := 0; i < 500; i++ { // more than the channel buffer, nobody reading yet
		e.Emit(eventCost(float64(i)))
	}
	e.Finish(eventCost(-1))
	if e.Emit(eventCost(9)) {
		t.Error("Emit after Finish accepted")
	}
	i := 0
	for ev := range e.C() {
		if ev.Done {
			if ev.Cost != -1 || i != 500 {
				t.Errorf("done after %d events: %+v", i, ev)
			}
			continue
		}
		if ev.Cost != float64(i) {
			t.Fatalf("event %d out of order: %v", i, ev.Cost)
		}
		i++
	}
}
