package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/store"
)

const waitFor = 15 * time.Second

func TestMain(m *testing.M) {
	// Keep the user's git config (signing, hooks, default branch) out of the
	// repos these tests create.
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "test",
		"GIT_AUTHOR_EMAIL":    "test@localhost",
		"GIT_COMMITTER_NAME":  "test",
		"GIT_COMMITTER_EMAIL": "test@localhost",
	} {
		os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

// ---------------------------------------------------------------- clock

type clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().UTC().Add(c.off)
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

// ---------------------------------------------------------------- notifier

type note struct {
	Event       domain.NotifyEvent
	Title, Body string
	URL         string
}

type recNotifier struct {
	mu    sync.Mutex
	notes []note
}

func (n *recNotifier) Notify(_ context.Context, ev domain.NotifyEvent, title, body, url string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notes = append(n.notes, note{ev, title, body, url})
}

func (n *recNotifier) count(ev domain.NotifyEvent) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, x := range n.notes {
		if x.Event == ev {
			c++
		}
	}
	return c
}

// ---------------------------------------------------------------- skills

type skillsStub struct{ dir string }

func (s skillsStub) Snapshot(context.Context) (string, error) { return s.dir, nil }

// ---------------------------------------------------------------- GitHub

type ghComment struct {
	Number       int
	Marker, Body string
}

// fakeGitHub keeps issues and PRs in memory and uses a local bare repository
// as origin.
type fakeGitHub struct {
	origin string

	mu        sync.Mutex
	next      int
	issues    map[int]string // number → body
	pulls     map[int]*PullState
	bodies    map[int]string // PR number → body
	comments  []ghComment
	merges    []string // "number:method"
	mergeSHAs []string // the head each merge required
	markReady []int
	pushes    []string
	pushDirs  []string
	fetches   []string // repo of each Fetch
}

func newFakeGitHub(origin string) *fakeGitHub {
	return &fakeGitHub{origin: origin, next: 1, issues: map[int]string{}, pulls: map[int]*PullState{}, bodies: map[int]string{}}
}

func (g *fakeGitHub) DefaultBranch(context.Context, string) (string, error) { return "main", nil }

func (g *fakeGitHub) CreateIssue(_ context.Context, _, title, body string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.next
	g.next++
	g.issues[n] = body
	return n, nil
}

func (g *fakeGitHub) ListIssues(context.Context, string) ([]Issue, error) { return nil, nil }

func (g *fakeGitHub) UpdateIssueBody(_ context.Context, _ string, n int, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.issues[n] = body
	return nil
}

func (g *fakeGitHub) UpsertComment(_ context.Context, _ string, n int, marker, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.comments = append(g.comments, ghComment{n, marker, body})
	return nil
}

func (g *fakeGitHub) headOf(branch string) string {
	out, err := exec.Command("git", "-C", g.origin, "rev-parse", "refs/heads/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (g *fakeGitHub) CreatePull(_ context.Context, _, head, base, title, body string, draft bool) (int, string, error) {
	sha := g.headOf(head)
	if sha == "" {
		return 0, "", fmt.Errorf("branch %s is not on origin", head)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.next
	g.next++
	url := fmt.Sprintf("https://github.test/pull/%d", n)
	g.pulls[n] = &PullState{Number: n, Title: title, Branch: head, URL: url, State: "open", Draft: draft,
		MergeableState: "clean", HeadSHA: sha, CI: "pending"}
	g.bodies[n] = body
	return n, url, nil
}

func (g *fakeGitHub) UpdatePullBody(_ context.Context, _ string, n int, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pulls[n] == nil {
		return fmt.Errorf("no PR %d", n)
	}
	g.bodies[n] = body
	return nil
}

func (g *fakeGitHub) MarkReady(_ context.Context, _ string, n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pulls[n] == nil {
		return fmt.Errorf("no PR %d", n)
	}
	g.pulls[n].Draft = false
	g.markReady = append(g.markReady, n)
	return nil
}

func (g *fakeGitHub) MergePull(_ context.Context, _ string, n int, method, headSHA string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	pr := g.pulls[n]
	if pr == nil {
		return fmt.Errorf("no PR %d", n)
	}
	if headSHA != "" && headSHA != pr.HeadSHA {
		return fmt.Errorf("head of PR %d is %s, not %s", n, pr.HeadSHA, headSHA) // GitHub's 409
	}
	g.mergeSHAs = append(g.mergeSHAs, headSHA)
	pr.Merged, pr.State = true, "closed"
	g.merges = append(g.merges, fmt.Sprintf("%d:%s", n, method))
	return nil
}

func (g *fakeGitHub) ListPulls(context.Context, string) ([]PullState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []PullState
	for _, p := range g.pulls {
		out = append(out, *p)
	}
	return out, nil
}

func (g *fakeGitHub) Pull(_ context.Context, _ string, n int) (PullState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pr := g.pulls[n]
	if pr == nil {
		return PullState{}, fmt.Errorf("no PR %d", n)
	}
	cp := *pr
	cp.Checks = append([]Check(nil), pr.Checks...)
	return cp, nil
}

func (g *fakeGitHub) FailedLogs(_ context.Context, _ string, c Check) (string, error) {
	return "log of " + c.Name, nil
}

func (g *fakeGitHub) Clone(ctx context.Context, _ string, dir string) error {
	return git(ctx, "clone", g.origin, dir)
}

func (g *fakeGitHub) Fetch(ctx context.Context, repo, dir string) error {
	g.mu.Lock()
	g.fetches = append(g.fetches, repo)
	g.mu.Unlock()
	return git(ctx, "-C", dir, "fetch", "--prune", "origin")
}

func (g *fakeGitHub) Push(ctx context.Context, _ string, dir, branch string) error {
	if branch == "main" {
		return fmt.Errorf("refusing to push the default branch")
	}
	if err := git(ctx, "-C", dir, "push", "--force", "origin", "refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		return err
	}
	sha := g.headOf(branch)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pushes = append(g.pushes, branch)
	g.pushDirs = append(g.pushDirs, dir)
	for _, pr := range g.pulls {
		if pr.Branch == branch && pr.HeadSHA != sha {
			// A new head starts a new CI run.
			pr.HeadSHA, pr.CI, pr.Checks = sha, "pending", nil
		}
	}
	return nil
}

// setCI sets a PR's combined CI result and a matching check.
func (g *fakeGitHub) setCI(n int, ci string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pr := g.pulls[n]
	pr.CI = ci
	pr.Checks = []Check{{Name: "test", Status: "completed", Conclusion: ci, ID: 1}}
}

func (g *fakeGitHub) update(n int, f func(*PullState)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f(g.pulls[n])
}

func (g *fakeGitHub) mergeCalls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.merges...)
}

func (g *fakeGitHub) pullBody(n int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bodies[n]
}

func (g *fakeGitHub) commentsOn(n int) []ghComment {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ghComment
	for _, c := range g.comments {
		if c.Number == n {
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------- runner

// testSession is an agent session the test drives by hand: it emits what the
// test tells it to and ends when the test (or the orchestrator) says so.
type testSession struct {
	mu         sync.Mutex
	ch         chan agent.Event
	ended      bool
	sent       []string
	interrupts int
	cmd        *exec.Cmd // a real process, for pause/resume
}

func (s *testSession) Events() <-chan agent.Event { return s.ch }

// Emit sends a non-final event; it is dropped once the session ended.
func (s *testSession) Emit(ev agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.ch <- ev
	}
}

// Finish sends the final event and closes the stream.
func (s *testSession) Finish(ev agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	ev.Done = true
	s.ch <- ev
	close(s.ch)
	if s.cmd != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (s *testSession) Send(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return agent.ErrEnded
	}
	s.sent = append(s.sent, text)
	return nil
}

func (s *testSession) Interrupt(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return agent.ErrEnded
	}
	s.interrupts++
	return nil
}

func (s *testSession) Stop() error {
	s.Finish(agent.Event{Err: agent.ErrStopped})
	return nil
}

func (s *testSession) PID() int {
	if s.cmd == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

func (s *testSession) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

func (s *testSession) interruptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupts
}

// testRun is one started run as the test sees it.
type testRun struct {
	Info RunInfo
	Spec agent.Spec
	Sess *testSession
}

func (r *testRun) ID() int64 { return r.Info.RunID }

type testRunner struct {
	mu          sync.Mutex
	pending     RunInfo
	withProcess bool
	starts      chan *testRun
}

func (r *testRunner) Start(_ context.Context, spec agent.Spec) (agent.Session, error) {
	r.mu.Lock()
	info, withProcess := r.pending, r.withProcess
	r.mu.Unlock()
	s := &testSession{ch: make(chan agent.Event, 256)}
	if withProcess {
		cmd := exec.Command("sleep", "300")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		go func() { _ = cmd.Wait() }()
		s.cmd = cmd
	}
	r.starts <- &testRun{Info: info, Spec: spec, Sess: s}
	return s, nil
}

// ---------------------------------------------------------------- harness

type harness struct {
	stopped bool // set by withoutRun
	t       *testing.T
	ctx     context.Context
	o       *Orchestrator
	st      *store.Store
	gh      *fakeGitHub
	notes   *recNotifier
	runner  *testRunner
	clock   *clock
	cfg     config.Config
	project domain.Project

	mu      sync.Mutex
	revoked []string
}

type harnessOpt func(*harness, *Options)

// withoutRun leaves the scheduler stopped, for tests that call its steps
// (like migrateLayout) themselves and must not race it.
func withoutRun() harnessOpt { return func(h *harness, _ *Options) { h.stopped = true } }

func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	root := t.TempDir()
	ctx := context.Background()

	// origin: a bare repo with one commit on main.
	seed := filepath.Join(root, "seed")
	origin := filepath.Join(root, "origin.git")
	mustGit(t, root, "init", "-q", "-b", "main", seed)
	writeFile(t, filepath.Join(seed, "README.md"), "hello\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "init")
	mustGit(t, root, "clone", "-q", "--bare", seed, origin)

	skills := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skills, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "openrouter.key")
	writeFile(t, keyFile, "or-key\n")

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.MaxParallelRuns = 2
	cfg.MCPListen = "127.0.0.1:0"
	cfg.SandboxPATH = os.Getenv("PATH")
	cfg.PublicURL = "http://test"
	cfg.OpenRouterKeyFile = keyFile

	st, err := store.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	h := &harness{
		t: t, ctx: ctx, st: st, gh: newFakeGitHub(origin), notes: &recNotifier{},
		runner: &testRunner{starts: make(chan *testRun, 16)}, clock: &clock{}, cfg: cfg,
	}
	o := Options{
		Config: cfg, Store: st, GitHub: h.gh,
		Runners:  map[domain.AgentKind]agent.Runner{domain.AgentClaude: h.runner, domain.AgentPi: h.runner, domain.AgentFake: h.runner},
		Notifier: h.notes, Skills: skillsStub{skills},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: h.clock.Now,
		Register: func(ri RunInfo) (string, error) {
			h.runner.mu.Lock()
			h.runner.pending = ri
			h.runner.mu.Unlock()
			return fmt.Sprintf("tok-%d", ri.RunID), nil
		},
		Revoke: func(tok string) {
			h.mu.Lock()
			h.revoked = append(h.revoked, tok)
			h.mu.Unlock()
		},
		Tick:   10 * time.Millisecond,
		CIPoll: 5 * time.Millisecond,
	}
	for _, f := range opts {
		f(h, &o)
	}
	h.o = New(o)

	h.project, err = st.CreateProject(ctx, domain.Project{Name: "demo", Repo: "acme/demo", DefaultBranch: "main", Agent: domain.AgentFake})
	if err != nil {
		t.Fatal(err)
	}

	// Run migrates the layout too; doing it first keeps it from moving
	// aside files a test plants under projects/ before its first run.
	if err := h.o.migrateLayout(ctx); err != nil {
		t.Fatal(err)
	}
	if h.stopped {
		return h
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- h.o.Run(runCtx) }()
	t.Cleanup(func() {
		h.o.StopPreview()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(waitFor):
			t.Error("orchestrator did not stop")
		}
	})
	return h
}

// newTicket inserts a ticket straight into the store (any stage/status).
func (h *harness) newTicket(t domain.Ticket) domain.Ticket {
	h.t.Helper()
	t.ProjectID = h.project.ID
	if t.Kind == "" {
		t.Kind = domain.KindTicket
	}
	if t.Title == "" {
		t.Title = "Add dark mode"
	}
	if t.Idea == "" {
		t.Idea = t.Title
	}
	if t.Agent == "" {
		t.Agent = domain.AgentFake
	}
	if t.Status == "" {
		t.Status = domain.StatusReady
	}
	if t.Stage == "" {
		t.Stage = domain.StageExplore
	}
	out, err := h.st.CreateTicket(h.ctx, t)
	if err != nil {
		h.t.Fatal(err)
	}
	h.o.Kick()
	return out
}

func (h *harness) ticket(id int64) domain.Ticket {
	h.t.Helper()
	t, err := h.st.Ticket(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return t
}

func (h *harness) run(id int64) domain.Run {
	h.t.Helper()
	r, err := h.st.Run(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

// nextRun waits for the next run to start and for its ticket to be saved as
// working (tools that save the ticket must not race startRun's own save).
func (h *harness) nextRun() *testRun {
	h.t.Helper()
	select {
	case r := <-h.runner.starts:
		h.waitTicket(r.Info.TicketID, "working", func(t domain.Ticket) bool { return t.Status == domain.StatusWorking })
		return r
	case <-time.After(waitFor):
		h.t.Fatal("no run started")
		return nil
	}
}

// noRun asserts no run starts within d.
// answerAndSubmit saves one answer to round n and submits the round.
func (h *harness) answerAndSubmit(ticketID int64, n int, qid string, a domain.Answer) AnswerResult {
	h.t.Helper()
	if _, err := h.o.Answer(h.ctx, ticketID, n, qid, a); err != nil {
		h.t.Fatalf("answer %s: %v", qid, err)
	}
	res, err := h.o.Submit(h.ctx, ticketID, n)
	if err != nil || !res.Completed {
		h.t.Fatalf("submit round %d = %+v, %v", n, res, err)
	}
	return res
}

func (h *harness) noRun(d time.Duration) {
	h.t.Helper()
	select {
	case r := <-h.runner.starts:
		h.t.Fatalf("unexpected run started: %+v", r.Info)
	case <-time.After(d):
	}
}

// waitTicket polls until cond holds for the ticket and returns it.
func (h *harness) waitTicket(id int64, what string, cond func(domain.Ticket) bool) domain.Ticket {
	h.t.Helper()
	deadline := time.Now().Add(waitFor)
	var t domain.Ticket
	for time.Now().Before(deadline) {
		var err error
		t, err = h.st.Ticket(h.ctx, id)
		if err == nil && cond(t) {
			return t
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("ticket %d never became %s; last: status=%s stage=%s note=%q", id, what, t.Status, t.Stage, t.StatusNote)
	return t
}

func (h *harness) waitState(id int64, status domain.Status, stage domain.Stage) domain.Ticket {
	h.t.Helper()
	return h.waitTicket(id, fmt.Sprintf("%s/%s", status, stage), func(t domain.Ticket) bool {
		return t.Status == status && t.Stage == stage
	})
}

// waitInactive waits until the orchestrator has let go of a ticket's run.
func (h *harness) waitInactive(ticketID int64) {
	h.t.Helper()
	eventually(h.t, "run to settle", func() bool { return !h.o.isActive(ticketID) })
}

func (h *harness) revokedTokens() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.revoked...)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit writes a file in dir and commits it.
func commit(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name), content)
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-q", "-m", "change "+name)
}
