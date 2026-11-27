package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// wontDoMarker marks the issue comment that carries a won't-do reason, so a
// second won't do edits it instead of adding another.
const wontDoMarker = "<!-- remote-agent:wont-do -->"

// WontDo closes a ticket as won't do: it moves to done with its stage kept,
// its open round is cancelled, an active run is stopped, and its issue
// (as not planned) and open PR are closed on GitHub. The ticket row is saved
// before GitHub is touched, so the CI poller never sees the closed PR on a
// ticket it still watches. GitHub failures do not undo it; the status note
// says what could not be closed.
func (o *Orchestrator) WontDo(ctx context.Context, id int64, reason string) (domain.Ticket, error) {
	reason = strings.TrimSpace(reason)
	t, err := o.update(ctx, id, func(t *domain.Ticket) error {
		if t.Status == domain.StatusDone {
			return invalid("ticket is done; only a ticket that is not done can be closed as won't do")
		}
		now := o.now()
		t.Status = domain.StatusDone
		t.WontDoAt = &now
		t.WontDoReason = reason
		t.WontDoClosedIssue, t.WontDoClosedPR = false, false
		t.StartAt, t.WaitUntil = nil, nil
		t.StatusNote = "won't do"
		// Under tmu, like Answer, so no answer can complete the round now.
		return o.cancelOpenRounds(ctx, t.ID)
	})
	if err != nil {
		return t, err
	}
	if o.activeFor(id) != nil {
		// settle leaves a won't-do ticket in done.
		if err := o.Stop(ctx, id); err != nil && !errors.Is(err, ErrNotActive) {
			o.log.Warn("won't do: stop run", "ticket", id, "err", err)
		}
	}
	if pv := o.Preview(); pv != nil && pv.TicketID == id {
		o.StopPreview()
	}

	closedIssue, closedPR, problems := o.closeOnGitHub(ctx, t, reason)
	marked := *t.WontDoAt
	cur, err := o.update(ctx, id, func(c *domain.Ticket) error {
		if c.WontDoAt == nil || !c.WontDoAt.Equal(marked) {
			return errUnchanged // reopened meanwhile
		}
		c.WontDoClosedIssue, c.WontDoClosedPR = closedIssue, closedPR
		if len(problems) > 0 {
			c.StatusNote = "won't do; " + strings.Join(problems, "; ")
		}
		return nil
	})
	if errors.Is(err, errUnchanged) {
		cur, err = o.st.Ticket(ctx, id)
	}
	if err != nil {
		return cur, err
	}
	o.bus.Publish(Message{Type: "board", ProjectID: cur.ProjectID})
	return cur, nil
}

// closeOnGitHub closes a won't-do ticket's open PR (unmerged) and its issue
// (as not planned), posting the reason on the issue. It reports what it
// closed and what it could not do.
func (o *Orchestrator) closeOnGitHub(ctx context.Context, t domain.Ticket, reason string) (closedIssue, closedPR bool, problems []string) {
	if t.PR == 0 && t.Issue == 0 {
		return false, false, nil
	}
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		return false, false, []string{"could not close anything on GitHub: " + err.Error()}
	}
	if t.PR != 0 {
		pr, err := o.gh.Pull(ctx, p.Repo, t.PR)
		if err == nil && pr.State == "open" && !pr.Merged {
			err = o.gh.SetPullState(ctx, p.Repo, t.PR, "closed")
			closedPR = err == nil
		}
		if err != nil {
			o.log.Warn("won't do: close PR", "ticket", t.ID, "pr", t.PR, "err", err)
			problems = append(problems, fmt.Sprintf("could not close PR #%d: %v", t.PR, err))
		}
	}
	if t.Issue != 0 {
		if reason != "" {
			body := wontDoMarker + "\nClosed as won't do on remote-agent: " + reason
			if err := o.gh.UpsertComment(ctx, p.Repo, t.Issue, wontDoMarker, body); err != nil {
				o.log.Warn("won't do: post reason", "ticket", t.ID, "err", err)
				problems = append(problems, fmt.Sprintf("could not post the reason on issue #%d: %v", t.Issue, err))
			}
		}
		if err := o.gh.SetIssueState(ctx, p.Repo, t.Issue, "closed", "not_planned"); err != nil {
			o.log.Warn("won't do: close issue", "ticket", t.ID, "issue", t.Issue, "err", err)
			problems = append(problems, fmt.Sprintf("could not close issue #%d: %v", t.Issue, err))
		} else {
			closedIssue = true
		}
	}
	return closedIssue, closedPR, problems
}

// cancelOpenRounds marks a ticket's unanswered rounds complete without
// answers; they stay in its history. The caller holds tmu.
func (o *Orchestrator) cancelOpenRounds(ctx context.Context, ticketID int64) error {
	rds, err := o.st.Rounds(ctx, ticketID)
	if err != nil {
		return err
	}
	for _, rd := range rds {
		if rd.CompletedAt != nil {
			continue
		}
		now := o.now()
		rd.CompletedAt = &now
		if err := o.st.SaveRound(ctx, rd); err != nil {
			return err
		}
	}
	return nil
}

// Reopen brings a won't-do ticket back to the backlog where it left off,
// and reopens the issue and the PR that won't do closed.
func (o *Orchestrator) Reopen(ctx context.Context, id int64) (domain.Ticket, error) {
	var before domain.Ticket
	t, err := o.update(ctx, id, func(t *domain.Ticket) error {
		if !t.WontDo() {
			return invalid("only a won't-do ticket can be reopened")
		}
		before = *t
		t.Status = domain.StatusBacklog
		t.WontDoAt, t.WontDoReason = nil, ""
		t.WontDoClosedIssue, t.WontDoClosedPR = false, false
		t.StatusNote = "reopened"
		return nil
	})
	if err != nil {
		return t, err
	}
	if problems := o.reopenOnGitHub(ctx, before); len(problems) > 0 {
		note := "reopened; " + strings.Join(problems, "; ")
		cur, err := o.update(ctx, id, func(c *domain.Ticket) error {
			if c.WontDo() || c.Status != domain.StatusBacklog {
				return errUnchanged
			}
			c.StatusNote = note
			return nil
		})
		if err == nil {
			t = cur
		}
	}
	o.bus.Publish(Message{Type: "board", ProjectID: t.ProjectID})
	return t, nil
}

// reopenOnGitHub reopens what won't do closed and reports what it could not.
func (o *Orchestrator) reopenOnGitHub(ctx context.Context, t domain.Ticket) []string {
	reopenIssue := t.WontDoClosedIssue && t.Issue != 0
	reopenPR := t.WontDoClosedPR && t.PR != 0
	if !reopenIssue && !reopenPR {
		return nil
	}
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		return []string{"could not reopen anything on GitHub: " + err.Error()}
	}
	var problems []string
	if reopenIssue {
		if err := o.gh.SetIssueState(ctx, p.Repo, t.Issue, "open", "reopened"); err != nil {
			o.log.Warn("reopen issue", "ticket", t.ID, "issue", t.Issue, "err", err)
			problems = append(problems, fmt.Sprintf("could not reopen issue #%d: %v", t.Issue, err))
		}
	}
	if reopenPR {
		if err := o.gh.SetPullState(ctx, p.Repo, t.PR, "open"); err != nil {
			o.log.Warn("reopen PR", "ticket", t.ID, "pr", t.PR, "err", err)
			problems = append(problems, fmt.Sprintf("could not reopen PR #%d: %v", t.PR, err))
		}
	}
	return problems
}
