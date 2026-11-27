package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const testToken = "tok-123"

// fake is a minimal GitHub API: handlers keyed by "METHOD /path".
type fake struct {
	t        *testing.T
	srv      *httptest.Server
	handlers map[string]http.HandlerFunc
}

func newFake(t *testing.T) (*fake, *Client) {
	t.Helper()
	f := &fake{t: t, handlers: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("%s %s: Accept = %q", r.Method, r.URL.Path, got)
		}
		h, ok := f.handlers[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f, New(f.srv.URL, testToken)
}

func (f *fake) on(route string, h http.HandlerFunc) { f.handlers[route] = h }

func (f *fake) json(route string, v any) {
	f.on(route, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, v) })
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decode(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

var ctx = context.Background()

func TestGetRepo(t *testing.T) {
	f, c := newFake(t)
	f.json("GET /repos/o/r", map[string]any{
		"full_name": "o/r", "default_branch": "main",
		"allow_squash_merge": true, "allow_merge_commit": false, "allow_rebase_merge": true,
	})
	repo, err := c.GetRepo(ctx, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q", repo.DefaultBranch)
	}
	if got := repo.MergeMethods(); !slices.Equal(got, []MergeMethod{MergeSquash, MergeRebase}) {
		t.Errorf("MergeMethods = %v", got)
	}
}

func TestBadRepoAndNotFound(t *testing.T) {
	_, c := newFake(t)
	for _, repo := range []string{"", "o", "o/", "/r", "o/r/x"} {
		if _, err := c.GetRepo(ctx, repo); err == nil {
			t.Errorf("GetRepo(%q) = nil error", repo)
		}
	}
	_, err := c.GetIssue(ctx, "o/r", 9)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || apiErr.Message != "Not Found" {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func TestIssues(t *testing.T) {
	f, c := newFake(t)
	f.on("POST /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		m := decode(t, r)
		if m["title"] != "T" || m["body"] != "B" {
			t.Errorf("create body = %v", m)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"number": 7, "title": "T", "body": "B", "state": "open"})
	})
	f.on("PATCH /repos/o/r/issues/7", func(w http.ResponseWriter, r *http.Request) {
		m := decode(t, r)
		if _, ok := m["title"]; ok || m["body"] != "B2" {
			t.Errorf("update body = %v (nil fields must be omitted)", m)
		}
		writeJSON(w, map[string]any{"number": 7, "body": "B2"})
	})
	f.on("GET /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state = %q", r.URL.Query().Get("state"))
		}
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/issues?state=open&page=2>; rel="next", <x>; rel="last"`, f.srv.URL))
			writeJSON(w, []any{map[string]any{"number": 1}, map[string]any{"number": 2, "pull_request": map[string]any{}}})
			return
		}
		writeJSON(w, []any{map[string]any{"number": 3}})
	})
	is, err := c.CreateIssue(ctx, "o/r", "T", "B")
	if err != nil || is.Number != 7 {
		t.Fatalf("CreateIssue = %+v, %v", is, err)
	}
	body := "B2"
	if is, err = c.UpdateIssue(ctx, "o/r", 7, IssueUpdate{Body: &body}); err != nil || is.Body != "B2" {
		t.Fatalf("UpdateIssue = %+v, %v", is, err)
	}
	list, err := c.ListIssues(ctx, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	var nums []int
	for _, is := range list {
		nums = append(nums, is.Number)
	}
	if !slices.Equal(nums, []int{1, 3}) {
		t.Errorf("ListIssues numbers = %v, want [1 3] (PR filtered, page 2 followed)", nums)
	}
}

func TestForeignLinkRefused(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://evil.example/steal>; rel="next"`)
		writeJSON(w, []any{})
	})
	if _, err := c.ListIssues(ctx, "o/r"); err == nil || !strings.Contains(err.Error(), "refusing link") {
		t.Fatalf("err = %v, want refusal", err)
	}
}

func TestUpsertComment(t *testing.T) {
	const marker = "<!-- ra:summary -->"
	f, c := newFake(t)
	existing := []any{
		map[string]any{"id": 1, "body": "hello"},
		map[string]any{"id": 2, "body": "old\n\n" + marker},
	}
	f.on("GET /repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, existing) })
	var edited, created string
	f.on("PATCH /repos/o/r/issues/comments/2", func(w http.ResponseWriter, r *http.Request) {
		edited = decode(t, r)["body"].(string)
		writeJSON(w, map[string]any{"id": 2, "body": edited})
	})
	f.on("POST /repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		created = decode(t, r)["body"].(string)
		writeJSON(w, map[string]any{"id": 3, "body": created})
	})

	cm, err := c.UpsertComment(ctx, "o/r", 5, marker, "new")
	if err != nil || cm.ID != 2 {
		t.Fatalf("UpsertComment = %+v, %v", cm, err)
	}
	if edited != "new\n\n"+marker || created != "" {
		t.Errorf("edited=%q created=%q", edited, created)
	}

	existing = existing[:1]
	cm, err = c.UpsertComment(ctx, "o/r", 5, marker, marker+"\nfresh")
	if err != nil || cm.ID != 3 {
		t.Fatalf("UpsertComment create = %+v, %v", cm, err)
	}
	if created != marker+"\nfresh" {
		t.Errorf("created = %q (marker already present must not be duplicated)", created)
	}
	if _, err := c.UpsertComment(ctx, "o/r", 5, "", "x"); err == nil {
		t.Error("empty marker accepted")
	}
}

func TestPulls(t *testing.T) {
	f, c := newFake(t)
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	f.on("GET /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("state") {
		case "open":
			writeJSON(w, []any{map[string]any{"number": 10, "state": "open", "draft": true}})
		case "closed":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/pulls?state=closed&page=2>; rel="next"`, f.srv.URL))
				writeJSON(w, []any{
					map[string]any{"number": 9, "state": "closed", "merged_at": ts(-time.Hour), "updated_at": ts(-time.Hour)},
					map[string]any{"number": 8, "state": "closed", "updated_at": ts(-2 * time.Hour)}, // closed unmerged
					map[string]any{"number": 7, "state": "closed", "merged_at": ts(-48 * time.Hour), "updated_at": ts(-30 * time.Hour)},
				})
				return
			}
			t.Error("fetched page 2 although page 1 ended before mergedSince")
			writeJSON(w, []any{})
		}
	})
	pulls, err := c.ListPulls(ctx, "o/r", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 2 || pulls[0].Number != 10 || pulls[1].Number != 9 || !pulls[1].Merged {
		t.Fatalf("ListPulls = %+v", pulls)
	}

	f.json("GET /repos/o/r/pulls/10", map[string]any{
		"number": 10, "node_id": "PR_10", "state": "open", "draft": true, "mergeable": false,
		"mergeable_state": "dirty", "merged": false, "head": map[string]any{"ref": "goal", "sha": "abc"},
	})
	pr, err := c.GetPull(ctx, "o/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Mergeable == nil || *pr.Mergeable || pr.MergeableState != "dirty" || pr.Head.SHA != "abc" || !pr.Draft {
		t.Errorf("GetPull = %+v", pr)
	}

	f.on("POST /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		m := decode(t, r)
		if m["head"] != "goal" || m["base"] != "main" || m["draft"] != true {
			t.Errorf("create pull body = %v", m)
		}
		writeJSON(w, map[string]any{"number": 11, "draft": true})
	})
	if pr, err = c.CreatePull(ctx, "o/r", NewPull{Title: "t", Body: "b", Head: "goal", Base: "main", Draft: true}); err != nil || pr.Number != 11 {
		t.Fatalf("CreatePull = %+v, %v", pr, err)
	}

	f.on("PATCH /repos/o/r/pulls/11", func(w http.ResponseWriter, r *http.Request) {
		if m := decode(t, r); m["body"] != "nb" || m["title"] != nil {
			t.Errorf("update pull body = %v", m)
		}
		writeJSON(w, map[string]any{"number": 11, "body": "nb"})
	})
	nb := "nb"
	if pr, err = c.UpdatePull(ctx, "o/r", 11, PullUpdate{Body: &nb}); err != nil || pr.Body != "nb" {
		t.Fatalf("UpdatePull = %+v, %v", pr, err)
	}

	f.on("PUT /repos/o/r/pulls/11/merge", func(w http.ResponseWriter, r *http.Request) {
		if m := decode(t, r); m["merge_method"] != "squash" || m["sha"] != "abc" {
			t.Errorf("merge body = %v", m)
		}
		writeJSON(w, map[string]any{"merged": true, "sha": "m1"})
	})
	res, err := c.MergePull(ctx, "o/r", 11, MergeSquash, "abc")
	if err != nil || !res.Merged || res.SHA != "m1" {
		t.Fatalf("MergePull = %+v, %v", res, err)
	}
}

func TestMarkReady(t *testing.T) {
	f, c := newFake(t)
	draft := true
	f.on("GET /repos/o/r/pulls/3", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"number": 3, "node_id": "PR_3", "draft": draft})
	})
	calls := 0
	f.on("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		calls++
		m := decode(t, r)
		if !strings.Contains(m["query"].(string), "markPullRequestReadyForReview") {
			t.Errorf("query = %v", m["query"])
		}
		if vars := m["variables"].(map[string]any); vars["id"] != "PR_3" {
			t.Errorf("variables = %v", vars)
		}
		writeJSON(w, map[string]any{"data": map[string]any{}})
	})
	if err := c.MarkReady(ctx, "o/r", 3); err != nil || calls != 1 {
		t.Fatalf("MarkReady = %v, calls %d", err, calls)
	}
	draft = false
	if err := c.MarkReady(ctx, "o/r", 3); err != nil || calls != 1 {
		t.Fatalf("MarkReady on ready PR = %v, calls %d (want no-op)", err, calls)
	}

	draft = true
	f.on("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"errors": []any{map[string]any{"message": "nope"}}})
	})
	if err := c.MarkReady(ctx, "o/r", 3); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want graphql error", err)
	}
}

func TestGraphQLURLOverride(t *testing.T) {
	f, c := newFake(t)
	f.json("GET /repos/o/r/pulls/3", map[string]any{"number": 3, "node_id": "PR_3", "draft": true})
	hit := false
	f.on("POST /api/graphql", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		writeJSON(w, map[string]any{"data": map[string]any{}})
	})
	c.GraphQLURL = f.srv.URL + "/api/graphql"
	if err := c.MarkReady(ctx, "o/r", 3); err != nil || !hit {
		t.Fatalf("MarkReady = %v, hit %v", err, hit)
	}
}

func TestCombinedChecks(t *testing.T) {
	tests := []struct {
		name     string
		runs     []any
		statuses []any
		want     CheckState
	}{
		{"none", nil, nil, CheckPending},
		{"all green", []any{run(1, "build", "completed", "success"), run(2, "lint", "completed", "skipped")},
			[]any{status("ci/ext", "success")}, CheckSuccess},
		{"one running", []any{run(1, "build", "completed", "success"), run(2, "e2e", "in_progress", "")}, nil, CheckPending},
		{"status pending", []any{run(1, "build", "completed", "neutral")}, []any{status("ci/ext", "pending")}, CheckPending},
		{"failure wins over pending", []any{run(1, "build", "queued", ""), run(2, "test", "completed", "timed_out")}, nil, CheckFailure},
		{"status error", nil, []any{status("ci/ext", "error")}, CheckFailure},
		{"cancelled", []any{run(1, "build", "completed", "cancelled")}, nil, CheckFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := newFake(t)
			f.on("GET /repos/o/r/commits/abc/check-runs", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("filter") != "latest" {
					t.Errorf("filter = %q", r.URL.Query().Get("filter"))
				}
				writeJSON(w, map[string]any{"total_count": len(tt.runs), "check_runs": append([]any{}, tt.runs...)})
			})
			f.json("GET /repos/o/r/commits/abc/status", map[string]any{"statuses": append([]any{}, tt.statuses...)})
			got, err := c.CombinedChecks(ctx, "o/r", "abc")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tt.want {
				t.Errorf("State = %s, want %s", got.State, tt.want)
			}
			if len(got.Checks) != len(tt.runs)+len(tt.statuses) {
				t.Errorf("Checks = %+v", got.Checks)
			}
		})
	}
}

func TestCombinedChecksFields(t *testing.T) {
	f, c := newFake(t)
	f.json("GET /repos/o/r/commits/abc/check-runs", map[string]any{"check_runs": []any{run(42, "test", "completed", "failure")}})
	f.json("GET /repos/o/r/commits/abc/status", map[string]any{"statuses": []any{status("ext", "success")}})
	got, err := c.CombinedChecks(ctx, "o/r", "abc")
	if err != nil {
		t.Fatal(err)
	}
	want := []Check{
		{Name: "test", Status: "completed", Conclusion: "failure", URL: "https://ci/42", CheckRunID: 42, State: CheckFailure},
		{Name: "ext", Status: "success", URL: "https://ext", State: CheckSuccess},
	}
	if !slices.Equal(got.Checks, want) {
		t.Errorf("Checks = %+v\nwant %+v", got.Checks, want)
	}
}

func run(id int, name, st, concl string) map[string]any {
	return map[string]any{"id": id, "name": name, "status": st, "conclusion": concl, "html_url": fmt.Sprintf("https://ci/%d", id)}
}

func status(ctx, state string) map[string]any {
	return map[string]any{"context": ctx, "state": state, "target_url": "https://ext"}
}

func TestFailedLogsFollowsRedirectWithoutToken(t *testing.T) {
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			t.Errorf("token leaked to blob storage: %q", a)
		}
		var b strings.Builder
		for i := 1; i <= 500; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		b.WriteString("##[error]Process completed with exit code 1.\n")
		b.WriteString("cleanup 1\ncleanup 2\ncleanup 3\ncleanup 4\ncleanup 5\n")
		_, _ = io.WriteString(w, b.String())
	}))
	defer blob.Close()
	f, c := newFake(t)
	f.on("GET /repos/o/r/actions/jobs/42/logs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blob.URL+"/log.txt", http.StatusFound)
	})
	got, err := c.FailedLogs(ctx, "o/r", 42)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != MaxLogLines {
		t.Fatalf("got %d lines, want %d", len(lines), MaxLogLines)
	}
	if lines[len(lines)-4] != "##[error]Process completed with exit code 1." || lines[len(lines)-1] != "cleanup 3" {
		t.Errorf("tail = %q", lines[len(lines)-5:])
	}
}

func TestFailedLogsFallsBackToCheckOutput(t *testing.T) {
	f, c := newFake(t)
	f.json("GET /repos/o/r/check-runs/77", map[string]any{
		"name": "ext", "output": map[string]any{"title": "Lint failed", "summary": "3 issues", "text": "a.go:1: bad"},
	})
	got, err := c.FailedLogs(ctx, "o/r", 77)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Lint failed\n\n3 issues\n\na.go:1: bad" {
		t.Errorf("got %q", got)
	}
}

func TestTail(t *testing.T) {
	tests := []struct {
		log  string
		n    int
		want string
	}{
		{"a\nb\nc\n", 2, "b\nc"},
		{"a\nerror: x\nb\nc\nd\ne\nf\n", 3, "b\nc\nd"},
		{"ok\n--- FAIL: TestX\nok\n", 10, "ok\n--- FAIL: TestX\nok"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		if got := Tail(tt.log, tt.n); got != tt.want {
			t.Errorf("Tail(%q, %d) = %q, want %q", tt.log, tt.n, got, tt.want)
		}
	}
}

func TestTailBuffer(t *testing.T) {
	tb := &tailBuffer{max: 4}
	for _, s := range []string{"ab", "cdef", "ghijk", "l"} {
		_, _ = tb.Write([]byte(s))
	}
	if got := string(tb.bytes()); got != "ijkl" {
		t.Errorf("tail = %q", got)
	}
}

// Won't do closes an issue as not planned and a PR without merging it.
func TestCloseIssueNotPlannedAndPull(t *testing.T) {
	f, c := newFake(t)
	f.on("PATCH /repos/o/r/issues/7", func(w http.ResponseWriter, r *http.Request) {
		if m := decode(t, r); m["state"] != "closed" || m["state_reason"] != "not_planned" || m["body"] != nil {
			t.Errorf("close issue body = %v", m)
		}
		writeJSON(w, map[string]any{"number": 7, "state": "closed"})
	})
	f.on("PATCH /repos/o/r/pulls/11", func(w http.ResponseWriter, r *http.Request) {
		if m := decode(t, r); m["state"] != "closed" || m["body"] != nil {
			t.Errorf("close pull body = %v", m)
		}
		writeJSON(w, map[string]any{"number": 11, "state": "closed"})
	})
	closed, reason := "closed", "not_planned"
	if is, err := c.UpdateIssue(ctx, "o/r", 7, IssueUpdate{State: &closed, StateReason: &reason}); err != nil || is.State != "closed" {
		t.Fatalf("UpdateIssue = %+v, %v", is, err)
	}
	if pr, err := c.UpdatePull(ctx, "o/r", 11, PullUpdate{State: &closed}); err != nil || pr.State != "closed" {
		t.Fatalf("UpdatePull = %+v, %v", pr, err)
	}
}
