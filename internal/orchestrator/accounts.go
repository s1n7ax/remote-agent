package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// pickAccount returns the Claude account to run on: the highest-priority one
// with quota, preferring accounts not near their limit. When every account is
// exhausted it returns nil and the earliest reset time.
func (o *Orchestrator) pickAccount(ctx context.Context) (*domain.Account, time.Time, error) {
	accs, err := o.st.Accounts(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(accs) == 0 {
		return nil, time.Time{}, errors.New("no Claude account configured: add one in Settings")
	}
	now := o.now()
	var warned *domain.Account
	var earliest time.Time
	for i := range accs {
		a := &accs[i]
		if a.ExhaustedTill != nil && a.ExhaustedTill.After(now) {
			if earliest.IsZero() || a.ExhaustedTill.Before(earliest) {
				earliest = *a.ExhaustedTill
			}
			continue
		}
		if a.Warning {
			if warned == nil {
				warned = a
			}
			continue
		}
		return a, time.Time{}, nil
	}
	if warned != nil {
		return warned, time.Time{}, nil
	}
	// A minute of slack so the reset has really happened when we retry.
	return nil, earliest.Add(time.Minute), nil
}

func (o *Orchestrator) recordUsage(ctx context.Context, accountID int64, u agent.Usage) {
	if accountID == 0 {
		return
	}
	a, err := o.st.Account(ctx, accountID)
	if err != nil {
		return
	}
	a.Utilization = u.Utilization
	a.Warning = u.Warning
	if err := o.st.UpdateAccount(ctx, a); err != nil {
		o.log.Error("update account usage", "err", err)
	}
}

func (o *Orchestrator) markExhausted(ctx context.Context, accountID int64, till time.Time) {
	if accountID == 0 {
		return
	}
	a, err := o.st.Account(ctx, accountID)
	if err != nil {
		return
	}
	if till.IsZero() || !till.After(o.now()) {
		till = o.now().Add(time.Hour)
	}
	a.ExhaustedTill = &till
	a.Utilization = 1
	if err := o.st.UpdateAccount(ctx, a); err != nil {
		o.log.Error("mark account exhausted", "err", err)
	}
}

// resumeSession returns the session to resume for a run that continues a
// stage interrupted by a usage limit, copying the transcript into the new
// account's config dir when the account changed. It returns "" for a fresh
// start.
func (o *Orchestrator) resumeSession(ctx context.Context, ar *activeRun, cfgDir string) string {
	if ar.run.Agent != domain.AgentClaude {
		return ""
	}
	runs, err := o.st.TicketRuns(ctx, ar.ticket.ID)
	if err != nil {
		return ""
	}
	var prev *domain.Run
	for i := range runs {
		r := runs[i]
		if r.ID == ar.run.ID {
			continue
		}
		if r.Stage == ar.ticket.Stage && r.Status == domain.RunLimited && r.SessionID != "" && r.Agent == domain.AgentClaude {
			prev = &r
		}
		break // only the run right before this one counts
	}
	if prev == nil {
		return ""
	}
	if prev.AccountID == ar.run.AccountID {
		return prev.SessionID
	}
	oldDir := filepath.Join(o.homeDir(ar.project.ID), ".claude-accounts", fmt.Sprint(prev.AccountID), "projects")
	matches, _ := filepath.Glob(filepath.Join(oldDir, "*", prev.SessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	src := matches[0]
	dst := filepath.Join(cfgDir, "projects", filepath.Base(filepath.Dir(src)), filepath.Base(src))
	if err := copyFile(src, dst); err != nil {
		o.log.Warn("copy session for resume", "err", err)
		return ""
	}
	return prev.SessionID
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
