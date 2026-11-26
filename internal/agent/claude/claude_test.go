package claude

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

func start(t *testing.T, mode string, mutate func(*agent.Spec)) (agent.Session, string) {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "record.json")
	spec := agent.Spec{
		Model: "opus", Effort: "high", Prompt: "do it",
		Workdir: dir, Home: dir, ConfigDir: filepath.Join(dir, "cfg"),
		Env: map[string]string{
			"FAKE_CLAUDE_MODE":   mode,
			"FAKE_CLAUDE_RECORD": rec,
			"ANTHROPIC_API_KEY":  "must-not-leak",
		},
	}
	if mutate != nil {
		mutate(&spec)
	}
	r := Runner{Bin: os.Args[0], InterruptAfter: 200 * time.Millisecond, KillAfter: 500 * time.Millisecond}
	s, err := r.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s, rec
}

// collect drains events until Done.
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
				if _, ok := <-s.Events(); ok {
					t.Fatal("event after Done")
				}
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
	s, rec := start(t, "ok", func(sp *agent.Spec) {
		sp.MCPConfig = "/mcp.json"
		sp.SystemPrompt = "be brief"
		sp.Resume = "/sess.jsonl"
	})
	evs, done := collect(t, s)
	if done.Err != nil || done.Summary != "all done" || done.Cost != 0.25 {
		t.Fatalf("done = %+v", done)
	}
	if s.PID() != 0 {
		t.Error("PID after exit should be 0")
	}
	if evs[0].SessionID != "sess-1" {
		t.Errorf("first event %+v, want session id", evs[0])
	}
	if got := logs(evs, domain.EventThinking); !slices.Equal(got, []string{"hmm"}) {
		t.Errorf("thinking = %q", got)
	}
	if got := logs(evs, domain.EventText); !slices.Equal(got, []string{"hello"}) {
		t.Errorf("text = %q", got)
	}
	if got := logs(evs, domain.EventToolCall); !slices.Equal(got, []string{`Bash {"command":"ls -la"}`}) {
		t.Errorf("tool calls = %q", got)
	}
	res := logs(evs, domain.EventToolResult)
	if len(res) != 1 || len(res[0]) > 2200 || !strings.Contains(res[0], "truncated") {
		t.Errorf("tool result not truncated: %d bytes", len(res[0]))
	}
	var usage *agent.Usage
	for _, ev := range evs {
		if ev.Usage != nil {
			usage = ev.Usage
		}
		if ev.Limit != nil {
			t.Errorf("unexpected limit %+v", ev.Limit)
		}
	}
	if usage == nil || !usage.Warning || usage.Utilization != 0.91 || usage.ResetsAt.Unix() != 2000000000 {
		t.Errorf("usage = %+v", usage)
	}

	var got struct {
		Args []string
		Env  []string
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--dangerously-skip-permissions", "--disallowedTools", "Agent,Task", "--model", "opus", "--effort", "high", "--mcp-config", "/mcp.json",
		"--append-system-prompt", "be brief", "--resume", "/sess.jsonl"}
	if !slices.Equal(got.Args, want) {
		t.Errorf("args = %q", got.Args)
	}
	if slices.Contains(got.Args, "--bare") {
		t.Error("--bare passed")
	}
	for _, kv := range got.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY leaked")
		}
	}
	if !slices.ContainsFunc(got.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") && strings.HasSuffix(kv, "/cfg")
	}) {
		t.Errorf("CLAUDE_CONFIG_DIR missing: %q", got.Env)
	}
	if !slices.Contains(got.Env, "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1") {
		t.Errorf("background tasks not disabled: %q", got.Env)
	}
}

func TestRunError(t *testing.T) {
	s, _ := start(t, "error", nil)
	_, done := collect(t, s)
	if done.Err == nil || !strings.Contains(done.Err.Error(), "Not logged in") {
		t.Fatalf("err = %v", done.Err)
	}
}

func TestRunLimit(t *testing.T) {
	s, _ := start(t, "limit", nil)
	evs, done := collect(t, s)
	if done.Err != nil {
		t.Errorf("limited run should not carry Err: %v", done.Err)
	}
	var limits []*agent.Limit
	for _, ev := range evs {
		if ev.Limit != nil {
			limits = append(limits, ev.Limit)
		}
	}
	if len(limits) != 1 || limits[0].ResetsAt.Unix() != 2000000000 || limits[0].Type != "seven_day" {
		t.Fatalf("limits = %+v", limits)
	}
}

func TestInterruptThenSend(t *testing.T) {
	s, _ := start(t, "interrupt", nil)
	ctx := context.Background()
	// Wait for the first text so the turn is under way.
	for ev := range s.Events() {
		if ev.Log != nil && ev.Log.Text == "working" {
			break
		}
	}
	if err := s.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	for ev := range s.Events() {
		if ev.Done {
			t.Fatalf("interrupted result ended the run: %+v", ev)
		}
		if ev.Log != nil && ev.Log.Kind == domain.EventSystem {
			break
		}
	}
	if err := s.Send(ctx, "go on"); err != nil {
		t.Fatal(err)
	}
	evs, done := collect(t, s)
	if done.Err != nil || done.Summary != "resumed" || done.Cost != 0.3 {
		t.Fatalf("done = %+v", done)
	}
	if got := logs(evs, domain.EventUser); !slices.Equal(got, []string{"go on"}) {
		t.Errorf("user = %q", got)
	}
	if got := logs(evs, domain.EventText); !slices.Equal(got, []string{"echo: go on prio=next"}) {
		t.Errorf("text = %q", got)
	}
	if err := s.Send(ctx, "late"); !errors.Is(err, agent.ErrEnded) {
		t.Errorf("Send after end = %v", err)
	}
}

func TestExitWithoutResult(t *testing.T) {
	s, _ := start(t, "crash", nil)
	evs, done := collect(t, s)
	if done.Err == nil || !strings.Contains(done.Err.Error(), "without a result") {
		t.Fatalf("err = %v", done.Err)
	}
	if got := logs(evs, domain.EventError); !slices.Equal(got, []string{"boom"}) {
		t.Errorf("stderr = %q", got)
	}
}

func TestStop(t *testing.T) {
	for _, mode := range []string{"hang", "stubborn"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := start(t, mode, nil)
			if s.PID() == 0 {
				t.Fatal("no pid")
			}
			began := time.Now()
			go func() { _ = s.Stop() }()
			_, done := collect(t, s)
			if !errors.Is(done.Err, agent.ErrStopped) {
				t.Fatalf("err = %v", done.Err)
			}
			// The stubborn CLI ignores EOF and SIGINT; only the kill ends it.
			if mode == "stubborn" && time.Since(began) < 500*time.Millisecond {
				t.Errorf("stopped after %v, before the kill deadline", time.Since(began))
			}
		})
	}
}

func TestContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := Runner{Bin: os.Args[0], KillAfter: 500 * time.Millisecond}
	s, err := r.Start(ctx, agent.Spec{Prompt: "x", Env: map[string]string{"FAKE_CLAUDE_MODE": "hang"}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, done := collect(t, s)
	if !errors.Is(done.Err, agent.ErrStopped) {
		t.Fatalf("err = %v", done.Err)
	}
}
