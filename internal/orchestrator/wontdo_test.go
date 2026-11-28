package orchestrator

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

func (g *fakeGitHub) setFailState(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failState = err
}

// A ticket in review is closed as won't do: its PR and issue close, the CI
// poller leaves it alone, and Reopen plus Start bring it back through CI.
func TestWontDoInReviewThenReopen(t *testing.T) {
	h := newHarness(t)
	n := h.gh.addPull(PullState{Branch: "ra/1-x", HeadSHA: "abc", CI: "success",
		Checks: []Check{{Name: "test", Status: "completed", Conclusion: "success"}}})
	tk := h.newTicket(domain.Ticket{Status: domain.StatusInReview, Stage: domain.StageCI, Issue: 50, PR: n,
		VerifiedSHA: "abc", Branch: "ra/1-x", Requirement: "req", Decisions: "dec"})

	got, err := h.o.WontDo(h.ctx, tk.ID, "  superseded by #9  ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusDone || got.Stage != domain.StageCI || !got.WontDo() ||
		got.WontDoReason != "superseded by #9" || !got.WontDoClosedIssue || !got.WontDoClosedPR {
		t.Fatalf("after won't do: %+v", got)
	}
	if got.StatusNote != "won't do" {
		t.Fatalf("note = %q", got.StatusNote)
	}
	if pr, _ := h.gh.Pull(h.ctx, "", n); pr.State != "closed" || pr.Merged {
		t.Fatalf("PR = %+v, want closed and not merged", pr)
	}
	if st := h.gh.issueStateOf(50); st != "closed:not_planned" {
		t.Fatalf("issue state = %q", st)
	}
	cs := h.gh.commentsOn(50)
	if len(cs) != 1 || cs[0].Marker != wontDoMarker || !strings.Contains(cs[0].Body, "superseded by #9") {
		t.Fatalf("issue comments = %+v", cs)
	}

	// The poller would fail an in-review ticket whose PR closed; it must not
	// touch a won't-do one.
	time.Sleep(100 * time.Millisecond)
	h.noRun(50 * time.Millisecond)
	if cur := h.ticket(tk.ID); cur.Status != domain.StatusDone || !cur.WontDo() {
		t.Fatalf("won't-do ticket moved to %s (%q)", cur.Status, cur.StatusNote)
	}
	if _, err := h.o.WontDo(h.ctx, tk.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("won't do on a done ticket: err = %v", err)
	}
	if _, err := h.o.StartTicket(h.ctx, tk.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("start on a won't-do ticket: err = %v", err)
	}

	re, err := h.o.Reopen(h.ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if re.Status != domain.StatusBacklog || re.Stage != domain.StageCI || re.WontDo() || re.WontDoReason != "" ||
		re.PR != n || re.Issue != 50 || re.Branch != "ra/1-x" || re.Requirement != "req" || re.Decisions != "dec" {
		t.Fatalf("after reopen: %+v", re)
	}
	if pr, _ := h.gh.Pull(h.ctx, "", n); pr.State != "open" {
		t.Fatalf("PR after reopen = %s", pr.State)
	}
	if st := h.gh.issueStateOf(50); st != "open" {
		t.Fatalf("issue after reopen = %q", st)
	}
	if _, err := h.o.Reopen(h.ctx, tk.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reopen twice: err = %v", err)
	}

	if _, err := h.o.StartTicket(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	h.waitState(tk.ID, domain.StatusInReview, domain.StageCI)
}

// Won't do on a running ticket stops the run, cancels the round it posted,
// and leaves the ticket in done instead of the backlog. Reopen and Start
// rerun the stage it was in.
func TestWontDoStopsActiveRun(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Issue: 60})
	r := h.nextRun()
	tree := domain.QuestionTree{Title: "R", Questions: []domain.Question{{ID: "ok", Kind: domain.QuestionConfirm, Title: "Go?"}}}
	if _, err := h.o.ToolSubmitQuestions(h.ctx, r.ID(), tree, domain.PurposeRequirement); err != nil {
		t.Fatal(err)
	}

	if _, err := h.o.WontDo(h.ctx, tk.ID, ""); err != nil {
		t.Fatal(err)
	}
	h.waitInactive(tk.ID)
	if st := h.run(r.ID()).Status; st != domain.RunStopped {
		t.Fatalf("run status = %s", st)
	}
	got := h.ticket(tk.ID)
	if got.Status != domain.StatusDone || got.Stage != domain.StageExplore || !got.WontDo() {
		t.Fatalf("after won't do on a running ticket: %s/%s wontDo=%v note=%q", got.Status, got.Stage, got.WontDo(), got.StatusNote)
	}
	if rd, err := h.o.OpenRound(h.ctx, tk.ID); err != nil || rd != nil {
		t.Fatalf("open round after won't do = %+v, %v", rd, err)
	}
	if _, err := h.o.Answer(h.ctx, tk.ID, 1, "ok", domain.Answer{OptionIDs: []string{"yes"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("answer after won't do: err = %v", err)
	}
	if _, err := h.o.Submit(h.ctx, tk.ID, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("submit after won't do: err = %v", err)
	}
	if cs := h.gh.commentsOn(60); len(cs) != 0 {
		t.Fatalf("a reason comment was posted without a reason: %+v", cs)
	}
	h.noRun(100 * time.Millisecond)

	if _, err := h.o.Reopen(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.StartTicket(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	r2 := h.nextRun()
	if r2.Info.Stage != domain.StageExplore {
		t.Fatalf("reopened ticket ran %s, want explore", r2.Info.Stage)
	}
	r2.Sess.Finish(agent.Event{Err: errors.New("done with the test")})
	h.waitInactive(tk.ID)
}

// GitHub refusing the close does not undo won't do; the note says what
// could not be closed. A job without an issue or PR is closed cleanly.
func TestWontDoGitHubFailureAndJobs(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	n := h.gh.addPull(PullState{Branch: "ra/2-x", HeadSHA: "def", CI: "pending"})
	tk := h.newTicket(domain.Ticket{Status: domain.StatusWaiting, Stage: domain.StageCI, Issue: 70, PR: n})
	h.gh.setFailState(errors.New("boom"))

	got, err := h.o.WontDo(h.ctx, tk.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusDone || !got.WontDo() || got.WontDoClosedIssue || got.WontDoClosedPR {
		t.Fatalf("after a failed close: %+v", got)
	}
	for _, want := range []string{"could not close PR #", "could not close issue #70"} {
		if !strings.Contains(got.StatusNote, want) {
			t.Fatalf("note %q lacks %q", got.StatusNote, want)
		}
	}
	h.gh.setFailState(nil)
	// Reopen leaves alone what won't do did not close.
	if _, err := h.o.Reopen(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if st := h.gh.issueStateOf(70); st != "" {
		t.Fatalf("reopen touched an issue won't do did not close: %q", st)
	}

	job := h.newTicket(domain.Ticket{Kind: domain.KindJob, Stage: domain.StageJob, Status: domain.StatusBacklog})
	got, err = h.o.WontDo(h.ctx, job.ID, "not needed")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusDone || !got.WontDo() || got.StatusNote != "won't do" {
		t.Fatalf("job after won't do: %+v", got)
	}
}

// Won't do lands while a run is starting (the scheduler read the ticket as
// ready just before): the run is stopped and the ticket stays in done.
func TestWontDoWhileRunStarts(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	tk := h.newTicket(domain.Ticket{})
	stale := h.ticket(tk.ID)
	if _, err := h.o.WontDo(h.ctx, tk.ID, ""); err != nil {
		t.Fatal(err)
	}
	started, err := h.o.startRun(h.ctx, stale)
	if err != nil || !started {
		t.Fatalf("startRun = %v, %v", started, err)
	}
	r := <-h.runner.starts
	h.waitInactive(tk.ID)
	if !r.Sess.isEnded() {
		t.Fatal("the run of a won't-do ticket kept going")
	}
	if st := h.run(r.ID()).Status; st != domain.RunStopped {
		t.Fatalf("run status = %s", st)
	}
	if got := h.ticket(tk.ID); got.Status != domain.StatusDone || !got.WontDo() {
		t.Fatalf("ticket = %s wontDo=%v note=%q", got.Status, got.WontDo(), got.StatusNote)
	}
}
