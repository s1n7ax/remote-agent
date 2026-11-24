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

// settle records how a run ended and moves its ticket to the next state.
func (o *Orchestrator) settle(ctx context.Context, ar *activeRun, final agent.Event) {
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		o.log.Error("settle: load ticket", "err", err)
		return
	}
	ar.mu.Lock()
	stop, shutdown, capped, limit := ar.stopRequested, ar.shutdown, ar.capped, ar.limit
	questions, prOpened, proof, summary, stageDone := ar.questions, ar.prOpened, ar.proof, ar.summary, ar.stageDone
	ar.mu.Unlock()
	if summary == "" {
		summary = final.Summary
	}
	ar.run.Summary = summary

	switch {
	case shutdown:
		o.finishRunRecord(ctx, ar, domain.RunFailed, "interrupted by a shutdown")
		t.Status = domain.StatusReady
		t.StatusNote = "will resume after the restart"
	case stop:
		o.finishRunRecord(ctx, ar, domain.RunStopped, "")
		t.Status = domain.StatusBacklog
		t.StatusNote = "stopped"
	case limit != nil:
		o.finishRunRecord(ctx, ar, domain.RunLimited, "usage limit")
		o.afterLimit(ctx, &t)
	case capped != "":
		o.finishRunRecord(ctx, ar, domain.RunCapped, capped)
		if err := o.askSpendCap(ctx, t, capped); err != nil {
			o.log.Error("ask spend cap", "err", err)
		}
		return
	case final.Err != nil:
		o.finishRunRecord(ctx, ar, domain.RunFailed, final.Err.Error())
		t.Status = domain.StatusFailed
		t.StatusNote = final.Err.Error()
		o.notify(ctx, domain.NotifyRunFailed, t, "Run failed: "+final.Err.Error())
	case !stageDone && !questions:
		// The agent stopped without finishing its stage: resume it once,
		// then give up.
		o.finishRunRecord(ctx, ar, domain.RunFailed, "the run ended without stage_done")
		if o.endedUnfinishedBefore(ctx, t) {
			t.Status = domain.StatusFailed
			t.StatusNote = "the agent ended the stage twice without finishing it"
			o.notify(ctx, domain.NotifyRunFailed, t, t.StatusNote)
		} else {
			t.Status = domain.StatusReady
			t.StatusNote = "resuming a stage the agent did not finish"
		}
	default:
		o.finishRunRecord(ctx, ar, domain.RunSucceeded, "")
		o.advance(ctx, &t, questions, prOpened, proof)
	}
	if err := o.save(ctx, &t); err != nil {
		o.log.Error("settle: save ticket", "err", err)
	}
}

// afterLimit re-queues a limited ticket on another account, or parks it until
// the earliest reset.
func (o *Orchestrator) afterLimit(ctx context.Context, t *domain.Ticket) {
	acc, wait, err := o.pickAccount(ctx)
	if err != nil || acc != nil {
		t.Status = domain.StatusReady
		t.StatusNote = "switching Claude account after a usage limit"
		return
	}
	t.Status = domain.StatusWaiting
	t.WaitUntil = &wait
	t.StatusNote = "waiting until " + wait.Local().Format(time.RFC1123) + " for a usage-limit reset"
	o.notify(ctx, domain.NotifyLimited, *t, t.StatusNote)
}

// advance moves a ticket past a stage that succeeded.
func (o *Orchestrator) advance(ctx context.Context, t *domain.Ticket, questions, prOpened bool, proof *domain.Proof) {
	if questions {
		// The run posted a round; submitQuestions already parked the ticket.
		t.Status = domain.StatusNeedsInput
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
			o.markReady(ctx, t)
		default:
			o.toFix(ctx, t, "verification failed")
		}
	case domain.StageJob:
		t.Status = domain.StatusDone
		o.notify(ctx, domain.NotifyJobDone, *t, "Job finished")
	default:
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("no transition after stage %q", t.Stage)
	}
}

// toFix sends a ticket to a fix run, or fails it when it is out of rounds.
func (o *Orchestrator) toFix(ctx context.Context, t *domain.Ticket, why string) {
	if t.FixRounds >= maxFixRounds {
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("%s after %d fix rounds", why, t.FixRounds)
		o.notify(ctx, domain.NotifyRunFailed, *t, t.StatusNote)
		return
	}
	t.FixRounds++
	t.Stage, t.Status = domain.StageFix, domain.StatusReady
	t.StatusNote = why
}

// markReady takes the PR out of draft after a passing verify; the final CI
// wait then moves the ticket to in_review.
func (o *Orchestrator) markReady(ctx context.Context, t *domain.Ticket) {
	p, err := o.st.Project(ctx, t.ProjectID)
	var pr PullState
	if err == nil {
		pr, err = o.gh.Pull(ctx, p.Repo, t.PR)
	}
	if err == nil && pr.Draft {
		err = o.gh.MarkReady(ctx, p.Repo, t.PR)
	}
	if err != nil {
		t.Status = domain.StatusFailed
		t.StatusNote = "could not mark the PR ready: " + err.Error()
		return
	}
	t.Stage, t.Status = domain.StageCI, domain.StatusWaiting
	t.StatusNote = "verified; waiting for the final CI run"
	t.VerifiedSHA = pr.HeadSHA
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
