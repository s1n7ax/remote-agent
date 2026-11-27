package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// A build run that opens its PR and ends its turn without stage_done has
// finished its stage: the PR is its outcome.
func TestBuildFinishedByItsPROpenedWithoutStageDone(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	commit(t, r.Info.Workdir, "feature.txt", "v1\n")
	if _, err := h.o.ToolOpenPR(h.ctx, r.ID(), "Add dark mode", "Implements it.", true); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{Summary: "Opened the PR."})
	h.waitState(tk.ID, domain.StatusWaiting, domain.StageCI)
	if got := h.run(r.ID()); got.Status != domain.RunSucceeded || got.Summary != "Opened the PR." {
		t.Fatalf("run = %s %q", got.Status, got.Summary)
	}
}

// A run that ends without its outcome continues the same conversation once,
// told why; a second such end fails the ticket with the agent's last
// message, and Retry continues the conversation again.
func TestUnfinishedRunContinuesOnceThenFails(t *testing.T) {
	h := newHarness(t)
	if _, err := h.st.CreateAccount(h.ctx, domain.Account{Label: "a", Priority: 1}, "token-a"); err != nil {
		t.Fatal(err)
	}
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild, Agent: domain.AgentClaude})

	r1 := h.nextRun()
	r1.Sess.Emit(agent.Event{SessionID: "sess-1"})
	r1.Sess.Finish(agent.Event{Summary: "Waiting for the reviewer."})

	r2 := h.nextRun()
	if r2.Spec.Resume != "sess-1" {
		t.Fatalf("continuation resume = %q, want sess-1", r2.Spec.Resume)
	}
	if !strings.Contains(r2.Spec.Prompt, "## Why the last run stopped") || !strings.Contains(r2.Spec.Prompt, "open_pr") {
		t.Fatalf("continuation prompt does not say what was missing:\n%s", r2.Spec.Prompt)
	}
	if got := h.run(r1.ID()); got.Status != domain.RunFailed || !strings.HasPrefix(got.Error, unfinishedPrefix) {
		t.Fatalf("first run = %s %q", got.Status, got.Error)
	}
	r2.Sess.Emit(agent.Event{SessionID: "sess-1"})
	r2.Sess.Finish(agent.Event{Summary: "Shall I continue?"})

	got := h.waitState(tk.ID, domain.StatusFailed, domain.StageBuild)
	if !strings.Contains(got.StatusNote, "twice") || !strings.Contains(got.StatusNote, "Shall I continue?") {
		t.Fatalf("note = %q", got.StatusNote)
	}
	if _, err := h.o.RetryTicket(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	r3 := h.nextRun()
	if r3.Spec.Resume != "sess-1" {
		t.Fatalf("retry resume = %q, want sess-1", r3.Spec.Resume)
	}
}

// CI that already failed one check waits until every check has finished,
// then sends the ticket to fix.
func TestCIWaitsForEveryCheckBeforeFix(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, _ := buildToPR(t, h, tk)
	h.o.Drain(true)
	h.gh.update(n, func(p *PullState) {
		p.CI = "failure"
		p.Checks = []Check{
			{Name: "lint", Status: "completed", Conclusion: "failure", ID: 1},
			{Name: "test", Status: "in_progress", ID: 2, Pending: true},
		}
	})
	time.Sleep(100 * time.Millisecond) // several CI polls
	if got := h.ticket(tk.ID); got.Status != domain.StatusWaiting || got.Stage != domain.StageCI {
		t.Fatalf("acted on CI with a check still running: %s/%s", got.Status, got.Stage)
	}
	h.gh.update(n, func(p *PullState) {
		p.Checks[1] = Check{Name: "test", Status: "completed", Conclusion: "success", ID: 2}
	})
	h.waitState(tk.ID, domain.StatusReady, domain.StageFix)
}

// Fix and verify runs carry the requirement.
func TestFixPromptCarriesTheRequirement(t *testing.T) {
	h := newHarness(t)
	h.newTicket(domain.Ticket{Stage: domain.StageFix, Requirement: "# Dark mode\n\n## Goal\n\nA dark theme."})
	r := h.nextRun()
	if !strings.Contains(r.Spec.Prompt, "## Requirement") || !strings.Contains(r.Spec.Prompt, "A dark theme.") {
		t.Fatalf("fix prompt lacks the requirement:\n%s", r.Spec.Prompt)
	}
}

// A run longer than its project's time limit is stopped, and its ticket
// fails with the reason.
func TestRunTimesOut(t *testing.T) {
	h := newHarness(t, func(_ *harness, o *Options) { o.RunTimeout = 50 * time.Millisecond })
	tk := h.newTicket(domain.Ticket{Stage: domain.StageExplore})
	r := h.nextRun()
	got := h.waitState(tk.ID, domain.StatusFailed, domain.StageExplore)
	if !r.Sess.isEnded() {
		t.Fatal("the timed-out session was not stopped")
	}
	if !strings.HasPrefix(got.StatusNote, "timed out after") {
		t.Fatalf("note = %q", got.StatusNote)
	}
}

// A project's own time limit overrides the server default.
func TestProjectRunTimeoutOverridesDefault(t *testing.T) {
	h := newHarness(t, withoutRun())
	if got := h.o.runTimeout(domain.Project{}); got != 120*time.Minute {
		t.Fatalf("default = %s", got)
	}
	if got := h.o.runTimeout(domain.Project{RunTimeoutMinutes: 300}); got != 300*time.Minute {
		t.Fatalf("project = %s", got)
	}
}

// stage_done alone does not finish a build: without its PR the run is
// continued, not failed.
func TestBuildStageDoneWithoutPRIsContinued(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "built"); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{})
	r2 := h.nextRun()
	if r2.Info.Stage != domain.StageBuild || !strings.Contains(r2.Spec.Prompt, "## Why the last run stopped") {
		t.Fatalf("not continued: stage %s\n%s", r2.Info.Stage, r2.Spec.Prompt)
	}
	if got := h.ticket(tk.ID); got.Status != domain.StatusWorking {
		t.Fatalf("status = %s", got.Status)
	}
}

// A continued fix run keeps its trigger: a verify failure stays one.
func TestContinuedFixKeepsItsTrigger(t *testing.T) {
	h := newHarness(t)
	h.newTicket(domain.Ticket{Stage: domain.StageFix, FixTrigger: fixVerifyFailed, StatusNote: "verification failed"})
	r := h.nextRun()
	r.Sess.Finish(agent.Event{})
	r2 := h.nextRun()
	if !strings.Contains(r2.Spec.Prompt, "- kind: verify_failed") {
		t.Fatalf("continued fix lost its trigger:\n%s", r2.Spec.Prompt)
	}
}

// Time paused does not count toward the run's limit: a run paused for
// longer than its limit still times out no sooner than limit plus the
// pause. Scheduling delays only make the run end later, so the bound holds
// on a slow machine.
func TestPausedTimeDoesNotCount(t *testing.T) {
	const limit, paused = time.Second, 1500 * time.Millisecond
	h := newHarness(t, func(_ *harness, o *Options) { o.RunTimeout = limit })
	h.runner.withProcess = true
	start := time.Now()
	tk := h.newTicket(domain.Ticket{Stage: domain.StageExplore})
	h.nextRun()
	h.waitState(tk.ID, domain.StatusWorking, domain.StageExplore)
	if err := h.o.Pause(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(paused)
	if err := h.o.Resume(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got := h.waitState(tk.ID, domain.StatusFailed, domain.StageExplore)
	if elapsed := time.Since(start); elapsed < limit+paused {
		t.Fatalf("timed out %s after starting; paused time counted toward the %s limit", elapsed, limit)
	}
	if !strings.HasPrefix(got.StatusNote, "timed out after") {
		t.Fatalf("note = %q", got.StatusNote)
	}
}

// Only a conversation that was cut short is continued; one that failed
// with an error starts fresh.
func TestLeftUnfinished(t *testing.T) {
	for err, want := range map[string]bool{
		unfinishedPrefix + "stage_done":                      true,
		"timed out after 120 minutes":                        true,
		"interrupted by a restart":                           true,
		"claude exited without a result: prompt is too long": false,
	} {
		if got := leftUnfinished(err); got != want {
			t.Errorf("leftUnfinished(%q) = %v, want %v", err, got, want)
		}
	}
}
