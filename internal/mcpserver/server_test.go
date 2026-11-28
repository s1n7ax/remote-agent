package mcpserver

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
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/github"
)

// fakeHost records calls by tool and run.
type fakeHost struct {
	mu    sync.Mutex
	calls []call
	err   error // returned by every method when set
}

type call struct {
	run  int64
	tool string
	arg  any
}

func (h *fakeHost) record(run Run, tool string, arg any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, call{run.ID, tool, arg})
	return h.err
}

func (h *fakeHost) last(t *testing.T) call {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) == 0 {
		t.Fatal("host was not called")
	}
	return h.calls[len(h.calls)-1]
}

func (h *fakeHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *fakeHost) SubmitQuestions(_ context.Context, r Run, p domain.Purpose, tree domain.QuestionTree) (int, error) {
	return 2, h.record(r, ToolSubmitQuestions, struct {
		P domain.Purpose
		T domain.QuestionTree
	}{p, tree})
}
func (h *fakeHost) SubmitRequirement(_ context.Context, r Run, md string) error {
	return h.record(r, ToolSubmitRequirement, md)
}
func (h *fakeHost) SubmitDecisions(_ context.Context, r Run, md string) error {
	return h.record(r, ToolSubmitDecisions, md)
}
func (h *fakeHost) SubmitPrototypes(_ context.Context, r Run, ps []domain.Prototype) error {
	return h.record(r, ToolSubmitPrototypes, ps)
}
func (h *fakeHost) StartPreview(_ context.Context, r Run, p Preview) (string, error) {
	return "https://preview/1", h.record(r, ToolStartPreview, p)
}
func (h *fakeHost) SubmitProof(_ context.Context, r Run, p Proof) ([]string, error) {
	return nil, h.record(r, ToolSubmitProof, p)
}
func (h *fakeHost) UploadArtifact(_ context.Context, r Run, path string) (domain.Artifact, error) {
	return domain.Artifact{ID: "art1", Name: filepath.Base(path)}, h.record(r, ToolUploadArtifact, path)
}
func (h *fakeHost) PushBranch(_ context.Context, r Run) (Pushed, error) {
	return Pushed{Branch: "goal", SHA: "abc"}, h.record(r, ToolPushBranch, nil)
}
func (h *fakeHost) OpenPR(_ context.Context, r Run, pr NewPR) (github.Pull, error) {
	return github.Pull{Number: 5, Draft: pr.Draft}, h.record(r, ToolOpenPR, pr)
}
func (h *fakeHost) UpdatePR(_ context.Context, r Run, _ int, body string) (github.Pull, error) {
	return github.Pull{Number: 5, Body: body}, h.record(r, ToolUpdatePR, body)
}
func (h *fakeHost) CIStatus(_ context.Context, r Run, _ int) (github.Checks, error) {
	return github.Checks{State: github.CheckFailure, Checks: []github.Check{{Name: "test"}}}, h.record(r, ToolCIStatus, nil)
}
func (h *fakeHost) CILogs(_ context.Context, r Run, _ int, check string) (string, error) {
	return "--- FAIL: TestX", h.record(r, ToolCILogs, check)
}
func (h *fakeHost) GetIssue(_ context.Context, r Run, _ int) (github.Issue, error) {
	return github.Issue{Number: 3, Title: "Idea"}, h.record(r, ToolGetIssue, nil)
}
func (h *fakeHost) Comment(_ context.Context, r Run, _ int, body string) (github.Comment, error) {
	return github.Comment{ID: 9, Body: body}, h.record(r, ToolComment, body)
}
func (h *fakeHost) ListPulls(_ context.Context, r Run) ([]github.Pull, error) {
	return []github.Pull{{Number: 5}}, h.record(r, ToolListPulls, nil)
}
func (h *fakeHost) GetTicket(_ context.Context, r Run) (any, error) {
	return map[string]any{"id": r.TicketID}, h.record(r, ToolGetTicket, nil)
}
func (h *fakeHost) CreateIssue(_ context.Context, r Run, title, body string) (github.Issue, error) {
	return github.Issue{Number: 9, Title: title}, h.record(r, ToolCreateIssue, title)
}
func (h *fakeHost) StageDone(_ context.Context, r Run, summary string) error {
	return h.record(r, ToolStageDone, summary)
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type env struct {
	srv  *Server
	host *fakeHost
	http *httptest.Server
	dir  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	host := &fakeHost{}
	srv := New(host)
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return &env{srv: srv, host: host, http: hs, dir: t.TempDir()}
}

func (e *env) register(t *testing.T, run Run) string {
	t.Helper()
	if run.Workdir == "" {
		run.Workdir = e.dir
	}
	token, err := e.srv.Register(run)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (e *env) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             e.http.URL,
		HTTPClient:           &http.Client{Transport: bearer{token, http.DefaultTransport}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool returns the result text and whether it was a tool error.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func postStatus(t *testing.T, url, auth string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t)
	revoked := e.register(t, Run{ID: 1})
	e.srv.Revoke(revoked)
	expired := e.register(t, Run{ID: 2, Expires: time.Now().Add(-time.Second)})
	for name, auth := range map[string]string{
		"none": "", "unknown": "Bearer nope", "revoked": "Bearer " + revoked,
		"expired": "Bearer " + expired, "basic": "Basic Zm9vOmJhcg==",
	} {
		if got := postStatus(t, e.http.URL, auth); got != http.StatusUnauthorized {
			t.Errorf("%s token: status %d, want 401", name, got)
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	_, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: e.http.URL, HTTPClient: &http.Client{Transport: bearer{"nope", http.DefaultTransport}}, MaxRetries: -1,
	}, nil)
	if err == nil {
		t.Error("client connected with an unknown token")
	}
}

func TestRevokeCutsOffLiveSession(t *testing.T) {
	e := newEnv(t)
	token := e.register(t, Run{ID: 1})
	cs := e.connect(t, token)
	if _, isErr := callTool(t, cs, ToolGetIssue, map[string]any{}); isErr {
		t.Fatal("get_issue failed")
	}
	e.srv.Revoke(token)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolGetIssue, Arguments: map[string]any{}}); err == nil {
		t.Fatal("call succeeded after revoke")
	}
}

func TestRegisterValidation(t *testing.T) {
	e := newEnv(t)
	if _, err := e.srv.Register(Run{ID: 1, Workdir: "relative"}); err == nil {
		t.Error("relative workdir accepted")
	}
	if _, err := e.srv.Register(Run{ID: 1, Workdir: e.dir, Tools: []string{"merge_pr"}}); err == nil {
		t.Error("unknown tool accepted")
	}
}

func TestListTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.register(t, Run{ID: 1}))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if strings.Contains(tool.Name, "merge") {
			t.Errorf("tool %q looks like a merge tool", tool.Name)
		}
		if len(tool.Description) < 40 {
			t.Errorf("tool %q has a thin description", tool.Name)
		}
	}
	slices.Sort(names)
	want := slices.Clone(ToolNames)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}

	limited := e.connect(t, e.register(t, Run{ID: 2, Tools: []string{ToolSubmitQuestions, ToolStageDone}}))
	res, err = limited.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 2 {
		t.Errorf("limited run sees %d tools, want 2", len(res.Tools))
	}
	if _, err := limited.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolPushBranch, Arguments: map[string]any{}}); err == nil {
		t.Error("limited run called push_branch")
	}
}

func TestSubmitQuestions(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.register(t, Run{ID: 7}))
	tree := map[string]any{
		"title": "Requirement",
		"questions": []any{
			map[string]any{"id": "db", "kind": "single", "title": "Which DB?", "options": []any{
				map[string]any{"id": "pg", "label": "Postgres", "recommended": true},
				map[string]any{"id": "sqlite", "label": "SQLite"},
			}},
			map[string]any{"id": "pool", "kind": "text", "title": "Pool size?",
				"showIf": map[string]any{"all": []any{
					map[string]any{"question": "db", "anyOf": []any{"pg"}},
					map[string]any{"not": map[string]any{"question": "db", "anyOf": []any{"sqlite"}}},
				}}},
		},
	}
	text, isErr := callTool(t, cs, ToolSubmitQuestions, map[string]any{"purpose": "requirement", "tree": tree})
	if isErr || !strings.Contains(text, `"round": 2`) {
		t.Fatalf("result %q isErr=%v", text, isErr)
	}
	c := e.host.last(t)
	got := c.arg.(struct {
		P domain.Purpose
		T domain.QuestionTree
	})
	if c.run != 7 || got.P != domain.PurposeRequirement || len(got.T.Questions) != 2 ||
		got.T.Questions[1].ShowIf.All[1].Not.AnyOf[0] != "sqlite" {
		t.Errorf("host got %+v", c)
	}

	// Default purpose.
	if _, isErr := callTool(t, cs, ToolSubmitQuestions, map[string]any{"tree": tree}); isErr {
		t.Fatal("default purpose rejected")
	}
	if p := e.host.last(t).arg.(struct {
		P domain.Purpose
		T domain.QuestionTree
	}).P; p != domain.PurposeFollowUp {
		t.Errorf("default purpose = %s", p)
	}

	n := e.host.count()
	bad := map[string]any{"title": "R", "questions": []any{
		map[string]any{"id": "a", "kind": "text", "title": "x", "showIf": map[string]any{"question": "b", "answered": true}},
		map[string]any{"id": "a", "kind": "single", "title": "y"},
	}}
	text, isErr = callTool(t, cs, ToolSubmitQuestions, map[string]any{"tree": bad})
	if !isErr {
		t.Fatal("invalid tree accepted")
	}
	for _, want := range []string{"not an earlier question", "duplicate id", "at least one option"} {
		if !strings.Contains(text, want) {
			t.Errorf("error %q lacks %q", text, want)
		}
	}
	if _, isErr := callTool(t, cs, ToolSubmitQuestions, map[string]any{"purpose": "spend_cap", "tree": tree}); !isErr {
		t.Error("spend_cap purpose accepted from an agent")
	}
	if _, isErr := callTool(t, cs, ToolSubmitQuestions, map[string]any{"tree": map[string]any{"title": "x", "questions": []any{map[string]any{"id": "a", "kind": "radio", "title": "t"}}}}); !isErr {
		t.Error("schema let an unknown kind through")
	}
	if e.host.count() != n {
		t.Error("host called for an invalid tree")
	}
}

func TestFileTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.register(t, Run{ID: 1}))
	shot := filepath.Join(e.dir, "shots", "a.png")
	if err := os.MkdirAll(filepath.Dir(shot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.dir, "link")); err != nil {
		t.Fatal(err)
	}
	realShot, _ := filepath.EvalSymlinks(shot)

	text, isErr := callTool(t, cs, ToolUploadArtifact, map[string]any{"path": "shots/a.png"})
	if isErr || !strings.Contains(text, `"id": "art1"`) {
		t.Fatalf("upload = %q isErr=%v", text, isErr)
	}
	if got := e.host.last(t).arg; got != realShot {
		t.Errorf("host got path %v, want %s", got, realShot)
	}
	for _, p := range []string{outside, "../" + filepath.Base(outside), "link", "shots", "missing.png", ""} {
		if _, isErr := callTool(t, cs, ToolUploadArtifact, map[string]any{"path": p}); !isErr {
			t.Errorf("upload_artifact(%q) accepted", p)
		}
	}

	text, isErr = callTool(t, cs, ToolSubmitProof, map[string]any{
		"verdict": "PASS", "report_markdown": "# ok", "files": []any{shot},
	})
	if isErr {
		t.Fatalf("submit_proof: %s", text)
	}
	if p := e.host.last(t).arg.(Proof); p.Verdict != "PASS" || !slices.Equal(p.Files, []string{realShot}) {
		t.Errorf("proof = %+v", p)
	}
	// An escaping or missing file is never passed on, but does not block the proof.
	text, isErr = callTool(t, cs, ToolSubmitProof, map[string]any{"verdict": "PASS", "report_markdown": "x", "files": []any{"link", "missing.png", shot}})
	if isErr || !strings.Contains(text, "not stored") || !strings.Contains(text, "link") || !strings.Contains(text, "missing.png") {
		t.Errorf("proof with escaping and missing files: %q isErr=%v", text, isErr)
	}
	if p := e.host.last(t).arg.(Proof); !slices.Equal(p.Files, []string{realShot}) {
		t.Errorf("proof files = %v, want only %s", p.Files, realShot)
	}
	if _, isErr := callTool(t, cs, ToolSubmitProof, map[string]any{"verdict": "MAYBE", "report_markdown": "x"}); !isErr {
		t.Error("bad verdict accepted")
	}
}

func TestOtherTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.register(t, Run{ID: 1}))
	tests := []struct {
		tool     string
		args     map[string]any
		wantText string
		wantArg  any
	}{
		{ToolSubmitRequirement, map[string]any{"markdown": "# R"}, "saved", "# R"},
		{ToolSubmitDecisions, map[string]any{"markdown": "# D"}, "saved", "# D"},
		{ToolStartPreview, map[string]any{"command": "npm run dev", "port": 5173}, "https://preview/1", Preview{Command: "npm run dev", Port: 5173}},
		{ToolPushBranch, map[string]any{}, `"sha": "abc"`, nil},
		{ToolOpenPR, map[string]any{"title": "feat: x", "body": "b"}, `"draft": true`, NewPR{Title: "feat: x", Body: "b", Draft: true}},
		{ToolOpenPR, map[string]any{"title": "feat: x", "body": "b", "draft": false}, `"draft": false`, NewPR{Title: "feat: x", Body: "b"}},
		{ToolUpdatePR, map[string]any{"body": "nb"}, `"number": 5`, "nb"},
		{ToolCIStatus, map[string]any{}, `"state": "failure"`, nil},
		{ToolCILogs, map[string]any{"check": "test"}, "--- FAIL: TestX", "test"},
		{ToolGetIssue, map[string]any{}, `"title": "Idea"`, nil},
		{ToolComment, map[string]any{"body": "hi"}, `"id": 9`, "hi"},
		{ToolListPulls, map[string]any{}, `"number": 5`, nil},
		{ToolStageDone, map[string]any{"summary": "did it"}, "end your turn", "did it"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr || !strings.Contains(text, tt.wantText) {
				t.Fatalf("result %q isErr=%v, want %q", text, isErr, tt.wantText)
			}
			if c := e.host.last(t); c.tool != tt.tool || c.arg != tt.wantArg {
				t.Errorf("host call = %+v, want %s(%v)", c, tt.tool, tt.wantArg)
			}
		})
	}

	ps := []any{
		map[string]any{"name": "cards", "branch": "proto/cards", "description": "cards", "artifacts": []any{"a1"}},
		map[string]any{"name": "list", "branch": "proto/list", "description": "list", "preview": map[string]any{"command": "npm run dev", "port": 3000}},
	}
	if text, isErr := callTool(t, cs, ToolSubmitPrototypes, map[string]any{"prototypes": ps}); isErr {
		t.Fatalf("submit_prototypes: %s", text)
	}
	if got := e.host.last(t).arg.([]domain.Prototype); len(got) != 2 || got[1].Preview == nil || got[1].Preview.Port != 3000 {
		t.Errorf("prototypes = %+v", got)
	}

	for name, args := range map[string]struct {
		tool string
		args map[string]any
	}{
		"empty markdown":   {ToolSubmitRequirement, map[string]any{"markdown": "  "}},
		"missing command":  {ToolStartPreview, map[string]any{"port": 80}},
		"bad port":         {ToolStartPreview, map[string]any{"command": "x", "port": 70000}},
		"no title":         {ToolOpenPR, map[string]any{"body": "b"}},
		"no prototypes":    {ToolSubmitPrototypes, map[string]any{"prototypes": []any{}}},
		"dup prototype":    {ToolSubmitPrototypes, map[string]any{"prototypes": []any{ps[0], ps[0]}}},
		"cmd without port": {ToolSubmitPrototypes, map[string]any{"prototypes": []any{map[string]any{"name": "a", "branch": "b", "summary": "s", "previewCmd": "x"}}}},
		"no check":         {ToolCILogs, map[string]any{}},
		"empty summary":    {ToolStageDone, map[string]any{"summary": ""}},
	} {
		if _, isErr := callTool(t, cs, args.tool, args.args); !isErr {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHostErrorAndRunIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.connect(t, e.register(t, Run{ID: 1}))
	b := e.connect(t, e.register(t, Run{ID: 2}))
	callTool(t, a, ToolGetIssue, map[string]any{})
	if e.host.last(t).run != 1 {
		t.Error("run a mapped to the wrong run")
	}
	callTool(t, b, ToolGetIssue, map[string]any{})
	if e.host.last(t).run != 2 {
		t.Error("run b mapped to the wrong run")
	}

	e.host.err = errors.New("no pull request yet; call open_pr first")
	text, isErr := callTool(t, a, ToolCIStatus, map[string]any{})
	if !isErr || !strings.Contains(text, "call open_pr first") {
		t.Errorf("host error = %q isErr=%v", text, isErr)
	}
}

func TestSchemasAreValidJSON(t *testing.T) {
	for name, s := range map[string]string{
		"questions": schemaSubmitQuestions, "markdown": schemaMarkdown, "prototypes": schemaSubmitPrototypes,
		"preview": schemaStartPreview, "proof": schemaSubmitProof, "path": schemaPath, "none": schemaNone,
		"openpr": schemaOpenPR, "body": schemaBody, "cilogs": schemaCILogs, "stagedone": schemaStageDone,
	} {
		if !json.Valid([]byte(s)) {
			t.Errorf("schema %s is not valid JSON", name)
		}
	}
}
