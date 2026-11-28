// Package orchestrator moves tickets through the /ship stages: it queues and
// starts agent runs, reacts to their results, rotates Claude accounts on usage
// limits, enforces spend caps, waits for CI and fires schedules.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// Notifier delivers push notifications.
type Notifier interface {
	Notify(ctx context.Context, ev domain.NotifyEvent, title, body, url string)
}

// Skills provides the skills snapshot a run mounts.
type Skills interface {
	// Snapshot returns a directory holding the current skills (one
	// subdirectory per skill, plus agents/).
	Snapshot(ctx context.Context) (string, error)
}

// Options wires an orchestrator.
type Options struct {
	Config   config.Config
	Store    *store.Store
	GitHub   GitHub
	Runners  map[domain.AgentKind]agent.Runner
	Notifier Notifier
	Skills   Skills
	Bus      *Bus
	Log      *slog.Logger
	// Now is the clock; tests replace it.
	Now func() time.Time
	// Sandbox wraps a command so it sees only the read-write dirs rw, the
	// read-only dirs ro and system paths, starting in workdir. It confines
	// agent runs, previews and the git the app runs on agent-writable
	// clones; nil runs them unsandboxed (tests only).
	Sandbox func(rw, ro []string, workdir string) func(argv, env []string) ([]string, []string)
	// Register gives a starting run its MCP bearer token; Revoke ends it.
	Register func(RunInfo) (token string, err error)
	Revoke   func(token string)
	// Tick is the scheduler period (default 5s).
	Tick time.Duration
	// CIPoll is how often waiting PRs are polled (default 60s).
	CIPoll time.Duration
	// RunTimeout stops a run that takes longer, unless its project sets
	// its own (default: the config's runTimeoutMinutes).
	RunTimeout time.Duration
}

// RunInfo describes a starting run to the MCP server.
type RunInfo struct {
	RunID      int64
	TicketID   int64
	Stage      domain.Stage
	Workdir    string
	ProjectDir string
}

// Orchestrator is the scheduler and state machine.
type Orchestrator struct {
	cfg      config.Config
	st       *store.Store
	gh       GitHub
	runners  map[domain.AgentKind]agent.Runner
	notifier Notifier
	skills   Skills
	bus      *Bus
	log      *slog.Logger
	now      func() time.Time
	tick     time.Duration
	ciPoll   time.Duration
	runLimit time.Duration
	sandbox  func(rw, ro []string, workdir string) func(argv, env []string) ([]string, []string)
	register func(RunInfo) (string, error)
	revoke   func(string)
	prev     preview

	kick    chan struct{}
	browser chan struct{} // one slot: runs that drive a browser

	// tmu serialises mutate's read-modify-write of ticket rows, which
	// the run watcher, MCP tool calls and the API make concurrently.
	tmu sync.Mutex
	// claimMu serialises job runs claiming a new branch.
	claimMu sync.Mutex
	// linkMu serialises adding links, so a loop check sees every link.
	linkMu sync.Mutex

	mu       sync.Mutex
	active   map[int64]*activeRun // by ticket id
	byRun    map[int64]*activeRun
	draining bool
	lastCI   map[int64]time.Time
	// piImages holds the pi models that can look at images.
	piImages        map[string]bool
	lastUploadSweep time.Time
	repoMu          map[int64]*sync.Mutex // serialises writes to a project's repo/
	wg              sync.WaitGroup
}

// New returns an orchestrator; call Run to start it.
func New(o Options) *Orchestrator {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.Tick == 0 {
		o.Tick = 5 * time.Second
	}
	if o.CIPoll == 0 {
		o.CIPoll = 60 * time.Second
	}
	if o.RunTimeout == 0 {
		o.RunTimeout = time.Duration(o.Config.RunTimeoutMinutes) * time.Minute
	}
	if o.RunTimeout <= 0 {
		o.RunTimeout = 120 * time.Minute
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Bus == nil {
		o.Bus = NewBus()
	}
	return &Orchestrator{
		cfg: o.Config, st: o.Store, gh: o.GitHub, runners: o.Runners, notifier: o.Notifier,
		skills: o.Skills, bus: o.Bus, log: o.Log, now: o.Now, tick: o.Tick, ciPoll: o.CIPoll, runLimit: o.RunTimeout,
		sandbox: o.Sandbox, register: o.Register, revoke: o.Revoke,
		kick:    make(chan struct{}, 1),
		browser: make(chan struct{}, 1),
		active:  map[int64]*activeRun{},
		byRun:   map[int64]*activeRun{},
		lastCI:  map[int64]time.Time{},
		repoMu:  map[int64]*sync.Mutex{},
	}
}

// Bus returns the live-update bus.
func (o *Orchestrator) Bus() *Bus { return o.bus }

// Kick wakes the scheduler loop early.
func (o *Orchestrator) Kick() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// Run drives the scheduler until ctx ends, then stops every active run.
func (o *Orchestrator) Run(ctx context.Context) error {
	for _, f := range []func(context.Context) error{o.migrateLayout, o.recover} {
		if err := f(ctx); err != nil {
			if ctx.Err() != nil {
				return nil // stopped while starting
			}
			return err
		}
	}
	t := time.NewTicker(o.tick)
	defer t.Stop()
	for {
		o.step(ctx)
		select {
		case <-ctx.Done():
			o.stopAll()
			o.wg.Wait()
			return nil
		case <-t.C:
		case <-o.kick:
		}
	}
}

// recover puts tickets whose run died with the process back in the queue.
func (o *Orchestrator) recover(ctx context.Context) error {
	runs, err := o.st.RunsByStatus(ctx, domain.RunRunning)
	if err != nil {
		return err
	}
	paused, err := o.st.RunsByStatus(ctx, domain.RunPaused)
	if err != nil {
		return err
	}
	for _, run := range append(runs, paused...) {
		run.Status = domain.RunFailed
		run.Error = interruptedPrefix + "a restart"
		fin := o.now()
		run.FinishedAt = &fin
		if err := o.st.UpdateRun(ctx, run); err != nil {
			return err
		}
		_, err := o.update(ctx, run.TicketID, func(t *domain.Ticket) error {
			if t.Status != domain.StatusWorking {
				return errUnchanged
			}
			t.Status = domain.StatusReady
			t.StatusNote = "resumed after a restart"
			return nil
		})
		if err != nil && !errors.Is(err, errUnchanged) && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

// step runs one scheduler pass. Errors are logged; the next pass retries.
func (o *Orchestrator) step(ctx context.Context) {
	for name, f := range map[string]func(context.Context) error{
		"schedules": o.fireSchedules,
		"timers":    o.releaseTimers,
		"ci":        o.pollCI,
		"uploads":   o.cleanUploads,
	} {
		if err := f(ctx); err != nil {
			o.log.Error("scheduler step failed", "step", name, "err", err)
		}
	}
	if err := o.startReady(ctx); err != nil {
		o.log.Error("start ready tickets", "err", err)
	}
}

// releaseTimers moves scheduled backlog tickets and expired waits to ready.
func (o *Orchestrator) releaseTimers(ctx context.Context) error {
	ts, err := o.st.TicketsByStatus(ctx, domain.StatusBacklog, domain.StatusWaiting)
	if err != nil {
		return err
	}
	now := o.now()
	for _, polled := range ts {
		// Decided on the current row: the user may have changed the ticket
		// since the list was read.
		_, err := o.update(ctx, polled.ID, func(t *domain.Ticket) error {
			switch {
			case t.Status == domain.StatusBacklog && t.StartAt != nil && !t.StartAt.After(now):
				t.StartAt = nil
				t.StatusNote = "started on schedule"
			case t.Status == domain.StatusWaiting && t.Stage != domain.StageCI && t.WaitUntil != nil && !t.WaitUntil.After(now):
				t.WaitUntil = nil
				t.StatusNote = "usage limit reset"
			default:
				return errUnchanged
			}
			t.Status = domain.StatusReady
			return nil
		})
		if err != nil && !errors.Is(err, errUnchanged) {
			return err
		}
	}
	return nil
}

// startReady starts runs for ready tickets while slots are free.
func (o *Orchestrator) startReady(ctx context.Context) error {
	o.mu.Lock()
	draining := o.draining
	free := o.cfg.MaxParallelRuns - len(o.active)
	o.mu.Unlock()
	if draining {
		return nil
	}
	ready, err := o.st.TicketsByStatus(ctx, domain.StatusReady, domain.StatusReadyForTesting)
	if err != nil {
		return err
	}
	// Every ready ticket's hold is checked, slot or not, so a held card
	// says what it waits on even while all slots are busy.
	for _, t := range ready {
		if o.isActive(t.ID) {
			continue
		}
		if held, err := o.held(ctx, t); err != nil || held {
			if err != nil {
				o.log.Error("check dependencies", "ticket", t.ID, "err", err)
			}
			continue
		}
		if free <= 0 {
			continue
		}
		started, err := o.startRun(ctx, t)
		if err != nil {
			o.log.Error("start run", "ticket", t.ID, "err", err)
			msg := err.Error()
			cur, uerr := o.update(ctx, t.ID, func(c *domain.Ticket) error {
				if c.Status != t.Status {
					return errUnchanged // the user moved it meanwhile
				}
				c.Status = domain.StatusFailed
				c.StatusNote = msg
				return nil
			})
			if uerr == nil {
				o.notify(ctx, domain.NotifyRunFailed, cur, "Run failed to start: "+msg)
			}
			continue
		}
		if started {
			free--
		}
	}
	return nil
}

func (o *Orchestrator) isActive(ticketID int64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.active[ticketID]
	return ok
}

// Idle reports whether no run is active.
func (o *Orchestrator) Idle() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.active) == 0
}

// Drain stops (or resumes) starting new runs; active runs continue.
func (o *Orchestrator) Drain(on bool) {
	o.mu.Lock()
	o.draining = on
	o.mu.Unlock()
	o.Kick()
}

// Draining reports whether new runs are held back.
func (o *Orchestrator) Draining() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.draining
}

// ActiveRuns returns the number of runs in progress.
func (o *Orchestrator) ActiveRuns() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.active)
}

func (o *Orchestrator) stopAll() {
	o.mu.Lock()
	runs := make([]*activeRun, 0, len(o.active))
	for _, r := range o.active {
		runs = append(runs, r)
	}
	o.mu.Unlock()
	for _, r := range runs {
		r.mu.Lock()
		r.shutdown = true
		r.mu.Unlock()
		_ = r.sess.Stop()
	}
}

// save writes a ticket and tells the UI.
func (o *Orchestrator) save(ctx context.Context, t *domain.Ticket) error {
	if err := o.st.UpdateTicket(ctx, t); err != nil {
		return err
	}
	o.bus.Publish(Message{Type: "ticket", ProjectID: t.ProjectID, TicketID: t.ID, Data: t})
	return nil
}

// mutate applies f to the ticket's current row and saves it, so concurrent
// writers of different fields do not overwrite each other. Every write of a
// ticket row goes through mutate, update or a path holding tmu; nothing
// called with tmu held may call them.
func (o *Orchestrator) mutate(ctx context.Context, id int64, f func(*domain.Ticket)) (domain.Ticket, error) {
	return o.update(ctx, id, func(t *domain.Ticket) error { f(t); return nil })
}

// errUnchanged, returned by an update func, leaves the row as it is.
var errUnchanged = errors.New("ticket unchanged")

// update is mutate with a func that may refuse the change: when f returns
// an error the row is not saved and update returns that error.
func (o *Orchestrator) update(ctx context.Context, id int64, f func(*domain.Ticket) error) (domain.Ticket, error) {
	o.tmu.Lock()
	defer o.tmu.Unlock()
	t, err := o.st.Ticket(ctx, id)
	if err != nil {
		return t, err
	}
	if err := f(&t); err != nil {
		return t, err
	}
	return t, o.save(ctx, &t)
}

func (o *Orchestrator) notify(ctx context.Context, ev domain.NotifyEvent, t domain.Ticket, body string) {
	if o.notifier == nil {
		return
	}
	url := fmt.Sprintf("%s/#/p/%d/t/%d", o.cfg.PublicURL, t.ProjectID, t.ID)
	o.notifier.Notify(ctx, ev, t.Title, body, url)
}

// ErrBusy is returned when an action needs no active run but one is active.
var ErrBusy = errors.New("a run is active on this ticket")

// ErrNotActive is returned when an action needs an active run.
var ErrNotActive = errors.New("no active run on this ticket")
