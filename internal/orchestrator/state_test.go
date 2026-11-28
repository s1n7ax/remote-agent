package orchestrator

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// addPull adds an open PR straight to the fake.
func (g *fakeGitHub) addPull(p PullState) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.next
	g.next++
	p.Number = n
	if p.State == "" {
		p.State = "open"
	}
	if p.MergeableState == "" {
		p.MergeableState = "clean"
	}
	g.pulls[n] = &p
	return n
}

func (g *fakeGitHub) pushCalls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.pushes...)
}

func (g *fakeGitHub) mergeHeads() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.mergeSHAs...)
}

// Concurrent answers to one round must all be kept, and exactly one of them
// completes the round.
func TestConcurrentAnswersAndSubmitsCompleteRoundOnce(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	const n = 12
	for iter := 0; iter < 5; iter++ {
		issue := 100 + iter
		tk := h.newTicket(domain.Ticket{Status: domain.StatusNeedsInput, Stage: domain.StageExplore, Issue: issue})
		qs := make([]domain.Question, n)
		for i := range qs {
			qs[i] = domain.Question{ID: fmt.Sprintf("q%d", i), Kind: domain.QuestionText, Title: "Q"}
		}
		rd, err := h.st.AddRound(h.ctx, domain.Round{TicketID: tk.ID, Purpose: domain.PurposeRequirement,
			Stage: domain.StageDesign, Tree: domain.QuestionTree{Title: "R", Questions: qs}})
		if err != nil {
			t.Fatal(err)
		}

		// Concurrent answers keep every answer and complete nothing.
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				res, err := h.o.Answer(h.ctx, tk.ID, rd.N, fmt.Sprintf("q%d", i), domain.Answer{Text: fmt.Sprint("a", i)})
				if err == nil && res.Completed {
					err = fmt.Errorf("answer q%d completed the round", i)
				}
				if err != nil {
					errs <- err
				}
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("answer: %v", err)
		}

		got, err := h.st.Round(h.ctx, tk.ID, rd.N)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Answers) != n || got.CompletedAt != nil {
			t.Fatalf("round kept %d of %d answers (completedAt %v): %v", len(got.Answers), n, got.CompletedAt, got.Answers)
		}
		for i := 0; i < n; i++ {
			if a := got.Answers[fmt.Sprintf("q%d", i)]; a.Text != fmt.Sprint("a", i) {
				t.Fatalf("answer q%d = %+v", i, a)
			}
		}
		if cur := h.ticket(tk.ID); cur.Status != domain.StatusNeedsInput {
			t.Fatalf("ticket with every answer saved = %s/%s", cur.Status, cur.Stage)
		}

		// Concurrent submits complete the round once.
		var completed atomic.Int32
		start = make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if res, err := h.o.Submit(h.ctx, tk.ID, rd.N); err == nil && res.Completed {
					completed.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got, _ = h.st.Round(h.ctx, tk.ID, rd.N); completed.Load() != 1 || got.CompletedAt == nil {
			t.Fatalf("round completed %d times (completedAt %v), want once", completed.Load(), got.CompletedAt)
		}
		if cur := h.ticket(tk.ID); cur.Status != domain.StatusReady || cur.Stage != domain.StageDesign {
			t.Fatalf("ticket after the round = %s/%s", cur.Status, cur.Stage)
		}
		if cs := h.gh.commentsOn(issue); len(cs) != 1 {
			t.Fatalf("round summary posted %d times", len(cs))
		}
	}
}

// The user submits the round while the run that posted it is being settled:
// settle must not overwrite the ready ticket with its stale copy.
func TestSettleRacingSubmit(t *testing.T) {
	var hook atomic.Pointer[func()]
	h := newHarness(t, func(_ *harness, o *Options) {
		now := o.Now
		o.Now = func() time.Time {
			// Fire once, from inside settle (it reads the clock to stamp
			// the run record).
			if f := hook.Load(); f != nil && strings.Contains(string(debug.Stack()), "(*Orchestrator).settle(") && hook.CompareAndSwap(f, nil) {
				(*f)()
			}
			return now()
		}
	})
	tk := h.newTicket(domain.Ticket{})
	r := h.nextRun()
	tree := domain.QuestionTree{Title: "R", Questions: []domain.Question{{ID: "ok", Kind: domain.QuestionConfirm, Title: "Go?"}}}
	if _, err := h.o.ToolSubmitQuestions(h.ctx, r.ID(), tree, domain.PurposeRequirement); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.Answer(h.ctx, tk.ID, 1, "ok", domain.Answer{OptionIDs: []string{"yes"}}); err != nil {
		t.Fatal(err)
	}
	var answered atomic.Bool
	answer := func() {
		res, err := h.o.Submit(h.ctx, tk.ID, 1)
		if err != nil || !res.Completed {
			t.Errorf("submit = %+v, %v", res, err)
		}
		answered.Store(true)
	}
	hook.Store(&answer)
	r.Sess.Finish(agent.Event{Summary: "posted"})

	r2 := h.nextRun()
	if !answered.Load() {
		t.Fatal("the submit did not race settle")
	}
	if r2.Info.Stage != domain.StageDesign {
		t.Fatalf("next run stage = %s, want design", r2.Info.Stage)
	}
}

// A push to the PR after verification takes the ticket out of review and
// back to verify.
func TestInReviewHeadMovedReverifies(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	const verified = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	n := h.gh.addPull(PullState{Branch: "ra/1-x", HeadSHA: verified, CI: "success"})
	tk := h.newTicket(domain.Ticket{Stage: domain.StageCI, Status: domain.StatusInReview, PR: n, VerifiedSHA: verified})

	time.Sleep(100 * time.Millisecond) // several CI polls
	if got := h.ticket(tk.ID); got.Status != domain.StatusInReview {
		t.Fatalf("in_review ticket on the verified head moved to %s/%s", got.Status, got.Stage)
	}

	h.gh.update(n, func(p *PullState) { p.HeadSHA, p.CI = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "pending" })
	got := h.waitState(tk.ID, domain.StatusWaiting, domain.StageCI)
	if !strings.Contains(got.StatusNote, "moved") {
		t.Fatalf("note = %q", got.StatusNote)
	}
	if got.VerifiedSHA != "" {
		t.Fatalf("verified sha still %q", got.VerifiedSHA)
	}
}

// MergeAt merges only the verified head of a ticket's PR.
func TestMergeRefusesUnverifiedHead(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	const verified = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	n := h.gh.addPull(PullState{Branch: "ra/1-x", HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CI: "success"})
	tk := h.newTicket(domain.Ticket{Stage: domain.StageCI, Status: domain.StatusInReview, PR: n, VerifiedSHA: verified})

	err := h.o.MergeAt(h.ctx, h.project.ID, n, "squash", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "verified") {
		t.Fatalf("merge of an unverified head: err = %v, want ErrInvalid", err)
	}
	if m := h.gh.mergeCalls(); len(m) != 0 {
		t.Fatalf("merged an unverified head: %v", m)
	}
	// The refused merge rechecks CI, which sends the ticket back to CI, then
	// to testing; wait for that so no poll of the old head lands after the
	// steps below.
	h.waitState(tk.ID, domain.StatusReadyForTesting, domain.StageVerify)

	// Never verified at all.
	cur := h.ticket(tk.ID)
	cur.VerifiedSHA = ""
	if err := h.st.UpdateTicket(h.ctx, &cur); err != nil {
		t.Fatal(err)
	}
	if err := h.o.MergeAt(h.ctx, h.project.ID, n, "squash", verified); !errors.Is(err, ErrInvalid) {
		t.Fatalf("merge of an unverified ticket: err = %v, want ErrInvalid", err)
	}

	// Move the head first: while it differs, the CI poller rightly clears
	// VerifiedSHA and sends the ticket back to verify.
	h.gh.update(n, func(p *PullState) { p.HeadSHA = verified })
	cur.VerifiedSHA = verified
	cur.Stage, cur.Status = domain.StageCI, domain.StatusInReview
	if err := h.st.UpdateTicket(h.ctx, &cur); err != nil {
		t.Fatal(err)
	}
	// The user must have seen the verified head.
	if err := h.o.MergeAt(h.ctx, h.project.ID, n, "squash", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("merge without the head the user saw: err = %v, want ErrInvalid", err)
	}
	if err := h.o.MergeAt(h.ctx, h.project.ID, n, "squash", verified); err != nil {
		t.Fatal(err)
	}
	if m := h.gh.mergeHeads(); len(m) != 1 || m[0] != verified {
		t.Fatalf("merge required heads %v, want the verified one", m)
	}

	// A PR no ticket owns merges at the head the user saw.
	other := h.gh.addPull(PullState{Branch: "dependabot/x", HeadSHA: "cccccccccccccccccccccccccccccccccccccccc"})
	if err := h.o.MergeAt(h.ctx, h.project.ID, other, "", "dddddddddddddddddddddddddddddddddddddddd"); err == nil {
		t.Fatal("merged a head the user did not see")
	}
	if err := h.o.MergeAt(h.ctx, h.project.ID, other, "", "cccccccccccccccccccccccccccccccccccccccc"); err != nil {
		t.Fatal(err)
	}
}

// A ticket's run may push only the ticket's own branch.
func TestPushBranchRefusesOtherTicketsBranch(t *testing.T) {
	h := newHarness(t)
	h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	own := mustGit(t, r.Info.Workdir, "symbolic-ref", "--short", "HEAD")

	mustGit(t, r.Info.Workdir, "checkout", "-q", "-b", "ra/999-someone-else")
	commit(t, r.Info.Workdir, "x.txt", "x\n")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("push of another ticket's branch: err = %v, want ErrInvalid", err)
	}
	mustGit(t, r.Info.Workdir, "checkout", "-q", "-b", "side")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("push of a branch that is not the ticket's: err = %v, want ErrInvalid", err)
	}
	if p := h.gh.pushCalls(); len(p) != 0 {
		t.Fatalf("pushed %v", p)
	}
	mustGit(t, r.Info.Workdir, "checkout", "-q", own)
	if b, err := h.o.ToolPushBranch(h.ctx, r.ID()); err != nil || b != own {
		t.Fatalf("push of the ticket's branch = %q, %v", b, err)
	}
}

// A job (no branch of its own) may push PR branches, but not another
// ticket's goal branch.
func TestJobPushRefusesTicketBranches(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	other := h.newTicket(domain.Ticket{Status: domain.StatusBacklog})
	job := h.newTicket(domain.Ticket{Kind: domain.KindJob, Stage: domain.StageJob})
	h.o.Drain(false)
	r := h.nextRun()
	if r.Info.TicketID != job.ID {
		t.Fatalf("run for ticket %d, want the job", r.Info.TicketID)
	}

	mustGit(t, r.Info.Workdir, "checkout", "-q", "-b", fmt.Sprintf("ra/%d-add-dark-mode", other.ID))
	commit(t, r.Info.Workdir, "x.txt", "x\n")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("job push of ticket %d's branch: err = %v, want ErrInvalid", other.ID, err)
	}
	mustGit(t, r.Info.Workdir, "checkout", "-q", "main")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("job push of the default branch: err = %v, want ErrInvalid", err)
	}
	mustGit(t, r.Info.Workdir, "checkout", "-q", "-b", "deps/bump")
	if b, err := h.o.ToolPushBranch(h.ctx, r.ID()); err != nil || b != "deps/bump" {
		t.Fatalf("job push of a PR branch = %q, %v", b, err)
	}
	if p := h.gh.pushCalls(); !slices.Equal(p, []string{"deps/bump"}) {
		t.Fatalf("pushes = %v", p)
	}
}

// passVerify submits a passing proof and ends a verify run.
func passVerify(t *testing.T, h *harness, r *testRun) {
	t.Helper()
	writeFile(t, filepath.Join(r.Info.Workdir, "shot.png"), "\x89PNG\r\n\x1a\n")
	if _, err := h.o.ToolSubmitProof(h.ctx, r.ID(), "PASS", "ok", []string{"shot.png"}); err != nil {
		t.Fatal(err)
	}
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "verified"); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{})
}

// The verified SHA is the commit the verify run started on, not whatever the
// PR head is when it ends.
func TestVerifyRecordsTheTestedCommit(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, _ := buildToPR(t, h, tk)
	h.gh.setCI(n, "success")
	r := h.nextRun()
	if r.Info.Stage != domain.StageVerify {
		t.Fatalf("stage = %s", r.Info.Stage)
	}
	tested := mustGit(t, r.Info.Workdir, "rev-parse", "HEAD")

	// A commit lands on the PR while the run verifies the old one.
	commit(t, r.Info.Workdir, "late.txt", "late\n")
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); err != nil {
		t.Fatal(err)
	}
	moved := mustGit(t, r.Info.Workdir, "rev-parse", "HEAD")
	passVerify(t, h, r)
	h.waitState(tk.ID, domain.StatusWaiting, domain.StageCI)
	h.gh.setCI(n, "success") // the new head is tested once its CI is green

	r2 := h.nextRun()
	got := h.ticket(tk.ID)
	if r2.Info.Stage != domain.StageVerify || got.VerifiedSHA != "" {
		t.Fatalf("after the head moved mid-verify: stage %s, verified %q (tested %s, head %s)", r2.Info.Stage, got.VerifiedSHA, tested, moved)
	}
	if pr, _ := h.gh.Pull(h.ctx, "", n); !pr.Draft {
		t.Fatal("PR marked ready on an unverified head")
	}
	passVerify(t, h, r2)
	// CI is already green on the new head, so the final wait may pass at
	// once: wait for whichever state the poll leaves it in.
	got = h.waitTicket(tk.ID, "verified", func(t domain.Ticket) bool { return t.VerifiedSHA != "" })
	if got.VerifiedSHA != moved {
		t.Fatalf("verified sha = %q, want %q", got.VerifiedSHA, moved)
	}
}

// A PR head that never reaches the branch on GitHub cannot be checked out
// for the next verify run: the ticket fails instead of looping.
func TestVerifyOnUnfetchableHeadFails(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, _ := buildToPR(t, h, tk)
	h.gh.setCI(n, "success")
	r := h.nextRun()
	h.gh.update(n, func(p *PullState) { p.HeadSHA = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" })
	passVerify(t, h, r)
	got := h.waitTicket(tk.ID, "failed", func(t domain.Ticket) bool { return t.Status == domain.StatusFailed })
	if got.VerifiedSHA != "" || !strings.Contains(got.StatusNote, "eeeeeee") {
		t.Fatalf("ticket = verified %q note %q", got.VerifiedSHA, got.StatusNote)
	}
}
