package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// activeRun is the orchestrator's view of one running agent.
type activeRun struct {
	mu      sync.Mutex
	run     domain.Run
	ticket  domain.Ticket
	project domain.Project
	sess    agent.Session
	token   string
	workdir string
	browser bool

	// Set by MCP tool calls during the run.
	questions bool
	prOpened  bool
	proof     *domain.Proof
	stageDone bool
	summary   string

	// Set by the orchestrator or the user.
	stopRequested bool
	shutdown      bool
	paused        bool
	capped        string // reason, when a spend cap ended the run
	limit         *agent.Limit
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// usesBrowser reports whether a stage may drive a browser (and so takes the
// single browser slot and gets the browser MCP servers).
func usesBrowser(t domain.Ticket) bool {
	switch t.Stage {
	case domain.StageVerify, domain.StageJob:
		return true
	case domain.StageDesign:
		return t.Prototypes
	}
	return false
}

// startRun starts the agent for a ready ticket. started is false when the run
// must wait (no account with quota, browser slot taken).
func (o *Orchestrator) startRun(ctx context.Context, t domain.Ticket) (started bool, err error) {
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		return false, err
	}
	kind := t.Agent
	if o.cfg.FakeAgents {
		kind = domain.AgentFake
	}
	runner, ok := o.runners[kind]
	if !ok {
		return false, fmt.Errorf("no runner for agent %q", kind)
	}

	env := map[string]string{}
	var account domain.Account
	if kind == domain.AgentClaude {
		acc, wait, err := o.pickAccount(ctx)
		if err != nil {
			return false, err
		}
		if acc == nil {
			t.Status = domain.StatusWaiting
			t.WaitUntil = &wait
			t.StatusNote = "all Claude accounts are at their usage limit"
			return false, o.save(ctx, &t)
		}
		account = *acc
		tok, err := o.st.AccountToken(ctx, account.ID)
		if err != nil {
			return false, err
		}
		env["CLAUDE_CODE_OAUTH_TOKEN"] = tok
	}
	if kind == domain.AgentPi {
		key, err := config.ReadSecret(o.cfg.OpenRouterKeyFile)
		if err != nil {
			return false, err
		}
		if key == "" {
			return false, errors.New("no OpenRouter key configured")
		}
		if reason := o.overCap(ctx, t); reason != "" {
			return false, o.askSpendCap(ctx, t, reason)
		}
		env["OPENROUTER_API_KEY"] = key
	}

	browser := usesBrowser(t)
	if browser {
		select {
		case o.browser <- struct{}{}:
		default:
			return false, nil // another run holds the browser; try next pass
		}
	}
	releaseBrowser := func() {
		if browser {
			<-o.browser
		}
	}

	workdir, err := o.prepareWorkspace(ctx, p, &t)
	if err != nil {
		releaseBrowser()
		return false, err
	}

	run, err := o.st.CreateRun(ctx, domain.Run{
		TicketID: t.ID, Stage: t.Stage, Agent: kind, Model: t.Model, Effort: t.Effort,
		AccountID: account.ID, Status: domain.RunRunning,
	})
	if err != nil {
		releaseBrowser()
		return false, err
	}
	ar := &activeRun{run: run, ticket: t, project: p, token: newToken(), workdir: workdir, browser: browser}

	spec, err := o.buildSpec(ctx, ar, kind, account, env)
	if err != nil {
		releaseBrowser()
		o.finishRunRecord(ctx, ar, domain.RunFailed, err.Error())
		return false, err
	}

	// Register before starting so the agent's first MCP call finds its run.
	o.mu.Lock()
	o.active[t.ID] = ar
	o.byToken[ar.token] = ar
	o.mu.Unlock()

	sess, err := runner.Start(ctx, spec)
	if err != nil {
		o.unregister(ar)
		releaseBrowser()
		o.finishRunRecord(ctx, ar, domain.RunFailed, err.Error())
		return false, fmt.Errorf("start %s: %w", kind, err)
	}
	ar.sess = sess

	started := o.now()
	ar.run.StartedAt = &started
	_ = o.st.UpdateRun(ctx, ar.run)
	t.Status = domain.StatusWorking
	t.StatusNote = ""
	if err := o.save(ctx, &t); err != nil {
		o.log.Error("save ticket", "err", err)
	}
	ar.ticket = t
	o.event(ctx, ar, domain.EventSystem, fmt.Sprintf("started %s stage with %s %s %s", t.Stage, kind, t.Model, t.Effort))

	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer releaseBrowser()
		o.watch(context.WithoutCancel(ctx), ar)
	}()
	return true, nil
}

func (o *Orchestrator) unregister(ar *activeRun) {
	o.mu.Lock()
	delete(o.active, ar.ticket.ID)
	delete(o.byToken, ar.token)
	o.mu.Unlock()
}

// buildSpec prepares config dirs, MCP config and the prompt.
func (o *Orchestrator) buildSpec(ctx context.Context, ar *activeRun, kind domain.AgentKind, acc domain.Account, env map[string]string) (agent.Spec, error) {
	home := o.homeDir(ar.project.ID)
	var cfgDir string
	switch kind {
	case domain.AgentClaude:
		cfgDir = filepath.Join(home, ".claude-accounts", fmt.Sprint(acc.ID))
	case domain.AgentPi:
		cfgDir = filepath.Join(home, ".pi", "agent")
	default:
		cfgDir = filepath.Join(home, ".fake")
	}
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return agent.Spec{}, fmt.Errorf("create config dir: %w", err)
	}

	var skillsDir string
	if o.skills != nil {
		dir, err := o.skills.Snapshot(ctx)
		if err != nil {
			return agent.Spec{}, fmt.Errorf("skills snapshot: %w", err)
		}
		skillsDir = dir
		links := map[string]string{
			filepath.Join(cfgDir, "skills"):           dir,
			filepath.Join(cfgDir, "agents"):           filepath.Join(dir, "agents"),
			filepath.Join(home, ".agents", "skills"):  dir,
			filepath.Join(home, ".claude", "skills"):  dir,
			filepath.Join(home, ".claude", "agents"):  filepath.Join(dir, "agents"),
			filepath.Join(home, ".pi", "agent", "agents"): filepath.Join(dir, "agents"),
		}
		for link, target := range links {
			if err := relink(link, target); err != nil {
				return agent.Spec{}, err
			}
		}
	}

	mcpURL := "http://" + o.cfg.MCPListen + "/mcp"
	servers := map[string]any{}
	for name, s := range o.cfg.MCPServers {
		if !ar.browser && contains(o.cfg.BrowserMCPServers, name) {
			continue
		}
		entry := map[string]any{}
		if s.URL != "" {
			entry["type"] = "http"
			entry["url"] = s.URL
		} else {
			entry["command"] = s.Command
			entry["args"] = s.Args
			if len(s.Env) > 0 {
				entry["env"] = s.Env
			}
		}
		if kind == domain.AgentPi {
			entry["exposure"] = "direct"
		}
		servers[name] = entry
	}
	app := map[string]any{"type": "http", "url": mcpURL, "headers": map[string]string{"Authorization": "Bearer " + ar.token}}
	if kind == domain.AgentPi {
		app["exposure"] = "direct"
	}
	servers["remote-agent"] = app
	mcpJSON, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return agent.Spec{}, err
	}
	mcpPath := filepath.Join(cfgDir, "mcp.json")
	if err := os.WriteFile(mcpPath, mcpJSON, 0o600); err != nil {
		return agent.Spec{}, fmt.Errorf("write mcp config: %w", err)
	}

	prompt, system, err := o.prompt(ctx, ar)
	if err != nil {
		return agent.Spec{}, err
	}

	env["REMOTE_AGENT_MCP_URL"] = mcpURL
	env["REMOTE_AGENT_MCP_TOKEN"] = ar.token
	env["REMOTE_AGENT_STAGE"] = string(ar.ticket.Stage)
	env["REMOTE_AGENT_TICKET"] = fmt.Sprint(ar.ticket.ID)
	env["PATH"] = o.cfg.SandboxPATH
	env["GIT_AUTHOR_NAME"] = "remote-agent"
	env["GIT_AUTHOR_EMAIL"] = "remote-agent@localhost"
	env["GIT_COMMITTER_NAME"] = "remote-agent"
	env["GIT_COMMITTER_EMAIL"] = "remote-agent@localhost"
	if o.cfg.FakeAgents {
		for _, k := range []string{"FAKE_AGENT_SCRIPT", "FAKE_AGENT_SCRIPT_DIR"} {
			if v := os.Getenv(k); v != "" {
				env[k] = v
			}
		}
	}

	spec := agent.Spec{
		Agent: kind, Model: ar.ticket.Model, Effort: ar.ticket.Effort,
		Prompt: prompt, SystemPrompt: system,
		Workdir: ar.workdir, Home: home, ConfigDir: cfgDir, MCPConfig: mcpPath, Env: env,
	}
	if resume := o.resumeSession(ctx, ar, cfgDir); resume != "" {
		spec.Resume = resume
	}
	if o.sandbox != nil {
		ro := []string{}
		if skillsDir != "" {
			ro = append(ro, skillsDir)
		}
		spec.Wrap = o.sandbox(o.projectDir(ar.project.ID), ro)
	}
	return spec, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// relink points link at target, replacing whatever was there.
func relink(link, target string) error {
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(link), err)
	}
	_ = os.RemoveAll(link)
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("link %s: %w", link, err)
	}
	return nil
}

// watch consumes a session's events until it ends, then settles the ticket.
func (o *Orchestrator) watch(ctx context.Context, ar *activeRun) {
	var final agent.Event
	for ev := range ar.sess.Events() {
		switch {
		case ev.Log != nil:
			o.event(ctx, ar, ev.Log.Kind, ev.Log.Text, ev.Log.Data)
		case ev.SessionID != "":
			ar.run.SessionID = ev.SessionID
			_ = o.st.UpdateRun(ctx, ar.run)
		case ev.Usage != nil:
			o.recordUsage(ctx, ar.run.AccountID, *ev.Usage)
		case ev.Limit != nil:
			ar.mu.Lock()
			ar.limit = ev.Limit
			ar.mu.Unlock()
			o.event(ctx, ar, domain.EventSystem, "usage limit reached; resets "+ev.Limit.ResetsAt.Local().Format(time.RFC1123))
			o.markExhausted(ctx, ar.run.AccountID, ev.Limit.ResetsAt)
			_ = ar.sess.Stop()
		}
		if ev.Cost > 0 && !ev.Done {
			o.addCost(ctx, ar, ev.Cost)
		}
		if ev.Done {
			final = ev
		}
	}
	if final.Cost > 0 && ar.run.Agent != domain.AgentPi {
		// claude reports a run total at the end.
		ar.run.CostUSD = final.Cost
	}
	o.unregister(ar)
	o.settle(ctx, ar, final)
	o.Kick()
}

// event stores and publishes a run log line.
func (o *Orchestrator) event(ctx context.Context, ar *activeRun, kind domain.EventKind, text string, data ...json.RawMessage) {
	ev := domain.RunEvent{RunID: ar.run.ID, Kind: kind, Text: text}
	if len(data) > 0 {
		ev.Data = data[0]
	}
	saved, err := o.st.AddEvent(ctx, ev)
	if err != nil {
		o.log.Error("store run event", "err", err)
		return
	}
	o.bus.Publish(Message{Type: "run_event", ProjectID: ar.ticket.ProjectID, TicketID: ar.ticket.ID, Data: saved})
}

func (o *Orchestrator) finishRunRecord(ctx context.Context, ar *activeRun, st domain.RunStatus, msg string) {
	fin := o.now()
	ar.run.Status = st
	ar.run.Error = msg
	ar.run.FinishedAt = &fin
	if err := o.st.UpdateRun(ctx, ar.run); err != nil {
		o.log.Error("update run", "err", err)
	}
}

// SendMessage delivers a user message to a ticket's active run.
func (o *Orchestrator) SendMessage(ctx context.Context, ticketID int64, text string) error {
	ar := o.activeFor(ticketID)
	if ar == nil {
		return ErrNotActive
	}
	if err := ar.sess.Send(ctx, text); err != nil {
		return err
	}
	o.event(ctx, ar, domain.EventUser, text)
	return nil
}

// Pause freezes a ticket's run (SIGSTOP on its process group): no tokens are
// spent while paused.
func (o *Orchestrator) Pause(ctx context.Context, ticketID int64) error {
	return o.signal(ctx, ticketID, syscall.SIGSTOP, true)
}

// Resume continues a paused run.
func (o *Orchestrator) Resume(ctx context.Context, ticketID int64) error {
	return o.signal(ctx, ticketID, syscall.SIGCONT, false)
}

func (o *Orchestrator) signal(ctx context.Context, ticketID int64, sig syscall.Signal, pause bool) error {
	ar := o.activeFor(ticketID)
	if ar == nil {
		return ErrNotActive
	}
	pid := ar.sess.PID()
	if pid <= 0 {
		return ErrNotActive
	}
	// Runners start agents in their own process group.
	if err := syscall.Kill(-pid, sig); err != nil {
		if err := syscall.Kill(pid, sig); err != nil {
			return fmt.Errorf("signal run: %w", err)
		}
	}
	ar.mu.Lock()
	ar.paused = pause
	ar.mu.Unlock()
	if pause {
		ar.run.Status = domain.RunPaused
		o.event(ctx, ar, domain.EventSystem, "paused")
		o.notify(ctx, domain.NotifyPaused, ar.ticket, "Run paused")
	} else {
		ar.run.Status = domain.RunRunning
		o.event(ctx, ar, domain.EventSystem, "resumed")
	}
	return o.st.UpdateRun(ctx, ar.run)
}

// Stop ends a ticket's run; the ticket returns to the backlog.
func (o *Orchestrator) Stop(ctx context.Context, ticketID int64) error {
	ar := o.activeFor(ticketID)
	if ar == nil {
		return ErrNotActive
	}
	ar.mu.Lock()
	ar.stopRequested = true
	paused := ar.paused
	ar.mu.Unlock()
	if paused {
		_ = o.Resume(ctx, ticketID)
	}
	o.event(ctx, ar, domain.EventSystem, "stop requested")
	return ar.sess.Stop()
}

func (o *Orchestrator) activeFor(ticketID int64) *activeRun {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.active[ticketID]
}

// RunForToken returns the active run an MCP bearer token belongs to.
func (o *Orchestrator) runForToken(token string) *activeRun {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.byToken[token]
}
