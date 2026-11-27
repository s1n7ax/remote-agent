package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/sandbox"
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
	// startSHA is the PR head (as GitHub reported it) that a verify run's
	// checkout was put at before the run started: the commit it tests, and
	// the only one a PASS may mark verified.
	startSHA string

	// Set by MCP tool calls during the run.
	questions bool
	prOpened  bool
	pushed    bool
	proof     *domain.Proof
	stageDone bool
	summary   string

	// Set by the orchestrator or the user.
	stopRequested bool
	shutdown      bool
	paused        bool
	capped        string // reason, when a spend cap ended the run
	limit         *agent.Limit
	timeout       *time.Timer
	deadline      time.Time     // when the timeout fires, while running
	left          time.Duration // time left on the timeout, while paused
	timedOut      time.Duration // the limit, when the run timed out
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

	env, account, ready, err := o.agentCredentials(ctx, t, kind)
	if err != nil || !ready {
		return false, err
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
	var startSHA string
	if (t.Stage == domain.StageVerify || t.Stage == domain.StageFix) && t.PR != 0 {
		verify := t.Stage == domain.StageVerify
		head, atHead, err := o.checkoutPRHead(ctx, p, t, workdir, verify)
		if err == nil && verify && !atHead {
			err = fmt.Errorf("the ticket's checkout is not at the PR head %s", shortSHA(head))
		}
		if err != nil {
			releaseBrowser()
			return false, err
		}
		if verify {
			startSHA = head
		}
	}

	run, err := o.st.CreateRun(ctx, domain.Run{
		TicketID: t.ID, Stage: t.Stage, Agent: kind, Model: t.Model, Effort: t.Effort,
		AccountID: account.ID, Status: domain.RunRunning,
	})
	if err != nil {
		releaseBrowser()
		return false, err
	}
	ar := &activeRun{run: run, ticket: t, project: p, workdir: workdir, browser: browser, startSHA: startSHA}
	if o.register != nil {
		tok, err := o.register(RunInfo{RunID: run.ID, TicketID: t.ID, Stage: t.Stage, Workdir: workdir, ProjectDir: o.projectDir(p.ID)})
		if err != nil {
			releaseBrowser()
			o.finishRunRecord(ctx, ar, domain.RunFailed, err.Error())
			return false, fmt.Errorf("register run with the MCP server: %w", err)
		}
		ar.token = tok
	}

	spec, err := o.buildSpec(ctx, ar, kind, account, env)
	if err != nil {
		releaseBrowser()
		o.finishRunRecord(ctx, ar, domain.RunFailed, err.Error())
		return false, err
	}

	// Register before starting so the agent's first MCP call finds its run.
	o.mu.Lock()
	o.active[t.ID] = ar
	o.byRun[run.ID] = ar
	o.mu.Unlock()

	sess, err := runner.Start(ctx, spec)
	if err != nil {
		o.unregister(ar)
		releaseBrowser()
		o.finishRunRecord(ctx, ar, domain.RunFailed, err.Error())
		return false, fmt.Errorf("start %s: %w", kind, err)
	}
	limit := o.runTimeout(p)
	ar.mu.Lock()
	ar.sess = sess
	ar.deadline = time.Now().Add(limit)
	ar.timeout = time.AfterFunc(limit, func() { o.timeOut(context.WithoutCancel(ctx), ar, limit) })
	stopEarly := ar.stopRequested // Stop came before the session existed
	ar.mu.Unlock()

	startedAt := o.now()
	ar.run.StartedAt = &startedAt
	_ = o.st.UpdateRun(ctx, ar.run)
	// The agent is already running and may have saved the ticket through a
	// tool call, so update the current row rather than our copy. A ticket
	// closed as won't do while the run was starting stays done, and the
	// run is stopped.
	cur, err := o.update(ctx, t.ID, func(c *domain.Ticket) error {
		if c.WontDo() {
			return errUnchanged
		}
		c.Status = domain.StatusWorking
		c.StatusNote = ""
		c.Branch = t.Branch // set by prepareWorkspace
		return nil
	})
	switch {
	case errors.Is(err, errUnchanged):
		t = cur
		ar.mu.Lock()
		ar.stopRequested = true
		ar.mu.Unlock()
		stopEarly = true
	case err != nil:
		o.log.Error("save ticket", "err", err)
	default:
		t = cur
	}
	ar.ticket = t
	if stopEarly {
		_ = sess.Stop()
	}
	o.event(ctx, ar, domain.EventSystem, fmt.Sprintf("started %s stage with %s %s %s", t.Stage, kind, t.Model, t.Effort))

	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer releaseBrowser()
		o.watch(context.WithoutCancel(ctx), ar)
	}()
	return true, nil
}

// runTimeout is how long a run of project p may take.
func (o *Orchestrator) runTimeout(p domain.Project) time.Duration {
	if p.RunTimeoutMinutes > 0 {
		return time.Duration(p.RunTimeoutMinutes) * time.Minute
	}
	return o.runLimit
}

// timeOut stops a run that reached its time limit. Time the user keeps a
// run paused does not count: pausing stops the timer and resuming restarts
// it with the time that was left (see pauseTimeout).
func (o *Orchestrator) timeOut(ctx context.Context, ar *activeRun, limit time.Duration) {
	ar.mu.Lock()
	if ar.paused {
		ar.mu.Unlock()
		return // fired as the run paused; resuming restarts it
	}
	ar.timedOut = limit
	ar.mu.Unlock()
	o.event(ctx, ar, domain.EventSystem, timedOutAfter(limit))
	_ = ar.sess.Stop()
}

// timedOutPrefix starts the error of a run stopped at its time limit.
const timedOutPrefix = "timed out after "

// timedOutAfter is the error of a run stopped at time limit d, in whole
// minutes.
func timedOutAfter(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m == 1 {
		return timedOutPrefix + "1 minute"
	}
	return fmt.Sprintf("%s%d minutes", timedOutPrefix, m)
}

// agentCredentials picks the account and secrets a run of kind needs. Not
// ready (with no error) means the ticket now waits: every Claude account is
// at its limit, or the spend cap asks the user first.
func (o *Orchestrator) agentCredentials(ctx context.Context, t domain.Ticket, kind domain.AgentKind) (env map[string]string, account domain.Account, ready bool, err error) {
	env = map[string]string{}
	switch kind {
	case domain.AgentClaude:
		acc, wait, err := o.pickAccount(ctx)
		if err != nil {
			return nil, account, false, err
		}
		if acc == nil {
			_, err := o.mutate(ctx, t.ID, func(c *domain.Ticket) {
				c.Status = domain.StatusWaiting
				c.WaitUntil = &wait
				c.StatusNote = "all Claude accounts are at their usage limit"
			})
			return nil, account, false, err
		}
		account = *acc
		tok, err := o.st.AccountToken(ctx, account.ID)
		if err != nil {
			return nil, account, false, err
		}
		env["CLAUDE_CODE_OAUTH_TOKEN"] = tok
	case domain.AgentPi:
		key, err := config.ReadSecret(o.cfg.OpenRouterKeyFile)
		if err != nil {
			return nil, account, false, err
		}
		if key == "" {
			return nil, account, false, errors.New("no OpenRouter key configured")
		}
		if reason := o.overCap(ctx, t); reason != "" {
			return nil, account, false, o.askSpendCap(ctx, t, reason)
		}
		env["OPENROUTER_API_KEY"] = key
	}
	return env, account, true, nil
}

// revokeTools ends the run's MCP access.
func (o *Orchestrator) revokeTools(ar *activeRun) {
	o.mu.Lock()
	delete(o.byRun, ar.run.ID)
	o.mu.Unlock()
	if o.revoke != nil && ar.token != "" {
		o.revoke(ar.token)
	}
}

func (o *Orchestrator) unregister(ar *activeRun) {
	o.mu.Lock()
	delete(o.active, ar.ticket.ID)
	delete(o.byRun, ar.run.ID)
	o.mu.Unlock()
	if o.revoke != nil && ar.token != "" {
		o.revoke(ar.token)
	}
}

// buildSpec prepares config dirs, MCP config and the prompt. home/ is
// agent-writable, so every
// file operation there goes through an os.Root at home/: a planted symlink
// cannot make the app delete, write or read outside it.
func (o *Orchestrator) buildSpec(ctx context.Context, ar *activeRun, kind domain.AgentKind, acc domain.Account, env map[string]string) (agent.Spec, error) {
	home := o.homeDir(ar.ticket)
	hroot, err := os.OpenRoot(home)
	if err != nil {
		return agent.Spec{}, fmt.Errorf("open home: %w", err)
	}
	defer hroot.Close()
	var cfgRel string
	switch kind {
	case domain.AgentClaude:
		cfgRel = filepath.Join(".claude-accounts", fmt.Sprint(acc.ID))
	case domain.AgentPi:
		cfgRel = filepath.Join(".pi", "agent")
	default:
		cfgRel = ".fake"
	}
	cfgDir := filepath.Join(home, cfgRel)
	if err := ensureDir(hroot, cfgRel); err != nil {
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
			filepath.Join(cfgRel, "skills"):         dir,
			filepath.Join(cfgRel, "agents"):         filepath.Join(dir, "agents"),
			filepath.Join(".agents", "skills"):      dir,
			filepath.Join(".claude", "skills"):      dir,
			filepath.Join(".claude", "agents"):      filepath.Join(dir, "agents"),
			filepath.Join(".pi", "agent", "agents"): filepath.Join(dir, "agents"),
		}
		for link, target := range links {
			if err := relink(hroot, link, target); err != nil {
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
	if err := writeNew(hroot, filepath.Join(cfgRel, "mcp.json"), mcpJSON); err != nil {
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
	if resume := o.resumeSession(ctx, ar, hroot, cfgRel); resume != "" {
		spec.Resume = resume
	}
	if o.sandbox != nil {
		ro := []string{}
		if skillsDir != "" {
			ro = append(ro, skillsDir)
		}
		// Read-write: the ticket's own clone and $HOME only; not repo/, not
		// other tickets' clones.
		spec.Wrap = o.sandbox([]string{ar.workdir, home}, ro, ar.workdir)
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

// relink points link (relative to root) at target, replacing whatever was
// there.
func relink(root *os.Root, link, target string) error {
	if cur, err := root.Readlink(link); err == nil && cur == target {
		return nil
	}
	if err := ensureDir(root, filepath.Dir(link)); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(link), err)
	}
	// RemoveAll unlinks a symlink, never its target.
	if err := root.RemoveAll(link); err != nil {
		return fmt.Errorf("replace %s: %w", link, err)
	}
	if err := root.Symlink(target, link); err != nil {
		return fmt.Errorf("link %s: %w", link, err)
	}
	return nil
}

// ensureDir makes every component of rel under root a real directory,
// replacing a symlink or file the agent put in its place.
func ensureDir(root *os.Root, rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	p := ""
	for _, c := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		p = filepath.Join(p, c)
		fi, err := root.Lstat(p)
		if err == nil && fi.IsDir() {
			continue
		}
		if err == nil {
			if err := root.RemoveAll(p); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := root.Mkdir(p, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// writeNew replaces rel under root with a new file holding data: whatever
// was there (a symlink included) is removed, and the file is created
// exclusively so a link planted meanwhile is not followed.
func writeNew(root *os.Root, rel string, data []byte) error {
	if err := ensureDir(root, filepath.Dir(rel)); err != nil {
		return err
	}
	if err := root.RemoveAll(rel); err != nil {
		return err
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
	ar.mu.Lock()
	if ar.timeout != nil {
		ar.timeout.Stop()
	}
	ar.mu.Unlock()
	if final.Cost > 0 && ar.run.Agent != domain.AgentPi {
		// claude reports a run total at the end.
		ar.run.CostUSD = final.Cost
	}
	// Tool calls end before settle decides; the ticket stays active (user
	// actions get ErrBusy) until settle has saved, so a Start right after
	// Stop is not overwritten.
	o.revokeTools(ar)
	o.settle(ctx, ar, final)
	o.mu.Lock()
	delete(o.active, ar.ticket.ID)
	o.mu.Unlock()
	if ar.ticket.Kind == domain.KindJob {
		// Every job firing gets a fresh clone; drop it once the job ended.
		if t, err := o.st.Ticket(ctx, ar.ticket.ID); err == nil && (t.Status == domain.StatusDone || t.Status == domain.StatusFailed) {
			o.removeWorkspace(ctx, t)
		}
	}
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
	// The session logs the message as an EventUser line.
	return ar.sess.Send(ctx, text)
}

// Pause freezes a ticket's run: no tokens are spent while paused. SIGSTOP
// goes to the run's process group and, since an agent can leave that group
// (setsid), to every process in its sandbox's PID namespace and below it.
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
	// Runners start agents in their own process group; sandbox.Signal also
	// reaches processes that left it.
	if _, err := sandbox.Signal(pid, sig); err != nil {
		return fmt.Errorf("signal run: %w", err)
	}
	ar.mu.Lock()
	if ar.paused != pause {
		ar.pauseTimeout(pause)
	}
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

// pauseTimeout stops the run's timeout when it pauses and restarts it with
// the time that was left when it resumes. Callers hold ar.mu.
func (ar *activeRun) pauseTimeout(pause bool) {
	if ar.timeout == nil {
		return
	}
	if pause {
		ar.left = 0
		if ar.timeout.Stop() {
			ar.left = time.Until(ar.deadline)
		}
		return
	}
	ar.deadline = time.Now().Add(ar.left)
	ar.timeout.Reset(ar.left)
}

// Stop ends a ticket's run; the ticket returns to the backlog.
func (o *Orchestrator) Stop(ctx context.Context, ticketID int64) error {
	ar := o.activeFor(ticketID)
	if ar == nil {
		return ErrNotActive
	}
	ar.mu.Lock()
	ar.stopRequested = true
	paused, sess := ar.paused, ar.sess
	ar.mu.Unlock()
	if sess == nil {
		return nil // still starting: startRun stops it once it has started
	}
	if paused {
		_ = o.Resume(ctx, ticketID)
	}
	o.event(ctx, ar, domain.EventSystem, "stop requested")
	return sess.Stop()
}

func (o *Orchestrator) activeFor(ticketID int64) *activeRun {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.active[ticketID]
}
