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
		if changed := o.onPull(ctx, &t, pr); changed {
			if err := o.save(ctx, &t); err != nil {
				return err
			}
		}
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
	case pr.MergeableState == "dirty":
		o.toFix(ctx, t, "merge conflict with the base branch")
		return true
	case pr.CI == "failure":
		o.toFix(ctx, t, "CI failed")
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
		t.Stage, t.Status = domain.StageVerify, domain.StatusReady
		t.StatusNote = "CI is green; verifying"
		return true
	}
	return false // pending
}
