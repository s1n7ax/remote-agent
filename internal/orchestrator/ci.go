package orchestrator

import (
	"context"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// pollCI checks the PR of every ticket waiting for CI or for review. Waiting
// costs no tokens: no agent runs until there is something to do.
func (o *Orchestrator) pollCI(ctx context.Context) error {
	ts, err := o.st.TicketsByStatus(ctx, domain.StatusWaiting, domain.StatusInReview)
	if err != nil {
		return err
	}
	now := o.now()
	for _, t := range ts {
		if t.PR == 0 || (t.Status == domain.StatusWaiting && t.Stage != domain.StageCI) {
			continue
		}
		o.mu.Lock()
		last := o.lastCI[t.ID]
		due := now.Sub(last) >= o.ciPoll
		if due {
			o.lastCI[t.ID] = now
		}
		o.mu.Unlock()
		if !due {
			continue
		}
		p, err := o.st.Project(ctx, t.ProjectID)
		if err != nil {
			return err
		}
		pr, err := o.gh.Pull(ctx, p.Repo, t.PR)
		if err != nil {
			o.log.Warn("poll PR", "ticket", t.ID, "pr", t.PR, "err", err)
			continue
		}
		if err := o.applyPull(ctx, t, pr); err != nil {
			return err
		}
	}
	return nil
}

// applyPull applies a polled PR to the ticket's current row. The poll took a
// network round trip, so the row read before it may be stale: a user action
// or a run may have changed the ticket meanwhile.
func (o *Orchestrator) applyPull(ctx context.Context, polled domain.Ticket, pr PullState) error {
	o.tmu.Lock()
	defer o.tmu.Unlock()
	t, err := o.st.Ticket(ctx, polled.ID)
	if err != nil {
		return err
	}
	waiting := t.Status == domain.StatusWaiting && t.Stage == domain.StageCI
	if t.PR != polled.PR || (!waiting && t.Status != domain.StatusInReview) {
		return nil // moved on while we polled
	}
	if o.onPull(ctx, &t, pr) {
		return o.save(ctx, &t)
	}
	return nil
}

// RecheckCI makes the next scheduler pass poll a ticket's PR immediately.
func (o *Orchestrator) RecheckCI(ticketID int64) {
	o.mu.Lock()
	delete(o.lastCI, ticketID)
	o.mu.Unlock()
	o.Kick()
}

// onPull applies a PR's state to its ticket and reports whether it changed.
func (o *Orchestrator) onPull(ctx context.Context, t *domain.Ticket, pr PullState) bool {
	notify := func(ev domain.NotifyEvent, t domain.Ticket, body string) { o.notify(ctx, ev, t, body) }
	switch {
	case pr.Merged:
		t.Status, t.Stage = domain.StatusDone, domain.StageMerged
		t.StatusNote = "merged"
		o.removeWorkspace(ctx, *t)
		o.notify(ctx, domain.NotifyMerged, *t, "PR merged")
		return true
	case pr.State == "closed":
		t.Status = domain.StatusFailed
		t.StatusNote = "the PR was closed without merging"
		return true
	case t.Status == domain.StatusInReview && pr.HeadSHA != t.VerifiedSHA:
		// Review (and Merge) are for the verified commit only. The new
		// head is tested once its CI is green.
		t.Stage, t.Status = domain.StageCI, domain.StatusWaiting
		t.StatusNote = "the PR head moved after verification; waiting for CI on " + shortSHA(pr.HeadSHA)
		t.VerifiedSHA = ""
		return true
	case pr.MergeableState == "dirty":
		toFix(t, fixConflict, "merge conflict with the base branch", notify)
		return true
	case pr.CI == "failure" && !checksPending(pr):
		// A fix run starts only once every check has finished, so it sees
		// all of the failures at once.
		toFix(t, fixCIFailed, "CI failed", notify)
		return true
	case pr.CI == "success" || pr.CI == "none":
		if t.Status == domain.StatusInReview {
			return false
		}
		if t.VerifiedSHA != "" && t.VerifiedSHA == pr.HeadSHA && !pr.Draft {
			t.Status = domain.StatusInReview
			t.StatusNote = "ready for your review"
			o.notify(ctx, domain.NotifyPRReady, *t, "PR is green and ready for your review")
			return true
		}
		t.Stage, t.Status = domain.StageVerify, domain.StatusReadyForTesting
		t.StatusNote = "CI is green; ready for testing"
		return true
	}
	return false // pending
}

// checksPending reports whether any check on the PR head is still running.
func checksPending(pr PullState) bool {
	for _, c := range pr.Checks {
		if c.Pending {
			return true
		}
	}
	return false
}
