package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// externalPush commits name on branch in a clone of origin other than the
// app's and pushes it, as GitHub's "Update branch" or a bot would, and
// returns the new head.
func externalPush(t *testing.T, h *harness, branch, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "elsewhere")
	mustGit(t, filepath.Dir(dir), "clone", "-q", h.gh.origin, dir)
	if branch != "main" {
		if gitQuiet(h.ctx, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch) == nil {
			mustGit(t, dir, "checkout", "-q", branch)
		} else {
			mustGit(t, dir, "checkout", "-q", "-b", branch)
		}
	}
	commit(t, dir, name, "from elsewhere\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	return mustGit(t, dir, "rev-parse", "HEAD")
}

// A commit pushed to the PR from elsewhere while CI runs is checked out
// before the verify run starts; that run verifies it and the ticket reaches
// in_review on it.
func TestVerifyChecksOutExternalPush(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, build := buildToPR(t, h, tk)
	branch := h.ticket(tk.ID).Branch

	moved := externalPush(t, h, branch, "update.txt")
	h.gh.update(n, func(p *PullState) { p.HeadSHA, p.CI = moved, "pending" })
	h.gh.setCI(n, "success")

	r := h.nextRun()
	if r.Info.Stage != domain.StageVerify {
		t.Fatalf("stage = %s", r.Info.Stage)
	}
	if r.Info.Workdir != build.Info.Workdir {
		t.Fatalf("verify got a new clone %s", r.Info.Workdir)
	}
	if got := mustGit(t, r.Info.Workdir, "rev-parse", "HEAD"); got != moved {
		t.Fatalf("verify checkout at %s, want the PR head %s", got, moved)
	}
	if b, err := currentBranch(h.ctx, r.Info.Workdir); err != nil || b != branch {
		t.Fatalf("checkout on %q (%v), want %s", b, err, branch)
	}
	if _, err := os.Stat(filepath.Join(r.Info.Workdir, "update.txt")); err != nil {
		t.Fatal("the external commit is not in the work tree")
	}
	passVerify(t, h, r)
	// CI is green on the head already: the final wait passes at once.
	got := h.waitState(tk.ID, domain.StatusInReview, domain.StageCI)
	if got.VerifiedSHA != moved {
		t.Fatalf("in review with verified %q, want %q", got.VerifiedSHA, moved)
	}
}

// A push from elsewhere during the verify run sends the ticket back to
// verify (not to failed), and the next run verifies the new head.
func TestExternalPushDuringVerifyReverifies(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, _ := buildToPR(t, h, tk)
	h.gh.setCI(n, "success")
	r := h.nextRun()
	tested := mustGit(t, r.Info.Workdir, "rev-parse", "HEAD")

	moved := externalPush(t, h, h.ticket(tk.ID).Branch, "bot.txt")
	h.gh.update(n, func(p *PullState) { p.HeadSHA, p.CI = moved, "pending" })
	passVerify(t, h, r)

	r2 := h.nextRun()
	got := h.ticket(tk.ID)
	if r2.Info.Stage != domain.StageVerify || got.VerifiedSHA != "" {
		t.Fatalf("after the head moved mid-verify: stage %s, verified %q", r2.Info.Stage, got.VerifiedSHA)
	}
	if head := mustGit(t, r2.Info.Workdir, "rev-parse", "HEAD"); head != moved || head == tested {
		t.Fatalf("second verify at %s, want %s", head, moved)
	}
	passVerify(t, h, r2)
	if got := h.waitTicket(tk.ID, "verified", func(t domain.Ticket) bool { return t.VerifiedSHA != "" }); got.VerifiedSHA != moved {
		t.Fatalf("verified %q, want %q", got.VerifiedSHA, moved)
	}
}

// A verify run on a branch with commits the PR does not have tests the PR
// head (detached); the branch keeps the commits.
func TestVerifyDetachesFromUnpushedCommits(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, build := buildToPR(t, h, tk)
	h.waitInactive(tk.ID)
	commit(t, build.Info.Workdir, "unpushed.txt", "local\n")
	local := mustGit(t, build.Info.Workdir, "rev-parse", "HEAD")
	pr, _ := h.gh.Pull(h.ctx, "", n)

	h.gh.setCI(n, "success")
	r := h.nextRun()
	if head := mustGit(t, r.Info.Workdir, "rev-parse", "HEAD"); head != pr.HeadSHA {
		t.Fatalf("verify at %s, want the PR head %s", head, pr.HeadSHA)
	}
	if tip := mustGit(t, r.Info.Workdir, "rev-parse", "refs/heads/"+h.ticket(tk.ID).Branch); tip != local {
		t.Fatalf("branch moved to %s, losing the unpushed %s", tip, local)
	}
	passVerify(t, h, r)
	if got := h.waitTicket(tk.ID, "verified", func(t domain.Ticket) bool { return t.VerifiedSHA != "" }); got.VerifiedSHA != pr.HeadSHA {
		t.Fatalf("verified %q, want %q", got.VerifiedSHA, pr.HeadSHA)
	}
}

// A FIFO planted as .git/HEAD cannot hang the scheduler when a verify run
// starts: git in the clone runs with a timeout, and the ticket fails.
func TestFIFOHeadDoesNotHangVerifyStart(t *testing.T) {
	oldGit, oldCheckout := cloneGitTimeout, checkoutTimeout
	cloneGitTimeout, checkoutTimeout = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { cloneGitTimeout, checkoutTimeout = oldGit, oldCheckout })

	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, build := buildToPR(t, h, tk)
	h.waitInactive(tk.ID)
	head := filepath.Join(build.Info.Workdir, ".git", "HEAD")
	if err := os.Remove(head); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(head, 0o600); err != nil {
		t.Fatal(err)
	}

	h.gh.setCI(n, "success")
	got := h.waitTicket(tk.ID, "failed", func(t domain.Ticket) bool { return t.Status == domain.StatusFailed })
	if got.Stage != domain.StageVerify {
		t.Fatalf("failed in stage %s: %s", got.Stage, got.StatusNote)
	}
	// The scheduler still starts other runs.
	other := h.newTicket(domain.Ticket{})
	if r := h.nextRun(); r.Info.TicketID != other.ID {
		t.Fatalf("run for %d, want %d", r.Info.TicketID, other.ID)
	}
}

// A push that changes anything under .github/ is refused, and nothing of it
// reaches GitHub or repo/'s branch.
func TestPushRefusesWorkflowChanges(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	wd := r.Info.Workdir
	branch, err := currentBranch(h.ctx, wd)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, wd, "feature.txt", "v1\n")
	commit(t, wd, ".github/workflows/ci.yml", "on: push\njobs: {}\n")

	_, err = h.o.ToolPushBranch(h.ctx, r.ID())
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), ".github/workflows/ci.yml") || !strings.Contains(err.Error(), "PR body") {
		t.Fatalf("push with a workflow change: err = %v", err)
	}
	if _, err := h.o.ToolOpenPR(h.ctx, r.ID(), "x", "y", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("open_pr with a workflow change: err = %v", err)
	}
	if p := h.gh.pushCalls(); len(p) != 0 {
		t.Fatalf("pushed %v", p)
	}
	repo := h.o.repoDir(h.project.ID)
	if gitQuiet(h.ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil {
		t.Fatal("the refused branch landed in repo/")
	}
	if out, _ := gitOutput(h.ctx, repo, "for-each-ref", "refs/remote-agent/"); strings.TrimSpace(out) != "" {
		t.Fatalf("staging refs left in repo/: %s", out)
	}

	// Other casings count too.
	mustGit(t, wd, "mv", ".github", ".GitHub")
	mustGit(t, wd, "commit", "-q", "-m", "rename")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("push with .GitHub/: err = %v", err)
	}

	// Once the branch no longer changes .github/ relative to main, it goes.
	mustGit(t, wd, "rm", "-q", "-r", ".GitHub")
	mustGit(t, wd, "commit", "-q", "-m", "drop the workflow")
	if b, err := h.o.ToolPushBranch(h.ctx, r.ID()); err != nil || b != branch {
		t.Fatalf("push without workflow changes = %q, %v", b, err)
	}
	_ = tk
}

// A change to an existing workflow (or its removal) is refused as well.
func TestPushRefusesWorkflowEdits(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	externalPush(t, h, "main", ".github/workflows/ci.yml")
	h.o.Drain(false)
	h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	mustGit(t, r.Info.Workdir, "rm", "-q", ".github/workflows/ci.yml")
	mustGit(t, r.Info.Workdir, "commit", "-q", "-m", "drop ci")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("push deleting a workflow: err = %v", err)
	}
}

// A job without a branch may push only a new branch nobody has; its first
// push claims it, and from then on it pushes only that one.
func TestJobPushesOnlyANewBranchOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	externalPush(t, h, "dependabot/x", "dep.txt")
	otherJob := h.newTicket(domain.Ticket{Kind: domain.KindJob, Stage: domain.StageJob, Status: domain.StatusDone, Branch: "chore/other-job"})
	job := h.newTicket(domain.Ticket{Kind: domain.KindJob, Stage: domain.StageJob})
	h.o.Drain(false)
	r := h.nextRun()
	if r.Info.TicketID != job.ID {
		t.Fatalf("run for ticket %d, want the job", r.Info.TicketID)
	}
	wd := r.Info.Workdir
	push := func(branch string) error {
		t.Helper()
		if gitQuiet(h.ctx, wd, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil {
			mustGit(t, wd, "checkout", "-q", branch)
		} else {
			mustGit(t, wd, "checkout", "-q", "-b", branch, "main")
		}
		_, err := h.o.ToolPushBranch(h.ctx, r.ID())
		return err
	}

	// Another PR's branch (on GitHub, no ticket of ours).
	if err := push("dependabot/x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("job push of a branch on GitHub: err = %v", err)
	}
	// Another job's stored branch, not on GitHub.
	if err := push("chore/other-job"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), fmt.Sprint(otherJob.ID)) {
		t.Fatalf("job push of job %d's branch: err = %v", otherJob.ID, err)
	}
	if p := h.gh.pushCalls(); len(p) != 0 {
		t.Fatalf("pushed %v", p)
	}

	if err := push("deps/bump"); err != nil {
		t.Fatal(err)
	}
	if got := h.ticket(job.ID).Branch; got != "deps/bump" {
		t.Fatalf("job branch = %q after its first push", got)
	}
	// Its own branch again (now on GitHub), but no second one.
	commit(t, wd, "more.txt", "more\n")
	if err := push("deps/bump"); err != nil {
		t.Fatalf("job re-push of its own branch: %v", err)
	}
	if err := push("deps/second"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("job push of a second branch: err = %v", err)
	}
	if p := h.gh.pushCalls(); len(p) != 2 || p[0] != "deps/bump" || p[1] != "deps/bump" {
		t.Fatalf("pushes = %v", p)
	}
}

// A user's patch racing the run's open_pr keeps the PR number (and the
// patch).
func TestPatchTicketRacingOpenPR(t *testing.T) {
	for i := range 5 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := newHarness(t)
			tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
			r := h.nextRun()
			commit(t, r.Info.Workdir, "feature.txt", "v1\n")

			var stop atomic.Bool
			var wg sync.WaitGroup
			var patches atomic.Int64
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for !stop.Load() {
						title := fmt.Sprintf("patched %d", patches.Add(1))
						if _, err := h.o.PatchTicket(h.ctx, tk.ID, TicketPatch{Title: &title}); err != nil {
							t.Error(err)
							return
						}
					}
				}()
			}
			pr, err := h.o.ToolOpenPR(h.ctx, r.ID(), "Add dark mode", "body", true)
			// Keep patching a little after open_pr saved the PR.
			time.Sleep(20 * time.Millisecond)
			stop.Store(true)
			wg.Wait()
			if err != nil {
				t.Fatal(err)
			}
			got := h.ticket(tk.ID)
			if got.PR != pr.Number || got.Branch == "" {
				t.Fatalf("after racing patches: PR %d branch %q, want PR %d", got.PR, got.Branch, pr.Number)
			}
			if !strings.HasPrefix(got.Title, "patched ") {
				t.Fatalf("title = %q", got.Title)
			}
		})
	}
}

// The merge button sends the head the user reviewed. A panel loaded while
// the ticket was verified at X cannot merge after it was verified again at
// Y.
func TestMergeRefusesStalePanel(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	const x = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const y = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	n := h.gh.addPull(PullState{Branch: "ra/1-x", HeadSHA: x, CI: "success"})
	tk := h.newTicket(domain.Ticket{Stage: domain.StageCI, Status: domain.StatusInReview, PR: n, VerifiedSHA: x})
	seen := h.ticket(tk.ID).VerifiedSHA // what the user's panel shows

	// The PR moves to Y and Y is verified while the panel stays open.
	h.gh.update(n, func(p *PullState) { p.HeadSHA = y })
	if _, err := h.o.mutate(h.ctx, tk.ID, func(t *domain.Ticket) { t.VerifiedSHA = y }); err != nil {
		t.Fatal(err)
	}
	err := h.o.MergeAt(h.ctx, h.project.ID, n, "squash", seen)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "changed since you looked") {
		t.Fatalf("merge from a stale panel: err = %v", err)
	}
	if m := h.gh.mergeCalls(); len(m) != 0 {
		t.Fatalf("merged %v", m)
	}
	if err := h.o.MergeAt(h.ctx, h.project.ID, n, "squash", y); err != nil {
		t.Fatal(err)
	}
	if m := h.gh.mergeHeads(); len(m) != 1 || m[0] != y {
		t.Fatalf("merge heads = %v", m)
	}
}

// The goal directory the app writes into the checkout never follows a
// symlink the agent planted.
func TestGoalDirSymlinkDoesNotEscape(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageExplore})
	r := h.nextRun()
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(r.Info.Workdir, ".ship")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Info.Workdir, ".ship")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.mutate(h.ctx, tk.ID, func(t *domain.Ticket) { t.Requirement = "# Req\n" }); err != nil {
		t.Fatal(err)
	}
	_ = r.Sess.Stop()
	h.waitInactive(tk.ID)
	if _, err := h.o.StartTicket(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	r2 := h.nextRun()
	_ = r2.Sess.Stop()
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("the app wrote through the planted symlink: %v", entries)
	}
	if fi, err := os.Lstat(filepath.Join(r2.Info.Workdir, ".ship")); err != nil || !fi.IsDir() {
		t.Fatalf(".ship is not a real directory: %v", err)
	}
}
