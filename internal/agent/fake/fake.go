// Package fake is a deterministic Runner for tests and e2e: it plays a JSON
// script of steps (log, sleep, app MCP calls, limits, failures, shell
// commands) instead of calling a model.
package fake

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/agent/proc"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Runner starts fake sessions.
type Runner struct {
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

var _ agent.Runner = Runner{}

// Start loads the run's script and plays it in the background.
func (r Runner) Start(ctx context.Context, spec agent.Spec) (agent.Session, error) {
	steps, err := LoadScript(spec.Env)
	if err != nil {
		return nil, err
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{
		em:        proc.NewEmitter(),
		cancel:    cancel,
		messages:  make(chan string, 64),
		interrupt: make(chan struct{}, 1),
		done:      make(chan struct{}),
		now:       now,
		spec:      spec,
		mcp:       &mcpClient{url: lookup(spec.Env, EnvMCPURL), token: lookup(spec.Env, EnvMCPToken)},
	}
	go s.run(ctx, steps)
	return s, nil
}

// Session is a running script. Its PID is always 0: there is no process.
type Session struct {
	em        *proc.Emitter
	cancel    context.CancelFunc
	messages  chan string
	interrupt chan struct{}
	done      chan struct{}
	now       func() time.Time
	spec      agent.Spec
	mcp       *mcpClient

	mu      sync.Mutex
	sent    []string
	ended   bool
	stopped bool
}

func (s *Session) Events() <-chan agent.Event { return s.em.C() }

func (s *Session) PID() int { return 0 }

// Messages returns every message sent to the session, in order.
func (s *Session) Messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

// Send records and logs a message; a script waiting for one resumes.
func (s *Session) Send(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return agent.ErrEnded
	}
	s.sent = append(s.sent, text)
	s.em.Log(domain.EventUser, text, nil)
	select {
	case s.messages <- text:
	default: // a script that never waits need not hold every message
	}
	return nil
}

// Interrupt cuts the current sleep short; the script then waits for a
// message before going on.
func (s *Session) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return agent.ErrEnded
	}
	select {
	case s.interrupt <- struct{}{}:
	default:
	}
	return nil
}

// Stop ends the script and waits for it.
func (s *Session) Stop() error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.cancel()
	<-s.done
	return nil
}

func (s *Session) run(ctx context.Context, steps []Step) {
	defer close(s.done)
	defer s.mcp.close()
	s.em.Emit(agent.Event{SessionID: fmt.Sprintf("fake-%d", s.now().UnixNano())})
	final := s.play(ctx, steps)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	if ctx.Err() != nil {
		final = agent.Event{Err: agent.ErrStopped}
	}
	s.em.Finish(final)
	s.cancel()
}

// play runs the steps and returns the final event.
func (s *Session) play(ctx context.Context, steps []Step) agent.Event {
	summary := ""
	for _, st := range steps {
		if ctx.Err() != nil {
			return agent.Event{}
		}
		switch {
		case st.Log != "":
			s.em.Log(domain.EventText, st.Log, nil)
			summary = st.Log
		case st.SleepMS > 0:
			s.sleep(ctx, time.Duration(st.SleepMS)*time.Millisecond)
		case st.MCP != nil:
			if err := s.callMCP(ctx, st.MCP); err != nil {
				return agent.Event{Err: err}
			}
		case st.Limit != nil:
			typ := st.Limit.Type
			if typ == "" {
				typ = "five_hour"
			}
			at := s.now().Add(time.Duration(st.Limit.InS) * time.Second).UTC()
			s.em.Emit(agent.Event{Limit: &agent.Limit{ResetsAt: at, Type: typ}})
			return agent.Event{Summary: summary}
		case st.Fail != "":
			return agent.Event{Err: errors.New(st.Fail)}
		case st.Exec != "":
			if err := s.exec(ctx, st.Exec); err != nil {
				return agent.Event{Err: err}
			}
		case st.Cost != 0:
			s.em.Emit(agent.Event{Cost: st.Cost})
		case st.WaitMessage:
			s.waitMessage(ctx)
		case st.Done != nil:
			return agent.Event{Summary: *st.Done}
		}
	}
	return agent.Event{Summary: summary}
}

func (s *Session) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	case <-s.interrupt:
		s.em.Log(domain.EventSystem, "turn interrupted; waiting for a message", nil)
		s.waitMessage(ctx)
	}
}

func (s *Session) waitMessage(ctx context.Context) {
	select {
	case <-s.messages:
	case <-ctx.Done():
	}
}

func (s *Session) callMCP(ctx context.Context, call *MCPCall) error {
	data := map[string]any{"name": call.Tool, "input": call.Args}
	s.em.Log(domain.EventToolCall, proc.ToolCallText(call.Tool, call.Args), data)
	res, err := s.mcp.call(ctx, call)
	if err != nil {
		return err
	}
	text := proc.Truncate(resultText(res), proc.MaxToolOutput)
	s.em.Log(domain.EventToolResult, text, map[string]any{"isError": res.IsError})
	return nil
}

func (s *Session) exec(ctx context.Context, command string) error {
	argv := []string{"sh", "-c", command}
	env := proc.Env(map[string]string{"HOME": s.spec.Home}, s.spec.Env)
	if s.spec.Wrap != nil {
		argv, env = s.spec.Wrap(argv, env)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = s.spec.Workdir
	s.em.Log(domain.EventToolCall, "exec "+command, map[string]any{"name": "exec", "input": command})
	out, err := cmd.CombinedOutput()
	s.em.Log(domain.EventToolResult, proc.Truncate(string(out), proc.MaxToolOutput), map[string]any{"isError": err != nil})
	if err != nil {
		return fmt.Errorf("fake: exec %q: %w", command, err)
	}
	return nil
}
