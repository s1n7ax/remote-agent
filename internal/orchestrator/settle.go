package orchestrator

import (
	"context"
	"fmt"
	"strings"
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
	stop, shutdown, capped, limit, timedOut := ar.stopRequested, ar.shutdown, ar.capped, ar.limit, ar.timedOut
	questions, prOpened, proof, summary := ar.questions, ar.prOpened, ar.proof, ar.summary
	missing := ar.missingOutcome()
	ar.mu.Unlock()
	if summary == "" {
		summary = final.Summary
	}
	ar.run.Summary = summary
	unfinished := unfinishedPrefix + missing

	// The run record and the GitHub calls come first, outside tmu. The
	// ticket's next state is then decided on, and saved over, its current
	// row under tmu: the user may have answered the run's round (or a tool
	// call saved the ticket) since the run started.
	var verify verifyResult
	switch {
	case shutdown:
		o.finishRunRecord(ctx, ar, domain.RunFailed, interruptedPrefix+"a shutdown")
	case stop:
		o.finishRunRecord(ctx, ar, domain.RunStopped, "")
	case timedOut > 0:
		o.finishRunRecord(ctx, ar, domain.RunFailed, timedOutAfter(timedOut))
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
	case missing != "":
		o.finishRunRecord(ctx, ar, domain.RunFailed, unfinished)
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
	case timedOut > 0:
		t.Status = domain.StatusFailed
		t.StatusNote = timedOutAfter(timedOut)
		queue(domain.NotifyRunFailed, t, "Run "+t.StatusNote)
	case limit != nil:
		o.afterLimit(ctx, &t, queue)
	case final.Err != nil:
		t.Status = domain.StatusFailed
		t.StatusNote = final.Err.Error()
		queue(domain.NotifyRunFailed, t, "Run failed: "+final.Err.Error())
	case missing != "":
		// The agent stopped without its stage's outcome: continue its
		// conversation once, then give up and show where it stopped.
		if o.endedUnfinishedBefore(ctx, t) {
			t.Status = domain.StatusFailed
			t.StatusNote = unfinished + ", twice"
			if last := lastWords(final.Summary); last != "" {
				t.StatusNote += ". The agent's last message: " + last
			}
			queue(domain.NotifyRunFailed, t, t.StatusNote)
		} else {
			t.Status = domain.StatusReady
			t.StatusNote = "continuing: " + unfinished
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
			toFix(t, fixVerifyFailed, "verification failed", notify)
		}
	case domain.StageJob:
		t.Status = domain.StatusDone
		notify(domain.NotifyJobDone, *t, "Job finished")
	default:
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("no transition after stage %q", t.Stage)
	}
}

// Fix triggers: why a ticket went to fix, kept for the fix run's prompt.
const (
	fixCIFailed     = "ci_failed"
	fixConflict     = "conflict"
	fixVerifyFailed = "verify_failed"
)

// toFix sends a ticket to a fix run, or fails it when it is out of rounds.
func toFix(t *domain.Ticket, trigger, why string, notify notifyFunc) {
	if t.FixRounds >= maxFixRounds {
		t.Status = domain.StatusFailed
		t.StatusNote = fmt.Sprintf("%s after %d fix rounds", why, t.FixRounds)
		notify(domain.NotifyRunFailed, *t, t.StatusNote)
		return
	}
	t.FixRounds++
	t.FixTrigger = trigger
	t.Stage, t.Status = domain.StageFix, domain.StatusReady
	t.StatusNote = why
}

// verifyResult is GitHub's view of a PR at the end of a passing verify run.
type verifyResult struct {
	checked bool
	pr      PullState
	err     error
}

// checkVerified reads the PR after a passing verify run and, when its head
// is still the commit the run tested, takes it out of draft. It compares
// GitHub's head with the head the run started on; it reads nothing from the
// agent's clone.
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
		return v
	}
	if v.pr.Draft {
		v.err = o.gh.MarkReady(ctx, ar.project.Repo, t.PR)
	}
	return v
}

// markReady records the commit a passing verify run tested as verified; the
// final CI wait then moves the ticket to in_review. When the PR head moved
// during the run, the ticket waits for CI on the new head, which is tested
// once it is green.
func markReady(t *domain.Ticket, tested string, v verifyResult, _ notifyFunc) {
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
	default:
		t.Stage, t.Status = domain.StageCI, domain.StatusWaiting
		t.StatusNote = fmt.Sprintf("the PR head moved during verification (tested %s, head is %s); waiting for CI on it",
			shortSHA(tested), shortSHA(v.pr.HeadSHA))
	}
}

// unfinishedPrefix starts the error of a run that ended without its stage's
// outcome.
const unfinishedPrefix = "the run ended without "

// interruptedPrefix starts the error of a run cut off by a shutdown or a
// restart.
const interruptedPrefix = "interrupted by "

// missingOutcome names the tool calls the run still owes its stage, or ""
// when it delivered: a question round, or its stage's outcome tool —
// stage_done for explore and job, which have no other. Callers hold ar.mu.
func (ar *activeRun) missingOutcome() string {
	if ar.questions {
		return ""
	}
	switch ar.run.Stage {
	case domain.StageBuild:
		if ar.prOpened || (ar.pushed && ar.ticket.PR != 0) {
			return ""
		}
		return "opening the pull request (push_branch, then open_pr)"
	case domain.StageFix:
		if ar.pushed {
			return ""
		}
		return "pushing the fix (push_branch)"
	case domain.StageVerify:
		if ar.proof != nil {
			return ""
		}
		return "a verdict (submit_proof)"
	case domain.StageDesign:
		return "the design round (submit_questions)"
	}
	if ar.stageDone {
		return ""
	}
	return "stage_done"
}

// lastWords shortens an agent's last message for a status note.
func lastWords(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

// endedUnfinishedBefore reports whether the run before the latest one of the
// ticket's stage also ended without its stage's outcome.
func (o *Orchestrator) endedUnfinishedBefore(ctx context.Context, t domain.Ticket) bool {
	runs, err := o.st.TicketRuns(ctx, t.ID)
	if err != nil || len(runs) < 2 {
		return false
	}
	prev := runs[1]
	return prev.Stage == t.Stage && prev.Status == domain.RunFailed && strings.HasPrefix(prev.Error, unfinishedPrefix)
}
