package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Project directory layout under <dataDir>/projects/<id>/:
//
//	repo/            the app's clone of the GitHub repository. No sandbox
//	                 ever sees it, so it is the only clone the app runs git
//	                 in with the token (fetch from GitHub, push).
//	work/<ticket>/   the ticket's own clone (objects copied, no hardlinks or
//	                 alternates), origin = the GitHub URL without credentials
//	home/            $HOME of the sandbox; per-account config dirs live here
//
// A run's sandbox binds only its work/<ticket> and home/ read-write, so both
// are agent-controlled: the app runs git there only hardened (and, when it
// reads their config, inside a sandbox) and touches their files only through
// os.Root.
func (o *Orchestrator) projectDir(projectID int64) string {
	return o.cfg.Path("projects", fmt.Sprint(projectID))
}

func (o *Orchestrator) repoDir(projectID int64) string {
	return filepath.Join(o.projectDir(projectID), "repo")
}

func (o *Orchestrator) workDir(t domain.Ticket) string {
	return filepath.Join(o.projectDir(t.ProjectID), "work", fmt.Sprint(t.ID))
}

// homeDir is a ticket's agent $HOME. Each ticket has its own, so runs
// working in parallel cannot read each other's MCP token or config.
func (o *Orchestrator) homeDir(t domain.Ticket) string {
	return filepath.Join(o.projectDir(t.ProjectID), "home", fmt.Sprint(t.ID))
}

// remoteURL is the credential-free GitHub URL a ticket clone's origin names.
func (o *Orchestrator) remoteURL(repo string) string {
	base := strings.TrimRight(o.cfg.GitURL, "/")
	if base == "" {
		base = "https://github.com"
	}
	return base + "/" + repo + ".git"
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// branchName returns a ticket's goal branch: ra/<id>-<slug>.
func branchName(t domain.Ticket) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(t.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		slug = "ticket"
	}
	return fmt.Sprintf("ra/%d-%s", t.ID, slug)
}

// gitTimeout bounds every git command the app runs.
const gitTimeout = 10 * time.Minute

// cloneGitTimeout bounds each git command the app runs in an agent's clone,
// and checkoutTimeout all of checkoutPRHead: the agent can make git block
// (a FIFO as .git/HEAD, say), and runs start from the scheduler loop.
// Variables so tests can shorten them.
var (
	cloneGitTimeout = 2 * time.Minute
	checkoutTimeout = 3 * time.Minute
)

// gitWaitDelay is how long a killed git's output pipes may stay open (held
// by a process it started) before Wait gives up on them.
const gitWaitDelay = 5 * time.Second

// prepareWorkspace makes sure the project clone is up to date and the
// ticket has its own clone, and returns that clone. Jobs get a clone of the
// default branch.
func (o *Orchestrator) prepareWorkspace(ctx context.Context, p domain.Project, t *domain.Ticket) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	pdir := o.projectDir(p.ID)
	if err := os.MkdirAll(pdir, 0o750); err != nil {
		return "", fmt.Errorf("create project dir: %w", err)
	}
	root, err := os.OpenRoot(pdir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// Both are bind-mounted into sandboxes, so they must be real directories:
	// a symlink would bind its target.
	for _, d := range []string{"home", "work"} {
		if err := realDir(root, d); err != nil {
			return "", err
		}
	}
	homes, err := root.OpenRoot("home")
	if err != nil {
		return "", err
	}
	err = realDir(homes, fmt.Sprint(t.ID))
	_ = homes.Close()
	if err != nil {
		return "", err
	}
	if err := o.syncRepo(ctx, p); err != nil {
		return "", err
	}
	if t.Kind != domain.KindJob && t.Branch == "" {
		t.Branch = branchName(*t)
	}

	name := filepath.Join("work", fmt.Sprint(t.ID))
	work := o.workDir(*t)
	if isClone(root, name) {
		if err := o.refreshClone(ctx, p.ID, work); err != nil {
			// The agent can still work on what it has.
			o.log.Warn("refresh ticket clone", "ticket", t.ID, "err", err)
		}
		return work, nil
	}
	if _, err := root.Lstat(name); err == nil {
		// Not a clone (e.g. .git removed): keep it for the operator, out of
		// every sandbox's sight, and start over.
		stale := fmt.Sprintf("%s.stale-%d", name, o.now().UnixNano())
		if err := root.Rename(name, stale); err != nil {
			return "", fmt.Errorf("move aside broken workspace: %w", err)
		}
		o.log.Warn("moved aside a workspace that is not a clone", "ticket", t.ID, "to", stale)
	}
	if err := o.createClone(ctx, p, *t, work); err != nil {
		return "", err
	}
	return work, nil
}

// realDir makes name (one component under root) a directory, refusing a
// symlink or other file in its place.
func realDir(root *os.Root, name string) error {
	fi, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create %s: %w", name, err)
		}
		fi, err = root.Lstat(name)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s in %s is not a directory", name, root.Name())
	}
	return nil
}

// isClone reports whether name is a directory holding a .git directory
// (neither a symlink).
func isClone(root *os.Root, name string) bool {
	fi, err := root.Lstat(name)
	if err != nil || !fi.IsDir() {
		return false
	}
	fi, err = root.Lstat(filepath.Join(name, ".git"))
	return err == nil && fi.IsDir()
}

// lockRepo serialises writes to a project's repo/.
func (o *Orchestrator) lockRepo(projectID int64) func() {
	o.mu.Lock()
	m := o.repoMu[projectID]
	if m == nil {
		m = &sync.Mutex{}
		o.repoMu[projectID] = m
	}
	o.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// syncRepo clones or fetches the project's repo/ from GitHub.
func (o *Orchestrator) syncRepo(ctx context.Context, p domain.Project) error {
	defer o.lockRepo(p.ID)()
	repo := o.repoDir(p.ID)
	if _, err := os.Lstat(filepath.Join(repo, ".git")); err != nil {
		if err := o.gh.Clone(ctx, p.Repo, repo); err != nil {
			return fmt.Errorf("clone %s: %w", p.Repo, err)
		}
		return nil
	}
	if err := o.gh.Fetch(ctx, p.Repo, repo); err != nil {
		return fmt.Errorf("fetch %s: %w", p.Repo, err)
	}
	return nil
}

// originRefs copies repo/'s view of GitHub into a ticket clone.
var originRefs = []string{"+refs/remotes/origin/*:refs/remotes/origin/*", "^refs/remotes/origin/HEAD"}

// createClone makes the ticket's own clone of repo/ at work: it is built in
// a hidden directory no sandbox sees and moved into place when complete.
func (o *Orchestrator) createClone(ctx context.Context, p domain.Project, t domain.Ticket, work string) error {
	repo := o.repoDir(p.ID)
	tmp, err := os.MkdirTemp(filepath.Dir(work), "."+filepath.Base(work)+".new-")
	if err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	defer func() {
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
	}()
	refs := originRefs
	if t.Kind != domain.KindJob && gitQuiet(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+t.Branch) == nil {
		// A branch pushed from an earlier clone of this ticket.
		refs = append([]string{"+refs/heads/" + t.Branch + ":refs/heads/" + t.Branch}, refs...)
	}
	steps := [][]string{
		// No templates: nothing in .git but what git needs.
		{"init", "-q", "--template=", tmp},
		append([]string{"-C", tmp, "fetch", "-q", "--no-tags", "--no-recurse-submodules", "--", repo}, refs...),
		{"-C", tmp, "config", "remote.origin.url", o.remoteURL(p.Repo)},
		{"-C", tmp, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
	}
	for _, args := range steps {
		if err := git(ctx, args...); err != nil {
			return fmt.Errorf("create ticket clone: %w", err)
		}
	}
	var checkout []string
	switch {
	case t.Kind == domain.KindJob:
		checkout = []string{"checkout", "-q", "-b", p.DefaultBranch, "origin/" + p.DefaultBranch}
	case gitQuiet(ctx, tmp, "rev-parse", "--verify", "--quiet", "refs/heads/"+t.Branch) == nil:
		checkout = []string{"checkout", "-q", t.Branch}
	case gitQuiet(ctx, tmp, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+t.Branch) == nil:
		checkout = []string{"checkout", "-q", "-b", t.Branch, "origin/" + t.Branch}
	default:
		checkout = []string{"checkout", "-q", "--no-track", "-b", t.Branch, "origin/" + p.DefaultBranch}
	}
	if err := git(ctx, append([]string{"-C", tmp}, checkout...)...); err != nil {
		return fmt.Errorf("create ticket clone: %w", err)
	}
	if err := os.Rename(tmp, work); err != nil {
		return fmt.Errorf("create ticket clone: %w", err)
	}
	tmp = ""
	return nil
}

// refreshClone brings an existing ticket clone's origin/* up to date with
// repo/. The fetch runs in the agent's clone and so reads its config: it
// runs in a sandbox seeing only that clone (read-write) and repo/
// (read-only).
func (o *Orchestrator) refreshClone(ctx context.Context, projectID int64, work string) error {
	repo := o.repoDir(projectID)
	args := append([]string{"fetch", "-q", "--no-tags", "--prune", "--no-recurse-submodules", "--", repo}, originRefs...)
	return o.gitSandboxed(ctx, []string{work}, []string{repo}, work, args...)
}

// importBranch copies refs/heads/<branch> of the agent's clone src into ref
// of repo/, from where it is pushed. git fetch runs in repo/ (the app's
// config); only upload-pack reads src, and it runs in a sandbox that sees
// src read-only, so a .git pointing elsewhere reads nothing but src. The
// caller holds the repo lock and has checked the branch name.
func (o *Orchestrator) importBranch(ctx context.Context, projectID int64, src, branch, ref string) error {
	// upload-pack reads the agent's clone, which can make it block; the
	// caller holds the repo lock meanwhile.
	ctx, cancel := context.WithTimeout(ctx, cloneGitTimeout)
	defer cancel()
	up := append([]string{gitBin()}, hardening...)
	up = append(up, "upload-pack")
	if o.sandbox != nil {
		up, _ = o.sandbox(nil, []string{src}, src)(up, o.sandboxGitEnv())
	}
	err := git(ctx, "-C", o.repoDir(projectID), "-c", "fetch.fsckObjects=true",
		"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		"--upload-pack="+shellJoin(up), "--", src, "+refs/heads/"+branch+":"+ref)
	if err != nil {
		return fmt.Errorf("read branch %s from the run's clone: %w", branch, err)
	}
	return nil
}

// protectedPaths are path prefixes a pushed branch may not change: what is
// under .github/ (workflows, actions, CODEOWNERS) decides what CI runs, and
// CI may run with a token that can write to the repository.
var protectedPaths = []string{".github/"}

// checkChangedPaths refuses ref (in repo/, trusted) when it changes a
// protected path relative to its merge base with the default branch.
func checkChangedPaths(ctx context.Context, repo, ref, defaultBranch, branch string) error {
	base, err := gitOutput(ctx, repo, "merge-base", "refs/remotes/origin/"+defaultBranch, ref)
	if err != nil {
		return invalid("refusing to push %q: it shares no history with %q", branch, defaultBranch)
	}
	out, err := gitOutput(ctx, repo, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--no-textconv",
		strings.TrimSpace(base), ref, "--")
	if err != nil {
		return fmt.Errorf("list the paths %s changes: %w", branch, err)
	}
	var hit []string
	for _, p := range strings.Split(out, "\x00") {
		for _, pre := range protectedPaths {
			if len(p) >= len(pre) && strings.EqualFold(p[:len(pre)], pre) {
				hit = append(hit, p)
			}
		}
	}
	if len(hit) == 0 {
		return nil
	}
	if len(hit) > 5 {
		hit = append(hit[:5], "…")
	}
	return invalid("refusing to push %q: it changes %s. Files under .github/ decide what CI runs, so agents may not "+
		"change them. Undo those changes (restore them from %s), and describe the workflow change that is needed in "+
		"the PR body, or ask the user in a follow_up question, so the user can apply it", branch, strings.Join(hit, ", "), defaultBranch)
}

// checkBranchName accepts plain branch names only.
func checkBranchName(ctx context.Context, branch string) error {
	if branch == "" || branch == "HEAD" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "refs/") ||
		strings.ContainsAny(branch, ":+*?[\\ ~^") || gitQuiet(ctx, "", "check-ref-format", "refs/heads/"+branch) != nil {
		return invalid("invalid branch name %q", branch)
	}
	return nil
}

// removeWorkspace deletes a finished ticket's clone; the branch stays on
// GitHub and in repo/.
func (o *Orchestrator) removeWorkspace(_ context.Context, t domain.Ticket) {
	root, err := os.OpenRoot(filepath.Join(o.projectDir(t.ProjectID), "work"))
	if err != nil {
		return
	}
	defer root.Close()
	// Root.RemoveAll unlinks symlinks rather than following them.
	if err := root.RemoveAll(fmt.Sprint(t.ID)); err != nil {
		o.log.Warn("remove ticket clone", "ticket", t.ID, "err", err)
	}
	if home, err := os.OpenRoot(filepath.Join(o.projectDir(t.ProjectID), "home")); err == nil {
		if err := home.RemoveAll(fmt.Sprint(t.ID)); err != nil {
			o.log.Warn("remove ticket home", "ticket", t.ID, "err", err)
		}
		_ = home.Close()
	}
}

// Workspace layout versions (setting layoutKey). Before version 2 a run's
// sandbox could write its whole project directory, repo/ included, so
// nothing there can be trusted.
const (
	layoutKey     = "workspace_layout"
	layoutVersion = "2"
)

// migrateLayout moves project directories written under the old layout
// aside (projects/<id>.legacy-<time>; the app never reads them again) so
// repo/, home/ and the ticket clones are rebuilt from GitHub. Unpushed work
// stays in the moved directory for the operator.
func (o *Orchestrator) migrateLayout(ctx context.Context) error {
	if v, _, err := o.st.Setting(ctx, layoutKey); err != nil {
		return err
	} else if v == layoutVersion {
		return nil
	}
	dir := o.cfg.Path("projects")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	stamp := o.now().Format("20060102T150405")
	for _, e := range entries {
		if _, err := strconv.ParseInt(e.Name(), 10, 64); err != nil {
			continue
		}
		from := filepath.Join(dir, e.Name())
		to := from + ".legacy-" + stamp
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("move aside old project dir: %w", err)
		}
		o.log.Warn("moved aside a project directory from the old workspace layout", "to", to)
	}
	return o.st.SetSetting(ctx, layoutKey, layoutVersion)
}

// hardening goes before every git subcommand the app runs: no hooks, no
// fsmonitor, no transports but local paths, no submodules.
var hardening = []string{
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.fsmonitor=false",
	"-c", "protocol.allow=never",
	"-c", "protocol.file.allow=always",
	"-c", "submodule.recurse=false",
}

// gitEnv is the environment of every git the app runs on the host: the
// app's environment without GIT_* variables, no system or global config
// (so no filter, credential or other command a config file could add), and
// no prompts.
func gitEnv() []string {
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
}

// sandboxGitEnv is the environment of git in a sandbox.
func (o *Orchestrator) sandboxGitEnv() []string {
	return []string{"PATH=" + o.cfg.SandboxPATH, "HOME=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
}

var gitPath = sync.OnceValue(func() string {
	p, err := exec.LookPath("git")
	if err != nil {
		return "git"
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
})

// gitBin is git's resolved path, which sandboxes see through their
// read-only system binds.
func gitBin() string { return gitPath() }

// git runs hardened git on the host.
func git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, gitBin(), append(append([]string{}, hardening...), args...)...)
	cmd.Env = gitEnv()
	cmd.WaitDelay = gitWaitDelay
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitOutput runs hardened git on the host in dir and returns its stdout.
// dir must be the app's own repo/: never an agent-writable clone.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, gitBin(), append(append([]string{}, hardening...), append([]string{"-C", dir}, args...)...)...)
	cmd.Env = gitEnv()
	cmd.WaitDelay = gitWaitDelay
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// gitQuiet runs hardened git in dir (when set) and only reports failure.
func gitQuiet(ctx context.Context, dir string, args ...string) error {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, gitBin(), append(append([]string{}, hardening...), args...)...)
	cmd.Env = gitEnv()
	cmd.WaitDelay = gitWaitDelay
	return cmd.Run()
}

// gitSandboxed runs hardened git in dir inside a sandbox seeing rw and ro
// (unsandboxed when the orchestrator has no sandbox).
func (o *Orchestrator) gitSandboxed(ctx context.Context, rw, ro []string, dir string, args ...string) error {
	_, err := o.gitSandboxedOutput(ctx, rw, ro, dir, args...)
	return err
}

// gitSandboxedOutput is gitSandboxed returning stdout. The output comes
// from git reading agent-controlled files: treat it as untrusted.
func (o *Orchestrator) gitSandboxedOutput(ctx context.Context, rw, ro []string, dir string, args ...string) (string, error) {
	argv := append(append([]string{gitBin()}, hardening...), append([]string{"-C", dir}, args...)...)
	env := gitEnv()
	if o.sandbox != nil {
		argv, env = o.sandbox(rw, ro, dir)(argv, o.sandboxGitEnv())
	}
	ctx, cancel := context.WithTimeout(ctx, cloneGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.WaitDelay = gitWaitDelay
	// Output is capped: a hostile clone must not make the app buffer
	// without bound.
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = 1<<20, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// limitedBuffer keeps the first max bytes written to it.
type limitedBuffer struct {
	strings.Builder
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		if len(p) > room {
			b.Builder.Write(p[:room])
		} else {
			b.Builder.Write(p)
		}
	}
	return len(p), nil
}

// isSHA reports whether s is a full hex object name (SHA-1 or SHA-256).
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkoutPRHead makes the ticket's branch in its clone hold the head of its
// PR as GitHub reports it, before a verify or fix run: a commit pushed to the
// PR by someone else (GitHub's "Update branch", a bot) would otherwise leave
// the checkout stale. The branch is fast-forwarded (git reset --keep) when it
// has no commits beyond the PR head. When it has, a fix run keeps them, and a
// verify run checks the PR head out detached, so it tests exactly that
// commit while the branch keeps the unpushed work. It returns the PR head
// and whether the checkout is at it. Git in the clone runs sandboxed.
func (o *Orchestrator) checkoutPRHead(ctx context.Context, p domain.Project, t domain.Ticket, work string, detach bool) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, checkoutTimeout)
	defer cancel()
	pr, err := o.gh.Pull(ctx, p.Repo, t.PR)
	if err != nil {
		return "", false, fmt.Errorf("read PR #%d: %w", t.PR, err)
	}
	head := pr.HeadSHA
	if !isSHA(head) {
		return "", false, fmt.Errorf("PR #%d has no valid head commit (%q)", t.PR, head)
	}
	if pr.Branch != "" && pr.Branch != t.Branch {
		return "", false, fmt.Errorf("PR #%d is on branch %q, not the ticket's %q", t.PR, pr.Branch, t.Branch)
	}
	repo := o.repoDir(p.ID)
	remote := "refs/remotes/origin/" + t.Branch
	has := func() bool {
		out, err := gitOutput(ctx, repo, "rev-parse", "--verify", "--quiet", remote+"^{commit}")
		return err == nil && strings.TrimSpace(out) == head
	}
	if !has() {
		// GitHub's API can be ahead of the last fetch.
		if err := o.syncRepo(ctx, p); err != nil {
			return "", false, err
		}
		if !has() {
			return "", false, fmt.Errorf("the head %s of PR #%d is not on branch %s on GitHub", shortSHA(head), t.PR, t.Branch)
		}
		if err := o.refreshClone(ctx, p.ID, work); err != nil {
			return "", false, fmt.Errorf("refresh the ticket's clone: %w", err)
		}
	}
	rw, ro := []string{work}, []string(nil)
	sgit := func(args ...string) (string, error) {
		out, err := o.gitSandboxedOutput(ctx, rw, ro, work, args...)
		return strings.TrimSpace(out), err
	}
	local := "refs/heads/" + t.Branch
	if cur, err := currentBranch(ctx, work); err != nil || cur != t.Branch {
		args := []string{"checkout", "-q", t.Branch}
		if _, err := sgit("rev-parse", "--verify", "--quiet", local+"^{commit}"); err != nil {
			args = []string{"checkout", "-q", "-b", t.Branch, head}
		}
		if _, err := sgit(args...); err != nil {
			return "", false, fmt.Errorf("check out %s in the ticket's clone: %w", t.Branch, err)
		}
	}
	tip, err := sgit("rev-parse", "--verify", "--quiet", local+"^{commit}")
	if err != nil {
		return "", false, fmt.Errorf("read %s in the ticket's clone: %w", t.Branch, err)
	}
	if tip != head {
		if _, err := sgit("merge-base", "--is-ancestor", tip, head); err == nil {
			// No unpushed commits: move the branch to the PR head, keeping
			// uncommitted changes that do not conflict.
			if _, err := sgit("reset", "-q", "--keep", head); err != nil {
				return "", false, fmt.Errorf("update %s in the ticket's clone to the PR head %s: %w", t.Branch, shortSHA(head), err)
			}
		} else if detach {
			if _, err := sgit("checkout", "-q", "--detach", head); err != nil {
				return "", false, fmt.Errorf("check out the PR head %s in the ticket's clone: %w", shortSHA(head), err)
			}
		}
	}
	at, err := sgit("rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	return head, err == nil && at == head, nil
}

// shellJoin quotes argv for sh.
func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
