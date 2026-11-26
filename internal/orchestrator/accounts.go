package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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

// sessionID is the shape of a Claude session id; the id comes from the
// agent's output, so it is checked before it names a file.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// resumeSession returns the session to resume for a run that continues a
// stage the run before it left unfinished (a usage limit, a run that ended
// without its outcome, a timeout, a restart), copying the transcript into the new
// account's config dir (cfgRel under home) when the account changed. It
// returns "" for a fresh start. home is agent-writable, so the copy goes
// through the os.Root at it: a transcript that is a symlink out of home is
// not read, and the destination is created afresh.
func (o *Orchestrator) resumeSession(ctx context.Context, ar *activeRun, home *os.Root, cfgRel string) string {
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
		continues := r.Status == domain.RunLimited || (r.Status == domain.RunFailed && leftUnfinished(r.Error))
		if r.Stage == ar.ticket.Stage && continues && r.SessionID != "" && r.Agent == domain.AgentClaude {
			prev = &r
		}
		break // only the run right before this one counts
	}
	if prev == nil || !sessionID.MatchString(prev.SessionID) {
		return ""
	}
	if prev.AccountID == ar.run.AccountID {
		return prev.SessionID
	}
	oldRel := filepath.Join(".claude-accounts", fmt.Sprint(prev.AccountID), "projects")
	d, err := home.Open(oldRel)
	if err != nil {
		return ""
	}
	entries, _ := d.ReadDir(-1)
	d.Close()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := prev.SessionID + ".jsonl"
		src := filepath.Join(oldRel, e.Name(), name)
		dst := filepath.Join(cfgRel, "projects", e.Name(), name)
		if err := copyIn(home, src, dst); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				o.log.Warn("copy session for resume", "err", err)
			}
			continue
		}
		return prev.SessionID
	}
	return ""
}

// leftUnfinished reports whether a failed run's error means its conversation
// was cut short (no outcome, a timeout, a restart) rather than broken: only
// such a conversation is worth continuing.
func leftUnfinished(runErr string) bool {
	for _, prefix := range []string{unfinishedPrefix, "timed out after", "interrupted by"} {
		if strings.HasPrefix(runErr, prefix) {
			return true
		}
	}
	return false
}

// copyIn copies regular file src to a new file dst, both under root.
func copyIn(root *os.Root, src, dst string) error {
	in, err := root.OpenFile(src, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	if err := ensureDir(root, filepath.Dir(dst)); err != nil {
		return err
	}
	if err := root.RemoveAll(dst); err != nil {
		return err
	}
	out, err := root.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
