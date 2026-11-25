package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// maxFixRounds is how many CI/verify fix rounds a ticket gets before it fails.
const maxFixRounds = 2

// notifyFunc sends (or queues) a notification about a ticket.
type notifyFunc func(ev domain.NotifyEvent, t domain.Ticket, body string)

// settle records how a run ended and moves its ticket to the next state.
func (o *Orchestrator) settle(ctx context.Context, ar *activeRun, final agent.Event) {
	ar.mu.Lock()
	stop, shutdown, capped, limit := ar.stopRequested, ar.shutdown, ar.capped, ar.limit
	questions, prOpened, proof, summary, stageDone := ar.questions, ar.prOpened, ar.proof, ar.summary, ar.stageDone
	ar.mu.Unlock()
	if summary == "" {
		summary = final.Summary
	}
	ar.run.Summary = summary

	// The run record and the GitHub calls come first, outside tmu. The
	// ticket's next state is then decided on, and saved over, its current
	// row under tmu: the user may have answered the run's round (or a tool
	// call saved the ticket) since the run started.
	var verify verifyResult
	switch {
	case shutdown:
		o.finishRunRecord(ctx, ar, domain.RunFailed, "interrupted by a shutdown")
	case stop:
		o.finishRunRecord(ctx, ar, domain.RunStopped, "")
	case limit != nil:
		o.finishRunRecord(ctx, ar, domain.RunLimited, "usage limit")
	case capped != "":
		o.finishRunRecord(ctx, ar, domain.RunCapped, capped)
		if err := o.askSpendCap(ctx, ar.ticket, capped); err != nil {
			o.log.Error("ask spend cap", "err", err)
		}
		return
	case final.Err != nil:
		o.finishRunRecord(ctx, ar, domain.RunFailed, final.Err.Error())
	case !stageDone && !questions:
		o.finishRunRecord(ctx, ar, domain.RunFailed, "the run ended without stage_done")
	default:
		o.finishRunRecord(ctx, ar, domain.RunSucceeded, "")
		if !questions && ar.run.Stage == domain.StageVerify && proof != nil && proof.Verdict == "PASS" {
			verify = o.checkVerified(ctx, ar)
		}
	}

	var outbox []func()
	queue := func(ev domain.NotifyEvent, t domain.Ticket, body string) {
		outbox = append(outbox, func() { o.notify(ctx, ev, t, body) })
	}
	defer func() {
		for _, f := range outbox {
			f()
		}
	}()
	o.tmu.Lock()
	defer o.tmu.Unlock()
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		o.log.Error("settle: load ticket", "err", err)
		return
	}
	switch {
	case shutdown:
		t.Status = domain.StatusReady
		t.StatusNote = "will resume after the restart"
	case stop:
		t.Status = domain.StatusBacklog
		t.StatusNote = "stopped"
	case limit != nil:
		o.afterLimit(ctx, &t, queue)
	case final.Err != nil:
		t.Status = domain.StatusFailed
		t.StatusNote = final.Err.Error()
		queue(domain.NotifyRunFailed, t, "Run failed: "+final.Err.Error())
	case !stageDone && !questions:
		// The agent stopped without finishing its stage: resume it once,
		// then give up.
		if o.endedUnfinishedBefore(ctx, t) {
			t.Status = domain.StatusFailed
			t.StatusNote = "the agent ended the stage twice without finishing it"
			queue(domain.NotifyRunFailed, t, t.StatusNote)
		} else {
			t.Status = domain.StatusReady
			t.StatusNote = "resuming a stage the agent did not finish"
		}
	default:
		o.advance(ctx, &t, questions, prOpened, proof, ar.startSHA, verify, queue)
	}
	if err := o.save(ctx, &t); err != nil {
		o.log.Error("settle: save ticket", "err", err)
	}
}

// afterLimit re-queues a limited ticket on another account, or parks it until
// the earliest reset.
func (o *Orchestrator) afterLimit(ctx context.Context, t *domain.Ticket, notify notifyFunc) {
	acc, wait, err := o.pickAccount(ctx)
	if err != nil || acc != nil {
		t.Status = domain.StatusReady
		t.StatusNote = "switching Claude account after a usage limit"
		return
	}
	t.Status = domain.StatusWaiting
	t.WaitUntil = &wait
	t.StatusNote = "waiting until " + wait.Local().Format(time.RFC1123) + " for a usage-limit reset"
	notify(domain.NotifyLimited, *t, t.StatusNote)
}

// advance moves a ticket past a stage that succeeded. startSHA and verify
// are the commit a verify run tested and what GitHub said at its end.
func (o *Orchestrator) advance(ctx context.Context, t *domain.Ticket, questions, prOpened bool, proof *domain.Proof,
	startSHA string, verify verifyResult, notify notifyFunc) {
	if questions {
		// The run posted a round. If the user already answered it while the
		// run was finishing, roundCompleted has queued the next stage: keep
		// that instead of parking the ticket with no open round.
		if rd, err := o.OpenRound(ctx, t.ID); err != nil || rd != nil {
			t.Status = domain.StatusNeedsInput
		}
		return
	}
	t.StatusNote = ""
	switch t.Stage {
	case domain.StageExplore:
		t.Stage, t.Status = domain.StageDesign, domain.StatusReady
	case domain.StageDesign:
		t.Stage, t.Status = domain.StageBuild, domain.StatusReady
	case domain.StageBuild, domain.StageFix:
		if t.PR == 0 && !prOpened {
			t.Status = domain.StatusFailed
			t.StatusNote = "the run ended without opening a pull request"
			return
		}
		t.Stage, t.Status = domain.StageCI, domain.StatusWaiting
		t.StatusNote = "waiting for CI"
	case domain.StageVerify:
		switch {
		case proof == nil:
			t.Status = domain.StatusFailed
			t.StatusNote = "the verify run ended without submitting proof"
		case proof.Verdict == "PASS":
			markReady(t, startSHA, verify, notify)
		default:
			toFix(t, "verification failed", notify)
		}
	case domain.StageJob:
		t.Status = domain.StatusDone
		notify(domain.NotifyJobDone, *t, "Job finished")
	default:
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("no transition after stage %q", t.Stage)
	}
}

// toFix sends a ticket to a fix run, or fails it when it is out of rounds.
func toFix(t *domain.Ticket, why string, notify notifyFunc) {
	if t.FixRounds >= maxFixRounds {
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("%s after %d fix rounds", why, t.FixRounds)
		notify(domain.NotifyRunFailed, *t, t.StatusNote)
		return
	}
	t.FixRounds++
	t.Stage, t.Status = domain.StageFix, domain.StatusReady
	t.StatusNote = why
}

// verifyResult is GitHub's view of a PR at the end of a passing verify run.
type verifyResult struct {
	checked  bool
	pr       PullState
	err      error
	checkout string // the checkout's HEAD, read when the PR head moved
}

// checkVerified reads the PR after a passing verify run and, when its head
// is still the commit the run tested, takes it out of draft.
func (o *Orchestrator) checkVerified(ctx context.Context, ar *activeRun) verifyResult {
	v := verifyResult{checked: true}
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err == nil {
		v.pr, err = o.gh.Pull(ctx, ar.project.Repo, t.PR)
	}
	if err != nil {
		v.err = err
		return v
	}
	if ar.startSHA == "" || v.pr.HeadSHA != ar.startSHA {
		v.checkout, _ = headCommit(ctx, ar.workdir)
		return v
	}
	if v.pr.Draft {
		v.err = o.gh.MarkReady(ctx, ar.project.Repo, t.PR)
	}
	return v
}

// markReady records the commit a passing verify run tested as verified; the
// final CI wait then moves the ticket to in_review. When the PR head moved
// during the run, the new head is verified again, from the checkout when it
// holds that head.
func markReady(t *domain.Ticket, tested string, v verifyResult, notify notifyFunc) {
	t.VerifiedSHA = ""
	switch {
	case !v.checked:
		t.Status = domain.StatusFailed
		t.StatusNote = "could not check the PR after verification"
	case v.err != nil:
		t.Status = domain.StatusFailed
		t.StatusNote = "could not mark the PR ready: " + v.err.Error()
	case tested != "" && v.pr.HeadSHA == tested:
		t.Stage, t.Status = domain.StageCI, domain.StatusWaiting
		t.StatusNote = "verified; waiting for the final CI run"
		t.VerifiedSHA = tested
	case v.checkout != "" && v.checkout == v.pr.HeadSHA:
		t.Stage, t.Status = domain.StageVerify, domain.StatusReady
		t.StatusNote = fmt.Sprintf("the PR head moved during verification (tested %s, head is %s); verifying again",
			shortSHA(tested), shortSHA(v.pr.HeadSHA))
	default:
		// Re-verifying from a checkout that lacks the head would test the
		// old commit again, forever.
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("verified %s, but the PR head is %s, which the ticket's checkout (at %s) does not hold",
			shortSHA(tested), shortSHA(v.pr.HeadSHA), shortSHA(v.checkout))
		notify(domain.NotifyRunFailed, *t, t.StatusNote)
	}
}

// endedUnfinishedBefore reports whether the run before the latest one of the
// ticket's stage also ended without stage_done.
func (o *Orchestrator) endedUnfinishedBefore(ctx context.Context, t domain.Ticket) bool {
	runs, err := o.st.TicketRuns(ctx, t.ID)
	if err != nil || len(runs) < 2 {
		return false
	}
	prev := runs[1]
	return prev.Stage == t.Stage && prev.Status == domain.RunFailed && prev.Error == "the run ended without stage_done"
}
