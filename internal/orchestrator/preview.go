package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// preview is the single live prototype preview (one at a time keeps RAM
// bounded on a small server). It is served on cfg.PreviewListen, which
// tailscale serve publishes to the tailnet.
type preview struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{} // closed when cmd exits
	ticketID int64
	branch   string
	target   *url.URL
}

// PreviewInfo describes the running preview.
type PreviewInfo struct {
	TicketID int64  `json:"ticketId"`
	Branch   string `json:"branch"`
	URL      string `json:"url"`
}

// ServePreview proxies the preview listener to the running preview.
func (o *Orchestrator) ServePreview(w http.ResponseWriter, r *http.Request) {
	o.prev.mu.Lock()
	target := o.prev.target
	o.prev.mu.Unlock()
	if target == nil {
		http.Error(w, "no preview is running; start one from the ticket", http.StatusServiceUnavailable)
		return
	}
	httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
}

// Preview returns the running preview, if any.
func (o *Orchestrator) Preview() *PreviewInfo {
	o.prev.mu.Lock()
	defer o.prev.mu.Unlock()
	if o.prev.target == nil {
		return nil
	}
	return &PreviewInfo{TicketID: o.prev.ticketID, Branch: o.prev.branch, URL: o.cfg.PreviewURL}
}

// StopPreview stops the running preview.
func (o *Orchestrator) StopPreview() {
	o.prev.mu.Lock()
	defer o.prev.mu.Unlock()
	o.stopPreviewLocked()
}

func (o *Orchestrator) stopPreviewLocked() {
	if o.prev.cmd != nil && o.prev.cmd.Process != nil {
		_ = syscall.Kill(-o.prev.cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-o.prev.done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-o.prev.cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	o.prev.cmd, o.prev.target, o.prev.ticketID, o.prev.branch = nil, nil, 0, ""
}

// StartPrototypePreview starts the preview of one of a ticket's prototypes.
func (o *Orchestrator) StartPrototypePreview(ctx context.Context, ticketID int64, name string) (*PreviewInfo, error) {
	ps, err := o.st.Prototypes(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.Name == name {
			if p.PreviewCmd == "" || p.PreviewPort == 0 {
				return nil, invalid("prototype %q has no preview command", name)
			}
			return o.startPreview(ctx, ticketID, p.Branch, p.PreviewCmd, p.PreviewPort)
		}
	}
	return nil, invalid("ticket has no prototype %q", name)
}

// ToolStartPreview starts a preview for the run's ticket.
func (o *Orchestrator) ToolStartPreview(ctx context.Context, token, branch, command string, port int) (*PreviewInfo, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return nil, err
	}
	if command == "" || port <= 0 || port > 65535 {
		return nil, invalid("start_preview needs a command and a port")
	}
	if branch == "" {
		branch, err = currentBranch(ctx, ar.workdir)
		if err != nil {
			return nil, err
		}
	}
	return o.startPreview(ctx, ar.ticket.ID, branch, command, port)
}

func (o *Orchestrator) startPreview(ctx context.Context, ticketID int64, branch, command string, port int) (*PreviewInfo, error) {
	t, err := o.st.Ticket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	o.prev.mu.Lock()
	defer o.prev.mu.Unlock()
	o.stopPreviewLocked()

	repo := o.repoDir(t.ProjectID)
	dir := filepath.Join(o.projectDir(t.ProjectID), "work", fmt.Sprintf("%d-preview", t.ID))
	if _, err := os.Stat(dir); err == nil {
		_ = git(ctx, "-C", repo, "worktree", "remove", "--force", dir)
	}
	if err := git(ctx, "-C", repo, "worktree", "add", "--detach", dir, branch); err != nil {
		return nil, err
	}
	argv := []string{"bash", "-lc", command}
	env := []string{"PATH=" + o.cfg.SandboxPATH, "HOME=" + o.homeDir(t.ProjectID), fmt.Sprintf("PORT=%d", port)}
	if o.sandbox != nil {
		argv, env = o.sandbox(o.projectDir(t.ProjectID), nil)(argv, env)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start preview: %w", err)
	}
	done := make(chan struct{})
	o.prev.cmd = cmd
	o.prev.done = done
	o.prev.ticketID = t.ID
	o.prev.branch = branch
	o.prev.target = &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	go func(c *exec.Cmd) {
		_ = c.Wait()
		close(done)
		o.prev.mu.Lock()
		if o.prev.cmd == c {
			o.prev.cmd, o.prev.target = nil, nil
		}
		o.prev.mu.Unlock()
	}(cmd)
	o.bus.Publish(Message{Type: "ticket", ProjectID: t.ProjectID, TicketID: t.ID, Data: t})
	return &PreviewInfo{TicketID: t.ID, Branch: branch, URL: o.cfg.PreviewURL}, nil
}

// pickedPrototype returns the prototype chosen in a completed round, if any.
func pickedPrototype(rds []domain.Round, ps []domain.Prototype) *domain.Prototype {
	for i := len(rds) - 1; i >= 0; i-- {
		a, ok := rds[i].Answers["prototype"]
		if !ok || len(a.OptionIDs) == 0 {
			continue
		}
		for j := range ps {
			if ps[j].Name == a.OptionIDs[0] {
				return &ps[j]
			}
		}
	}
	return nil
}
