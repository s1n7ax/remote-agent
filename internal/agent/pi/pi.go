// Package pi runs the pi coding agent in RPC mode. One session is one task:
// once the run settles the session closes stdin and pi exits.
package pi

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

// Runner starts pi sessions.
type Runner struct {
	// Bin is the pi executable; "pi" when empty.
	Bin string
	// Provider is pi's --provider; "openrouter" when empty.
	Provider string
	// InterruptAfter and KillAfter override the Stop escalation (tests).
	InterruptAfter, KillAfter time.Duration
}

var _ agent.Runner = Runner{}

// Args returns pi's arguments for spec (without the executable).
func (r Runner) Args(spec agent.Spec) []string {
	provider := r.Provider
	if provider == "" {
		provider = "openrouter"
	}
	args := []string{"--mode", "rpc", "--provider", provider}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--thinking", spec.Effort)
	}
	if spec.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", spec.SystemPrompt)
	}
	if spec.Resume != "" {
		args = append(args, "--session", spec.Resume)
	}
	return args
}

// Env returns pi's environment for spec.
func Env(spec agent.Spec) []string {
	base := map[string]string{"PI_CODING_AGENT_DIR": spec.ConfigDir, "HOME": spec.Home}
	return proc.Env(base, spec.Env)
}

// Start launches pi and sends the first prompt.
func (r Runner) Start(ctx context.Context, spec agent.Spec) (agent.Session, error) {
	bin := r.Bin
	if bin == "" {
		bin = "pi"
	}
	s := &session{em: proc.NewEmitter()}
	lim := proc.NewLimiter(20, 2)
	p, err := proc.Start(proc.Options{
		Argv:           append([]string{bin}, r.Args(spec)...),
		Env:            Env(spec),
		Dir:            spec.Workdir,
		Wrap:           spec.Wrap,
		OnStdout:       s.onLine,
		OnStderr:       proc.StderrSink(s.em, lim),
		InterruptAfter: r.InterruptAfter,
		KillAfter:      r.KillAfter,
	})
	if err != nil {
		return nil, fmt.Errorf("pi: %w", err)
	}
	s.proc = p
	go s.finish(ctx, lim)
	err = p.WriteJSON(map[string]string{"id": promptID, "type": "prompt", "message": spec.Prompt})
	if err == nil {
		err = p.WriteJSON(map[string]string{"id": stateID, "type": "get_state"})
	}
	if err != nil {
		_ = s.Stop()
		return nil, fmt.Errorf("pi: send prompt: %w", err)
	}
	return s, nil
}

// session is one running pi process.
type session struct {
	proc *proc.Process
	em   *proc.Emitter

	mu           sync.Mutex
	parser       parser
	final        *agent.Event
	interrupting bool // an abort is in flight; its settle is not the end
	idle         bool // interrupted, waiting for a message
	stopped      bool
	reqN         int
}

func (s *session) Events() <-chan agent.Event { return s.em.C() }

func (s *session) PID() int { return s.proc.PID() }

func (s *session) onLine(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evs, out, err := s.parser.handle(line)
	for _, ev := range evs {
		s.em.Emit(ev)
	}
	for _, rec := range s.parser.reply {
		_ = s.proc.WriteJSON(rec)
	}
	s.parser.reply = nil
	if s.final != nil {
		return
	}
	switch out {
	case settled:
		if s.interrupting {
			s.interrupting, s.idle = false, true
			s.em.Log(domain.EventSystem, "turn interrupted; waiting for a message", nil)
			return
		}
		s.end(agent.Event{Summary: s.parser.lastText, Err: s.parser.runErr()})
	case failed:
		s.end(agent.Event{Err: err})
	case handled:
		s.end(agent.Event{})
	}
}

func (s *session) end(ev agent.Event) {
	s.final = &ev
	s.proc.Shutdown()
}

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
		s.em.Finish(agent.Event{Err: fmt.Errorf("pi exited before the run settled: %w", err)})
	}
}

// Send steers the running agent, or starts a new prompt after an interrupt.
func (s *session) Send(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.final != nil || s.stopped {
		return agent.ErrEnded
	}
	cmd := map[string]string{"type": "steer", "message": text}
	if s.idle {
		s.reqN++
		cmd = map[string]string{"id": "m" + strconv.Itoa(s.reqN), "type": "prompt", "message": text}
	}
	if err := s.proc.WriteJSON(cmd); err != nil {
		return fmt.Errorf("pi: send: %w", err)
	}
	s.idle = false
	s.em.Log(domain.EventUser, text, nil)
	return nil
}

// Interrupt aborts the current turn.
func (s *session) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.final != nil || s.stopped {
		return agent.ErrEnded
	}
	if s.idle || s.interrupting {
		return nil
	}
	if err := s.proc.WriteJSON(map[string]string{"type": "abort"}); err != nil {
		return fmt.Errorf("pi: abort: %w", err)
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
