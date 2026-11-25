// Package claude runs the official claude CLI in bidirectional stream-json
// mode. One session is one task: after the turn's result the session closes
// stdin and the CLI exits.
package claude

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/agent/proc"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Runner starts claude sessions.
type Runner struct {
	// Bin is the claude executable; "claude" when empty.
	Bin string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// InterruptAfter and KillAfter override the Stop escalation (tests).
	InterruptAfter, KillAfter time.Duration
}

var _ agent.Runner = Runner{}

// Args returns the CLI arguments for spec (without the executable).
func Args(spec agent.Spec) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	if spec.MCPConfig != "" {
		args = append(args, "--mcp-config", spec.MCPConfig)
	}
	if spec.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", spec.SystemPrompt)
	}
	if spec.Resume != "" {
		args = append(args, "--resume", spec.Resume)
	}
	return args
}

// Env returns the CLI environment for spec. ANTHROPIC_API_KEY is always
// removed: runs must bill the subscription login, never an API key.
func Env(spec agent.Spec) []string {
	base := map[string]string{"CLAUDE_CONFIG_DIR": spec.ConfigDir, "HOME": spec.Home}
	return proc.Env(base, spec.Env, "ANTHROPIC_API_KEY")
}

// Start launches claude and sends the first prompt.
func (r Runner) Start(ctx context.Context, spec agent.Spec) (agent.Session, error) {
	bin := r.Bin
	if bin == "" {
		bin = "claude"
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	s := &session{em: proc.NewEmitter(), parser: parser{now: now}}
	lim := proc.NewLimiter(20, 2)
	p, err := proc.Start(proc.Options{
		Argv:           append([]string{bin}, Args(spec)...),
		Env:            Env(spec),
		Dir:            spec.Workdir,
		Wrap:           spec.Wrap,
		OnStdout:       s.onLine,
		OnStderr:       proc.StderrSink(s.em, lim),
		InterruptAfter: r.InterruptAfter,
		KillAfter:      r.KillAfter,
	})
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	s.proc = p
	go s.finish(ctx, lim)
	if err := p.WriteJSON(userLine(spec.Prompt, "")); err != nil {
		_ = s.Stop()
		return nil, fmt.Errorf("claude: send prompt: %w", err)
	}
	return s, nil
}

// session is one running claude process.
type session struct {
	proc *proc.Process
	em   *proc.Emitter

	mu           sync.Mutex
	parser       parser
	final        *agent.Event // the turn's result, once seen
	interrupting bool         // an interrupt is in flight; swallow its result
	idle         bool         // interrupted, waiting for a message
	stopped      bool
	reqN         int
}

func (s *session) Events() <-chan agent.Event { return s.em.C() }

func (s *session) PID() int { return s.proc.PID() }

func (s *session) onLine(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evs, res := s.parser.handle(line)
	for _, ev := range evs {
		s.em.Emit(ev)
	}
	if res == nil || s.final != nil {
		return
	}
	if s.interrupting {
		s.interrupting, s.idle = false, true
		s.em.Log(domain.EventSystem, "turn interrupted; waiting for a message", nil)
		return
	}
	s.final = &agent.Event{Summary: res.summary, Cost: res.cost, Err: res.err}
	s.proc.Shutdown()
}

// finish waits for the process (stopping it if ctx ends) and sends Done.
func (s *session) finish(ctx context.Context, lim *proc.Limiter) {
	select {
	case <-s.proc.Done():
	case <-ctx.Done():
		_ = s.Stop()
		<-s.proc.Done()
	}
	proc.LogDropped(s.em, lim)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.final != nil:
		s.em.Finish(*s.final)
	case s.stopped:
		s.em.Finish(agent.Event{Err: agent.ErrStopped})
	default:
		err := s.proc.Err()
		if err == nil {
			err = errors.New("exit status 0")
		}
		s.em.Finish(agent.Event{Err: fmt.Errorf("claude exited without a result: %w", err)})
	}
}

// Send writes a user message, taken up at the next opportunity.
func (s *session) Send(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.final != nil || s.stopped {
		return agent.ErrEnded
	}
	if err := s.proc.WriteJSON(userLine(text, "next")); err != nil {
		return fmt.Errorf("claude: send: %w", err)
	}
	s.idle = false
	s.em.Log(domain.EventUser, text, nil)
	return nil
}

// Interrupt asks the CLI to stop the current turn.
func (s *session) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.final != nil || s.stopped {
		return agent.ErrEnded
	}
	if s.idle || s.interrupting {
		return nil
	}
	s.reqN++
	req := map[string]any{
		"type":       "control_request",
		"request_id": "interrupt-" + strconv.Itoa(s.reqN),
		"request":    map[string]string{"subtype": "interrupt"},
	}
	if err := s.proc.WriteJSON(req); err != nil {
		return fmt.Errorf("claude: interrupt: %w", err)
	}
	s.interrupting = true
	return nil
}

// Stop closes stdin, escalating to SIGINT and SIGKILL, and waits for exit.
func (s *session) Stop() error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.proc.Shutdown()
	<-s.proc.Done()
	return nil
}

// userLine is a stream-json user message.
func userLine(text, priority string) map[string]any {
	m := map[string]any{
		"type":               "user",
		"message":            map[string]string{"role": "user", "content": text},
		"parent_tool_use_id": nil,
	}
	if priority != "" {
		m["priority"] = priority
	}
	return m
}
