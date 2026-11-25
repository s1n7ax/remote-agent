package fake

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

func writeScript(t *testing.T, path string, steps string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(steps), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestDefaultScript(t *testing.T) {
	t.Setenv(EnvScript, "")
	t.Setenv(EnvScriptDir, "")
	s, err := Runner{}.Start(context.Background(), agent.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	evs, done := collect(t, s)
	if done.Err != nil || done.Summary != "fake agent finished" {
		t.Fatalf("done = %+v", done)
	}
	if evs[0].SessionID == "" {
		t.Error("no session id")
	}
}

func TestStageScriptExecCostAndFail(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	writeScript(t, filepath.Join(dir, "build.json"), `[
		{"log":"building"},
		{"exec":"echo hi > out.txt && cat out.txt"},
		{"cost":0.5},
		{"fail":"boom"},
		{"log":"never"}
	]`)
	writeScript(t, filepath.Join(dir, "other.json"), `[{"log":"wrong stage"}]`)
	spec := agent.Spec{Workdir: work, Env: map[string]string{EnvScriptDir: dir, EnvStage: "build"}}
	s, err := Runner{}.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	evs, done := collect(t, s)
	if done.Err == nil || done.Err.Error() != "boom" {
		t.Fatalf("err = %v", done.Err)
	}
	if got := logs(evs, domain.EventText); !slices.Equal(got, []string{"building"}) {
		t.Errorf("text = %q", got)
	}
	if got := logs(evs, domain.EventToolResult); !slices.Equal(got, []string{"hi\n"}) {
		t.Errorf("exec output = %q", got)
	}
	if _, err := os.Stat(filepath.Join(work, "out.txt")); err != nil {
		t.Errorf("exec did not run in workdir: %v", err)
	}
	if !slices.ContainsFunc(evs, func(ev agent.Event) bool { return ev.Cost == 0.5 }) {
		t.Error("cost not reported")
	}
}

func TestFallsBackToScriptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	writeScript(t, path, `[{"limit":{"in_s":60}}]`)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	spec := agent.Spec{Env: map[string]string{EnvScript: path, EnvScriptDir: t.TempDir(), EnvStage: "design"}}
	s, err := Runner{Now: func() time.Time { return now }}.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	evs, done := collect(t, s)
	if done.Err != nil {
		t.Fatalf("err = %v", done.Err)
	}
	var lim *agent.Limit
	for _, ev := range evs {
		if ev.Limit != nil {
			lim = ev.Limit
		}
	}
	if lim == nil || !lim.ResetsAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("limit = %+v", lim)
	}
}

func TestBadScript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	writeScript(t, path, `{not json`)
	if _, err := (Runner{}).Start(context.Background(), agent.Spec{Env: map[string]string{EnvScript: path}}); err == nil {
		t.Fatal("want error")
	}
}

func TestSendInterruptStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	writeScript(t, path, `[{"wait_message":true},{"log":"got it"},{"sleep_ms":60000},{"log":"after"},{"sleep_ms":60000}]`)
	s, err := Runner{}.Start(context.Background(), agent.Spec{Env: map[string]string{EnvScript: path}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Send(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	waitText(t, s, "got it")
	if err := s.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	waitKind(t, s, domain.EventSystem)
	if err := s.Send(ctx, "continue"); err != nil {
		t.Fatal(err)
	}
	waitText(t, s, "after")
	go func() { _ = s.Stop() }()
	_, done := collect(t, s)
	if !errors.Is(done.Err, agent.ErrStopped) {
		t.Fatalf("err = %v", done.Err)
	}
	if got := s.(*Session).Messages(); !slices.Equal(got, []string{"hello", "continue"}) {
		t.Errorf("messages = %q", got)
	}
	if err := s.Send(ctx, "late"); !errors.Is(err, agent.ErrEnded) {
		t.Errorf("Send after end = %v", err)
	}
}

func waitText(t *testing.T, s agent.Session, text string) {
	t.Helper()
	for ev := range s.Events() {
		if ev.Log != nil && ev.Log.Text == text {
			return
		}
	}
	t.Fatalf("never saw %q", text)
}

func waitKind(t *testing.T, s agent.Session, kind domain.EventKind) {
	t.Helper()
	for ev := range s.Events() {
		if ev.Log != nil && ev.Log.Kind == kind {
			return
		}
	}
	t.Fatalf("never saw a %s event", kind)
}

func TestMCPCall(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "app", Version: "1"}, nil)
	var got map[string]any
	server.AddTool(&mcp.Tool{Name: "submit_questions", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_ = json.Unmarshal(req.Params.Arguments, &got)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "round 1 posted"}}}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer ts.Close()

	path := filepath.Join(t.TempDir(), "s.json")
	writeScript(t, path, `[{"mcp":{"tool":"submit_questions","args":{"title":"Requirement","questions":[]}}}]`)
	run := func(token string) ([]agent.Event, agent.Event) {
		env := map[string]string{EnvScript: path, EnvMCPURL: ts.URL, EnvMCPToken: token}
		s, err := Runner{}.Start(context.Background(), agent.Spec{Env: env})
		if err != nil {
			t.Fatal(err)
		}
		return collect(t, s)
	}

	evs, done := run("tok")
	if done.Err != nil {
		t.Fatalf("err = %v", done.Err)
	}
	if got["title"] != "Requirement" {
		t.Errorf("server got %v", got)
	}
	if res := logs(evs, domain.EventToolResult); !slices.Equal(res, []string{"round 1 posted"}) {
		t.Errorf("result = %q", res)
	}

	if _, done := run("wrong"); done.Err == nil || !strings.Contains(done.Err.Error(), "connect") {
		t.Errorf("bad token err = %v", done.Err)
	}
}
