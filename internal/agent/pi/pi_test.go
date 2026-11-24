package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

func start(t *testing.T, mode string) (agent.Session, string) {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "record.json")
	spec := agent.Spec{
		Model: "anthropic/claude-sonnet-4", Effort: "high", Prompt: "do it",
		SystemPrompt: "be brief", Resume: "/s.jsonl",
		Workdir: dir, Home: dir, ConfigDir: filepath.Join(dir, "pi"),
		Env: map[string]string{"FAKE_PI_MODE": mode, "FAKE_PI_RECORD": rec, "OPENROUTER_API_KEY": "k"},
	}
	r := Runner{Bin: os.Args[0], KillAfter: 500 * time.Millisecond}
	s, err := r.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s, rec
}

func collect(t *testing.T, s agent.Session) ([]agent.Event, agent.Event) {
	t.Helper()
	var evs []agent.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatal("events closed without Done")
			}
			if ev.Done {
				return evs, ev
			}
			evs = append(evs, ev)
		case <-timeout:
			t.Fatal("timed out waiting for Done")
		}
	}
}

func logs(evs []agent.Event, kind domain.EventKind) []string {
	var out []string
	for _, ev := range evs {
		if ev.Log != nil && ev.Log.Kind == kind {
			out = append(out, ev.Log.Text)
		}
	}
	return out
}

func TestRunSuccess(t *testing.T) {
	s, rec := start(t, "ok")
	evs, done := collect(t, s)
	if done.Err != nil || done.Summary != "final answer" {
		t.Fatalf("done = %+v", done)
	}
	var cost float64
	var session string
	for _, ev := range evs {
		cost += ev.Cost
		if ev.SessionID != "" {
			session = ev.SessionID
		}
	}
	if cost < 0.0299 || cost > 0.0301 {
		t.Errorf("cost = %v, want 0.03", cost)
	}
	if session != "/home/x/.pi/s.jsonl" {
		t.Errorf("session = %q", session)
	}
	if got := logs(evs, domain.EventText); !slices.Equal(got, []string{"looking", "final answer"}) {
		t.Errorf("text = %q", got)
	}
	if got := logs(evs, domain.EventThinking); !slices.Equal(got, []string{"plan"}) {
		t.Errorf("thinking = %q", got)
	}
	if got := logs(evs, domain.EventToolCall); !slices.Equal(got, []string{`bash {"command":"ls"}`}) {
		t.Errorf("tool calls = %q", got)
	}
	if got := logs(evs, domain.EventToolResult); !slices.Equal(got, []string{"a.go"}) {
		t.Errorf("tool results = %q", got)
	}

	var got struct{ Args, Env []string }
	b, _ := os.ReadFile(rec)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"--mode", "rpc", "--provider", "openrouter", "--model", "anthropic/claude-sonnet-4",
		"--thinking", "high", "--append-system-prompt", "be brief", "--session", "/s.jsonl"}
	if !slices.Equal(got.Args, want) {
		t.Errorf("args = %q", got.Args)
	}
	for _, want := range []string{"OPENROUTER_API_KEY=k", "PI_CODING_AGENT_DIR="} {
		if !slices.ContainsFunc(got.Env, func(kv string) bool { return strings.HasPrefix(kv, want) }) {
			t.Errorf("env lacks %s", want)
		}
	}
}

func TestRunModelError(t *testing.T) {
	s, _ := start(t, "error")
	_, done := collect(t, s)
	if done.Err == nil || !strings.Contains(done.Err.Error(), "402") {
		t.Fatalf("err = %v", done.Err)
	}
}

func TestPromptRejected(t *testing.T) {
	s, _ := start(t, "reject")
	_, done := collect(t, s)
	if done.Err == nil || !strings.Contains(done.Err.Error(), "No API key") {
		t.Fatalf("err = %v", done.Err)
	}
}

func TestInterruptThenSend(t *testing.T) {
	s, _ := start(t, "interrupt")
	ctx := context.Background()
	for ev := range s.Events() {
		if ev.Log != nil && ev.Log.Text == "working" {
			break
		}
	}
	// A steer while running is accepted.
	if err := s.Send(ctx, "steer me"); err != nil {
		t.Fatal(err)
	}
	if err := s.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	for ev := range s.Events() {
		if ev.Done {
			t.Fatalf("aborted settle ended the run: %+v", ev)
		}
		if ev.Log != nil && ev.Log.Kind == domain.EventSystem {
			break
		}
	}
	if err := s.Send(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	evs, done := collect(t, s)
	if done.Err != nil || done.Summary != "echo: again" {
		t.Fatalf("done = %+v", done)
	}
	if got := logs(evs, domain.EventUser); !slices.Equal(got, []string{"again"}) {
		t.Errorf("user = %q", got)
	}
	if err := s.Interrupt(ctx); !errors.Is(err, agent.ErrEnded) {
		t.Errorf("Interrupt after end = %v", err)
	}
}
