package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/sandbox"
)

// sandboxCall is one use of Options.Sandbox.
type sandboxCall struct {
	rw, ro  []string
	workdir string
}

type sandboxRec struct {
	mu    sync.Mutex
	calls []sandboxCall
	// wrap builds the command; identity when nil.
	wrap func(rw, ro []string, workdir string) func(argv, env []string) ([]string, []string)
}

func (s *sandboxRec) opt(_ *harness, o *Options) {
	o.Sandbox = func(rw, ro []string, workdir string) func(argv, env []string) ([]string, []string) {
		s.mu.Lock()
		s.calls = append(s.calls, sandboxCall{slices.Clone(rw), slices.Clone(ro), workdir})
		s.mu.Unlock()
		if s.wrap != nil {
			return s.wrap(rw, ro, workdir)
		}
		return func(argv, env []string) ([]string, []string) { return argv, env }
	}
}

func (s *sandboxRec) all() []sandboxCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// plantHooks makes every hook git might run, core.hooksPath and
// core.fsmonitor in the clone at dir append to marker, as a malicious agent
// would.
func plantHooks(t *testing.T, dir, marker string) {
	t.Helper()
	script := "#!/bin/sh\necho \"$0 $*\" >> " + marker + "\n"
	hooksPath := filepath.Join(dir, ".evil-hooks")
	for _, d := range []string{hooksPath, filepath.Join(dir, ".git", "hooks")} {
		for _, name := range []string{
			"post-checkout", "reference-transaction", "pre-push", "post-merge", "post-rewrite", "pre-auto-gc",
			"push-to-checkout", "pre-receive", "update", "post-receive", "post-update", "fsmonitor-watchman",
		} {
			p := filepath.Join(d, name)
			writeFile(t, p, script)
			if err := os.Chmod(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	mustGit(t, dir, "config", "core.hooksPath", hooksPath)
	mustGit(t, dir, "config", "core.fsmonitor", filepath.Join(hooksPath, "fsmonitor-watchman"))
	mustGit(t, dir, "config", "uploadpack.packObjectsHook", filepath.Join(hooksPath, "update"))
}

// A malicious agent's hooks, core.hooksPath and core.fsmonitor in its clone
// never run as the app: not when it pushes, not when its clone is
// refreshed for the next stage, not when another ticket's clone is made.
// Its run sees only its own clone and home read-write.
func TestAgentGitConfigNeverRunsInApp(t *testing.T) {
	rec := &sandboxRec{}
	h := newHarness(t, rec.opt)
	marker := filepath.Join(t.TempDir(), "ran")

	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	wd := r.Info.Workdir
	if wd != h.o.workDir(tk) {
		t.Fatalf("workdir = %s, want %s", wd, h.o.workDir(tk))
	}
	if fi, err := os.Lstat(filepath.Join(wd, ".git")); err != nil || !fi.IsDir() {
		t.Fatalf("ticket workspace is not an independent clone: %v", err)
	}
	for _, f := range []string{"objects/info/alternates", "commondir"} {
		if _, err := os.Stat(filepath.Join(wd, ".git", f)); err == nil {
			t.Fatalf("ticket clone has .git/%s", f)
		}
	}
	if u := mustGit(t, wd, "config", "remote.origin.url"); u != "https://github.com/acme/demo.git" {
		t.Fatalf("origin = %q", u)
	}
	commit(t, wd, "feature.txt", "v1\n")
	plantHooks(t, wd, marker)

	branch, err := h.o.ToolPushBranch(h.ctx, r.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := h.gh.headOf(branch), mustGit(t, wd, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin %s = %q, want the clone's head %q", branch, got, want)
	}
	h.gh.mu.Lock()
	pushDirs := slices.Clone(h.gh.pushDirs)
	h.gh.mu.Unlock()
	if len(pushDirs) != 1 || pushDirs[0] != h.o.repoDir(h.project.ID) {
		t.Fatalf("pushed from %v, want only repo/", pushDirs)
	}

	// Another ticket's clone is made while the hooks are planted.
	other := h.newTicket(domain.Ticket{})
	r2 := h.nextRun()
	if r2.Info.Workdir != h.o.workDir(other) {
		t.Fatalf("second workdir = %s", r2.Info.Workdir)
	}
	_ = r2.Sess.Stop()
	h.waitInactive(other.ID)

	// The same ticket runs again: its existing clone is refreshed.
	if err := h.o.Stop(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	h.waitInactive(tk.ID)
	cur := h.ticket(tk.ID)
	cur.Status = domain.StatusReady
	if err := h.st.UpdateTicket(h.ctx, &cur); err != nil {
		t.Fatal(err)
	}
	h.o.Kick()
	r3 := h.nextRun()
	if r3.Info.Workdir != wd {
		t.Fatalf("rerun workdir = %s, want %s", r3.Info.Workdir, wd)
	}
	if b, err := currentBranch(h.ctx, wd); err != nil || b != branch {
		t.Fatalf("current branch = %q, %v", b, err)
	}
	_ = r3.Sess.Stop()

	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("agent-controlled git commands ran in the app:\n%s", b)
	}
	if cfg, _ := os.ReadFile(filepath.Join(h.o.repoDir(h.project.ID), ".git", "config")); strings.Contains(string(cfg), "evil") {
		t.Fatalf("repo/ config picked up the agent's settings:\n%s", cfg)
	}
	h.gh.mu.Lock()
	fetches := slices.Clone(h.gh.fetches)
	h.gh.mu.Unlock()
	for _, f := range fetches {
		if f != "acme/demo" {
			t.Fatalf("fetched %q, want the project's repo", f)
		}
	}

	// Sandboxes: runs write only their clone and home; git reading an
	// agent clone sees nothing else of the project.
	repo := h.o.repoDir(h.project.ID)
	var runs, uploads, refreshes int
	for _, c := range rec.all() {
		switch {
		case len(c.rw) == 2:
			runs++
			// Each ticket has its own home, named like its clone.
			home := filepath.Join(h.o.projectDir(h.project.ID), "home", filepath.Base(c.rw[0]))
			if c.rw[1] != home || (c.rw[0] != wd && c.rw[0] != h.o.workDir(other)) || c.workdir != c.rw[0] {
				t.Errorf("run sandbox rw = %v (workdir %s), want its clone and home", c.rw, c.workdir)
			}
		case len(c.rw) == 0:
			uploads++
			if !slices.Equal(c.ro, []string{wd}) {
				t.Errorf("upload-pack sandbox ro = %v, want only the clone", c.ro)
			}
		default:
			refreshes++
			if !slices.Equal(c.rw, []string{wd}) || !slices.Equal(c.ro, []string{repo}) {
				t.Errorf("refresh sandbox rw=%v ro=%v", c.rw, c.ro)
			}
		}
		for _, d := range c.rw {
			if d == repo || d == h.o.projectDir(h.project.ID) {
				t.Errorf("sandbox can write %s", d)
			}
		}
	}
	if runs != 3 || uploads != 1 || refreshes != 1 {
		t.Errorf("sandbox calls: %d runs, %d upload-packs, %d refreshes; want 3, 1, 1", runs, uploads, refreshes)
	}
}

// The git the app runs on agent clones works inside a real bubblewrap
// sandbox.
func TestGitInRealSandbox(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}
	if err := exec.Command(bwrap, "--unshare-all", "--ro-bind", "/", "/", "true").Run(); err != nil {
		t.Skipf("user namespaces unavailable: %v", err)
	}
	if !strings.HasPrefix(gitBin(), "/nix/store/") {
		t.Skip("git is not in /nix/store")
	}
	rec := &sandboxRec{wrap: func(rw, ro []string, workdir string) func(argv, env []string) ([]string, []string) {
		return sandbox.Config{Bwrap: bwrap, ReadWrite: rw, ReadOnly: ro, Workdir: workdir}.Wrap()
	}}
	h := newHarness(t, rec.opt)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	commit(t, r.Info.Workdir, "feature.txt", "v1\n")
	branch, err := h.o.ToolPushBranch(h.ctx, r.ID())
	if err != nil {
		t.Fatal(err)
	}
	if h.gh.headOf(branch) != mustGit(t, r.Info.Workdir, "rev-parse", "HEAD") {
		t.Fatal("sandboxed import did not carry the commit")
	}
	if err := h.o.refreshClone(h.ctx, tk.ProjectID, r.Info.Workdir); err != nil {
		t.Fatalf("sandboxed refresh: %v", err)
	}
	_ = r.Sess.Stop()
}

// Symlinks an agent plants in home/ never make the app delete, write or
// copy outside it.
func TestHomeSymlinksDoNotEscape(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "skills", "keep"), "precious")
	writeFile(t, filepath.Join(outside, "agents", "keep"), "precious")
	writeFile(t, filepath.Join(outside, "mcp-target"), "precious")
	home := h.o.homeDir(domain.Ticket{ID: 1, ProjectID: h.project.ID}) // the ticket created below
	for link, target := range map[string]string{
		".claude":                          outside, // .claude/skills would be outside/skills
		".agents":                          "../../../../../" + filepath.Base(outside),
		filepath.Join(".fake", "mcp.json"): filepath.Join(outside, "mcp-target"),
		filepath.Join(".fake", "skills"):   filepath.Join(outside, "skills"),
	} {
		p := filepath.Join(home, link)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}

	h.newTicket(domain.Ticket{})
	r := h.nextRun()
	for _, f := range []string{"skills/keep", "agents/keep", "mcp-target"} {
		if b, err := os.ReadFile(filepath.Join(outside, f)); err != nil || string(b) != "precious" {
			t.Fatalf("outside file %s = %q, %v", f, b, err)
		}
	}
	if fi, err := os.Lstat(filepath.Join(home, ".claude")); err != nil || !fi.IsDir() {
		t.Fatalf("home/.claude is not a real directory: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(home, ".claude", "skills")); err != nil || strings.HasPrefix(target, outside) {
		t.Fatalf("home/.claude/skills -> %q, %v", target, err)
	}
	fi, err := os.Lstat(r.Spec.MCPConfig)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("mcp.json is not a regular file: %v", err)
	}
	if b, _ := os.ReadFile(r.Spec.MCPConfig); !strings.Contains(string(b), "tok-") {
		t.Fatalf("mcp.json lacks the run token: %s", b)
	}
	_ = r.Sess.Stop()
}

// A transcript the agent turned into a symlink to an app file is not copied
// into the next account's config dir on resume.
func TestResumeDoesNotCopySymlinkedTranscript(t *testing.T) {
	h := newHarness(t)
	if _, err := h.st.CreateAccount(h.ctx, domain.Account{Label: "a", Priority: 1}, "token-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.CreateAccount(h.ctx, domain.Account{Label: "b", Priority: 2}, "token-b"); err != nil {
		t.Fatal(err)
	}
	h.newTicket(domain.Ticket{Agent: domain.AgentClaude})
	r1 := h.nextRun()
	r1.Sess.Emit(agent.Event{SessionID: "sess-1"})
	db := filepath.Join(h.cfg.DataDir, "remote-agent.db")
	writeFile(t, db, "secret db")
	for dir, target := range map[string]string{"abs": db, "rel": "../../../../../../../remote-agent.db"} {
		link := filepath.Join(r1.Spec.ConfigDir, "projects", dir, "sess-1.jsonl")
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	r1.Sess.Emit(agent.Event{Limit: &agent.Limit{ResetsAt: h.clock.Now().Add(time.Hour)}})

	r2 := h.nextRun()
	if r2.Spec.Resume != "" {
		t.Fatalf("resumed %q from a symlinked transcript", r2.Spec.Resume)
	}
	for _, dir := range []string{"abs", "rel"} {
		if b, err := os.ReadFile(filepath.Join(r2.Spec.ConfigDir, "projects", dir, "sess-1.jsonl")); err == nil {
			t.Fatalf("app file copied into the sandbox: %q", b)
		}
	}
	_ = r2.Sess.Stop()
}

// Project directories from the old layout (repo/ agent-writable) are moved
// aside once, so nothing an agent could have planted is reused.
func TestMigrateLayoutMovesOldProjectDirs(t *testing.T) {
	h := newHarness(t)
	if err := h.st.SetSetting(h.ctx, layoutKey, "1"); err != nil {
		t.Fatal(err)
	}
	projects := h.cfg.Path("projects")
	writeFile(t, filepath.Join(projects, "7", "repo", ".git", "hooks", "post-checkout"), "evil")
	writeFile(t, filepath.Join(projects, "notes", "x"), "kept")
	if err := h.o.migrateLayout(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(projects, "7")); !os.IsNotExist(err) {
		t.Fatalf("old project dir still in place: %v", err)
	}
	moved, _ := filepath.Glob(filepath.Join(projects, "7.legacy-*", "repo", ".git", "hooks", "post-checkout"))
	if len(moved) != 1 {
		t.Fatal("old project dir not kept aside")
	}
	if _, err := os.Stat(filepath.Join(projects, "notes", "x")); err != nil {
		t.Fatal("non-project dir touched")
	}
	if v, _, _ := h.st.Setting(h.ctx, layoutKey); v != layoutVersion {
		t.Fatalf("layout = %q", v)
	}
	// Once migrated, it is a no-op.
	writeFile(t, filepath.Join(projects, "8", "x"), "new")
	if err := h.o.migrateLayout(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(projects, "8", "x")); err != nil {
		t.Fatal("migrated twice")
	}
}
