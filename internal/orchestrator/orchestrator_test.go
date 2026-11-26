package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

func requirementTree() domain.QuestionTree {
	return domain.QuestionTree{
		Title: "Requirement",
		Questions: []domain.Question{
			{ID: "scope", Kind: domain.QuestionSingle, Title: "What should change?",
				Options: []domain.Option{{ID: "ui", Label: "The UI only"}, {ID: "all", Label: "Everything"}}},
			{ID: "screens", Kind: domain.QuestionText, Title: "Which screens?",
				ShowIf: &domain.Condition{Question: "scope", AnyOf: []string{"ui"}}},
			{ID: "why", Kind: domain.QuestionMulti, Title: "Why everything?",
				Options: []domain.Option{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
				ShowIf:  &domain.Condition{Question: "scope", AnyOf: []string{"all"}}},
			{ID: "ok", Kind: domain.QuestionConfirm, Title: "Proceed?"},
		},
	}
}

// 1. explore → questions → needs_input → answers → ready(design) → design run.
func TestExploreQuestionsAnsweredAdvanceToDesign(t *testing.T) {
	h := newHarness(t)
	tk, err := h.o.CreateTicket(h.ctx, h.project.ID, NewTicket{Idea: "Add dark mode\nwith a toggle", Agent: domain.AgentFake, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Issue == 0 || tk.Title != "Add dark mode" {
		t.Fatalf("ticket = %+v, want an issue and a title from the idea", tk)
	}

	r := h.nextRun()
	if r.Info.Stage != domain.StageExplore || r.Spec.Env["REMOTE_AGENT_STAGE"] != "explore" {
		t.Fatalf("first run stage = %s / %s", r.Info.Stage, r.Spec.Env["REMOTE_AGENT_STAGE"])
	}
	if !strings.Contains(r.Spec.Prompt, "## Idea") || !strings.Contains(r.Spec.Prompt, "Add dark mode") {
		t.Fatalf("explore prompt lacks the idea:\n%s", r.Spec.Prompt)
	}

	if _, err := h.o.ToolSubmitQuestions(h.ctx, r.ID(), domain.QuestionTree{Title: "x"}, domain.PurposeRequirement); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty tree: err = %v, want ErrInvalid", err)
	}
	n, err := h.o.ToolSubmitQuestions(h.ctx, r.ID(), requirementTree(), domain.PurposeRequirement)
	if err != nil || n != 1 {
		t.Fatalf("submit questions = %d, %v", n, err)
	}
	if _, err := h.o.ToolSubmitQuestions(h.ctx, r.ID(), requirementTree(), domain.PurposeRequirement); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second submit: err = %v, want ErrInvalid", err)
	}
	if h.notes.count(domain.NotifyQuestions) != 1 {
		t.Fatal("no questions notification")
	}
	r.Sess.Finish(agent.Event{Summary: "posted round 1"})

	h.waitState(tk.ID, domain.StatusNeedsInput, domain.StageExplore)
	h.waitInactive(tk.ID)
	if got := h.run(r.ID()).Status; got != domain.RunSucceeded {
		t.Fatalf("explore run status = %s", got)
	}
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "late"); !errors.Is(err, ErrUnknownRun) {
		t.Fatalf("tool call on a finished run: err = %v, want ErrUnknownRun", err)
	}
	rd, err := h.o.OpenRound(h.ctx, tk.ID)
	if err != nil || rd == nil || rd.Stage != domain.StageDesign || rd.Purpose != domain.PurposeRequirement {
		t.Fatalf("open round = %+v, %v; want a requirement round resuming design", rd, err)
	}

	answer := func(qid string, a domain.Answer) AnswerResult {
		t.Helper()
		res, err := h.o.Answer(h.ctx, tk.ID, 1, qid, a)
		if err != nil {
			t.Fatalf("answer %s: %v", qid, err)
		}
		return res
	}
	if _, err := h.o.Answer(h.ctx, tk.ID, 1, "scope", domain.Answer{OptionIDs: []string{"nope"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown option: err = %v", err)
	}
	if res := answer("scope", domain.Answer{OptionIDs: []string{"ui"}}); res.Completed {
		t.Fatal("round complete after one of three visible answers")
	}
	if res := answer("ok", domain.Answer{OptionIDs: []string{"yes"}}); res.Completed {
		t.Fatal("round complete while the visible 'screens' question is unanswered")
	}
	h.noRun(50 * time.Millisecond)
	if got := h.ticket(tk.ID).Status; got != domain.StatusNeedsInput {
		t.Fatalf("status after partial answers = %s", got)
	}
	// The hidden "why" question is never answered.
	res := answer("screens", domain.Answer{Text: "settings"})
	if !res.Completed || res.Ticket.Status != domain.StatusReady || res.Ticket.Stage != domain.StageDesign {
		t.Fatalf("after the last visible answer: completed=%v ticket=%s/%s", res.Completed, res.Ticket.Status, res.Ticket.Stage)
	}
	if _, err := h.o.Answer(h.ctx, tk.ID, 1, "screens", domain.Answer{Text: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("answer on a completed round: err = %v", err)
	}
	if len(h.gh.commentsOn(tk.Issue)) != 1 {
		t.Fatalf("issue comments = %+v, want the round summary", h.gh.commentsOn(tk.Issue))
	}

	r2 := h.nextRun()
	if r2.Info.Stage != domain.StageDesign || r2.Info.TicketID != tk.ID {
		t.Fatalf("second run = %+v, want design on the same ticket", r2.Info)
	}
	if !strings.Contains(r2.Spec.Prompt, "## Answers") || !strings.Contains(r2.Spec.Prompt, `screens — Which screens?: "settings"`) {
		t.Fatalf("design prompt lacks the answers:\n%s", r2.Spec.Prompt)
	}
	if strings.Contains(r2.Spec.Prompt, "why —") {
		t.Fatal("design prompt lists a hidden question")
	}
	goal := goalName(h.ticket(tk.ID))
	if _, err := os.Stat(filepath.Join(r2.Info.Workdir, ".ship", goal, "rounds", "1.json")); err != nil {
		t.Fatalf("goal dir round file: %v", err)
	}
	h.waitState(tk.ID, domain.StatusWorking, domain.StageDesign)
}

// Answering the round before the run that posted it has exited must not
// leave the ticket parked in needs_input with no open round.
func TestRoundAnsweredBeforeRunEnds(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{})
	r := h.nextRun()
	tree := domain.QuestionTree{Title: "R", Questions: []domain.Question{{ID: "ok", Kind: domain.QuestionConfirm, Title: "Go?"}}}
	if _, err := h.o.ToolSubmitQuestions(h.ctx, r.ID(), tree, domain.PurposeRequirement); err != nil {
		t.Fatal(err)
	}
	res, err := h.o.Answer(h.ctx, tk.ID, 1, "ok", domain.Answer{OptionIDs: []string{"yes"}})
	if err != nil || !res.Completed {
		t.Fatalf("answer = %+v, %v", res, err)
	}
	r.Sess.Finish(agent.Event{Summary: "posted"})

	r2 := h.nextRun()
	if r2.Info.Stage != domain.StageDesign {
		t.Fatalf("next run stage = %s, want design", r2.Info.Stage)
	}
}

// 2. A usage limit moves the stage to the next account; with every account
// exhausted the ticket waits for the earliest reset.
func TestUsageLimitSwitchesAccountThenWaits(t *testing.T) {
	h := newHarness(t)
	a, err := h.st.CreateAccount(h.ctx, domain.Account{Label: "a", Priority: 1}, "token-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.st.CreateAccount(h.ctx, domain.Account{Label: "b", Priority: 2}, "token-b")
	if err != nil {
		t.Fatal(err)
	}
	tk := h.newTicket(domain.Ticket{Agent: domain.AgentClaude})

	r1 := h.nextRun()
	if got := h.run(r1.ID()).AccountID; got != a.ID {
		t.Fatalf("first run account = %d, want %d", got, a.ID)
	}
	if r1.Spec.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "token-a" {
		t.Fatal("first run does not carry account a's token")
	}
	// A transcript on account a, for the resume on account b.
	r1.Sess.Emit(agent.Event{SessionID: "sess-1"})
	transcript := filepath.Join(r1.Spec.ConfigDir, "projects", "proj", "sess-1.jsonl")
	writeFile(t, transcript, "{}\n")
	resetA := h.clock.Now().Add(2 * time.Hour).Truncate(time.Second)
	r1.Sess.Emit(agent.Event{Limit: &agent.Limit{ResetsAt: resetA, Type: "five_hour"}})

	r2 := h.nextRun()
	if !r1.Sess.isEnded() {
		t.Fatal("the limited session was not stopped")
	}
	if got := h.run(r1.ID()).Status; got != domain.RunLimited {
		t.Fatalf("limited run status = %s", got)
	}
	if got := h.run(r2.ID()).AccountID; got != b.ID {
		t.Fatalf("second run account = %d, want %d", got, b.ID)
	}
	if r2.Spec.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "token-b" || r2.Info.Stage != domain.StageExplore {
		t.Fatalf("second run token/stage = %q/%s", r2.Spec.Env["CLAUDE_CODE_OAUTH_TOKEN"], r2.Info.Stage)
	}
	if r2.Spec.Resume != "sess-1" {
		t.Fatalf("second run resume = %q, want sess-1", r2.Spec.Resume)
	}
	if _, err := os.Stat(filepath.Join(r2.Spec.ConfigDir, "projects", "proj", "sess-1.jsonl")); err != nil {
		t.Fatalf("transcript not copied to account b: %v", err)
	}
	if !strings.Contains(r2.Spec.Prompt, "resumed: account switch") {
		t.Fatalf("resume prompt lacks the reason:\n%s", r2.Spec.Prompt)
	}
	accA, _ := h.st.Account(h.ctx, a.ID)
	if accA.ExhaustedTill == nil || !accA.ExhaustedTill.Equal(resetA) {
		t.Fatalf("account a exhausted till %v, want %v", accA.ExhaustedTill, resetA)
	}

	resetB := h.clock.Now().Add(time.Hour).Truncate(time.Second)
	r2.Sess.Emit(agent.Event{Limit: &agent.Limit{ResetsAt: resetB}})
	w := h.waitTicket(tk.ID, "waiting", func(t domain.Ticket) bool { return t.Status == domain.StatusWaiting })
	if w.WaitUntil == nil || !w.WaitUntil.Equal(resetB.Add(time.Minute)) {
		t.Fatalf("wait until %v, want the earliest reset + 1m (%v)", w.WaitUntil, resetB.Add(time.Minute))
	}
	if w.Stage != domain.StageExplore {
		t.Fatalf("waiting stage = %s", w.Stage)
	}
	if h.notes.count(domain.NotifyLimited) != 1 {
		t.Fatal("no usage-limited notification")
	}
	h.noRun(50 * time.Millisecond)

	// After b resets, the ticket runs again on b (a is still exhausted).
	h.clock.Advance(time.Hour + 2*time.Minute)
	r3 := h.nextRun()
	if got := h.run(r3.ID()).AccountID; got != b.ID {
		t.Fatalf("third run account = %d, want %d", got, b.ID)
	}
}

func TestPickAccountPrefersAccountsWithoutWarning(t *testing.T) {
	h := newHarness(t)
	a, _ := h.st.CreateAccount(h.ctx, domain.Account{Label: "a", Priority: 1}, "ta")
	b, _ := h.st.CreateAccount(h.ctx, domain.Account{Label: "b", Priority: 2}, "tb")
	a.Warning = true
	if err := h.st.UpdateAccount(h.ctx, a); err != nil {
		t.Fatal(err)
	}
	acc, _, err := h.o.pickAccount(h.ctx)
	if err != nil || acc == nil || acc.ID != b.ID {
		t.Fatalf("pick = %+v, %v; want b", acc, err)
	}
	till := h.clock.Now().Add(time.Hour)
	b.ExhaustedTill = &till
	if err := h.st.UpdateAccount(h.ctx, b); err != nil {
		t.Fatal(err)
	}
	acc, _, err = h.o.pickAccount(h.ctx)
	if err != nil || acc == nil || acc.ID != a.ID {
		t.Fatalf("pick = %+v, %v; want the warned account a over none", acc, err)
	}
}

func TestClaudeTicketWithoutAccountsFails(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Agent: domain.AgentClaude})
	got := h.waitTicket(tk.ID, "failed", func(t domain.Ticket) bool { return t.Status == domain.StatusFailed })
	if !strings.Contains(got.StatusNote, "no Claude account") {
		t.Fatalf("note = %q", got.StatusNote)
	}
}

// 3. Stop sends the ticket back to the backlog.
func TestStop(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{})
	r := h.nextRun()
	if err := h.o.Stop(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got := h.waitState(tk.ID, domain.StatusBacklog, domain.StageExplore)
	if got.StatusNote != "stopped" {
		t.Fatalf("note = %q", got.StatusNote)
	}
	h.waitInactive(tk.ID)
	if st := h.run(r.ID()).Status; st != domain.RunStopped {
		t.Fatalf("run status = %s", st)
	}
	if toks := h.revokedTokens(); len(toks) != 1 || toks[0] != r.Spec.Env["REMOTE_AGENT_MCP_TOKEN"] {
		t.Fatalf("revoked = %v, want the run's token %q", toks, r.Spec.Env["REMOTE_AGENT_MCP_TOKEN"])
	}
	if err := h.o.Stop(h.ctx, tk.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second stop: err = %v", err)
	}
	h.noRun(50 * time.Millisecond)
}

func procState(t *testing.T, pid int) byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	return strings.Fields(s[strings.LastIndexByte(s, ')')+1:])[0][0]
}

// 3. Pause freezes the agent's process group; resume continues it; stop on
// a paused run still ends it.
func TestPauseResume(t *testing.T) {
	h := newHarness(t)
	h.runner.withProcess = true
	if err := h.o.Pause(h.ctx, 999); !errors.Is(err, ErrNotActive) {
		t.Fatalf("pause without a run: err = %v", err)
	}
	tk := h.newTicket(domain.Ticket{})
	r := h.nextRun()
	pid := r.Sess.PID()

	if err := h.o.Pause(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "process stopped", func() bool { return procState(t, pid) == 'T' })
	if st := h.run(r.ID()).Status; st != domain.RunPaused {
		t.Fatalf("run status after pause = %s", st)
	}
	if h.notes.count(domain.NotifyPaused) != 1 {
		t.Fatal("no paused notification")
	}
	if got := h.ticket(tk.ID).Status; got != domain.StatusWorking {
		t.Fatalf("ticket status while paused = %s", got)
	}

	if err := h.o.Resume(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "process continued", func() bool { return procState(t, pid) != 'T' })
	if st := h.run(r.ID()).Status; st != domain.RunRunning {
		t.Fatalf("run status after resume = %s", st)
	}

	if err := h.o.Pause(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "process stopped again", func() bool { return procState(t, pid) == 'T' })
	if err := h.o.Stop(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	h.waitState(tk.ID, domain.StatusBacklog, domain.StageExplore)
	h.waitInactive(tk.ID)
	if st := h.run(r.ID()).Status; st != domain.RunStopped {
		t.Fatalf("run status = %s", st)
	}
}

// buildToPR runs a build stage that commits, opens a draft PR and finishes.
func buildToPR(t *testing.T, h *harness, tk domain.Ticket) (int, *testRun) {
	t.Helper()
	r := h.nextRun()
	if r.Info.Stage != domain.StageBuild {
		t.Fatalf("run stage = %s, want build", r.Info.Stage)
	}
	branch := mustGit(t, r.Info.Workdir, "symbolic-ref", "--short", "HEAD")
	if !strings.HasPrefix(branch, "ra/") {
		t.Fatalf("workdir branch = %q", branch)
	}
	commit(t, r.Info.Workdir, "feature.txt", "v1\n")
	pr, err := h.o.ToolOpenPR(h.ctx, r.ID(), "Add dark mode", "Implements it.", true)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Draft || pr.Branch != branch {
		t.Fatalf("PR = %+v", pr)
	}
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "built"); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{Summary: "built"})
	got := h.waitState(tk.ID, domain.StatusWaiting, domain.StageCI)
	if got.PR != pr.Number || got.Branch != branch {
		t.Fatalf("ticket PR/branch = %d/%s, want %d/%s", got.PR, got.Branch, pr.Number, branch)
	}
	return pr.Number, r
}

// 4. build opens a PR → CI green → verify → PASS → PR ready → final green
// CI on the verified head → in_review.
func TestBuildCIVerifyToReview(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild, Issue: 41})
	n, _ := buildToPR(t, h, tk)
	if body := h.gh.pullBody(n); !strings.Contains(body, "Closes #41") {
		t.Fatalf("PR body %q does not close the issue", body)
	}

	// Pending CI keeps the ticket waiting.
	h.noRun(50 * time.Millisecond)
	if got := h.ticket(tk.ID); got.Status != domain.StatusWaiting || got.Stage != domain.StageCI {
		t.Fatalf("with pending CI: %s/%s", got.Status, got.Stage)
	}

	h.gh.setCI(n, "success")
	r := h.nextRun()
	if r.Info.Stage != domain.StageVerify {
		t.Fatalf("run after green CI = %s, want verify", r.Info.Stage)
	}
	if v := h.ticket(tk.ID).VerifiedSHA; v != "" {
		t.Fatalf("verified sha before verify = %q", v)
	}
	writeFile(t, filepath.Join(r.Info.Workdir, "shot.png"), "\x89PNG\r\n\x1a\n")
	if err := h.o.ToolSubmitProof(h.ctx, r.ID(), "fail?", "x", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict: err = %v", err)
	}
	if err := h.o.ToolSubmitProof(h.ctx, r.ID(), "pass", "all good", []string{"shot.png"}); err != nil {
		t.Fatal(err)
	}
	if len(h.gh.commentsOn(n)) != 1 {
		t.Fatal("proof not posted on the PR")
	}
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "verified"); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{})

	got := h.waitState(tk.ID, domain.StatusInReview, domain.StageCI)
	pr, _ := h.gh.Pull(h.ctx, "", n)
	if pr.Draft {
		t.Fatal("PR still a draft in review")
	}
	if got.VerifiedSHA != pr.HeadSHA {
		t.Fatalf("verified sha %q, head %q", got.VerifiedSHA, pr.HeadSHA)
	}
	if h.notes.count(domain.NotifyPRReady) != 1 {
		t.Fatal("no pr_ready notification")
	}
	if m := h.gh.mergeCalls(); len(m) != 0 {
		t.Fatalf("something merged without the user: %v", m)
	}
	h.noRun(50 * time.Millisecond)
	if got := h.ticket(tk.ID).Status; got != domain.StatusInReview {
		t.Fatalf("in_review ticket moved to %s", got)
	}
}

// 4. Green CI on a head other than the verified one sends the ticket back to
// verify instead of review.
func TestGreenCIOnUnverifiedHeadReverifies(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	h.o.Drain(false)
	n, _ := buildToPR(t, h, tk)
	h.o.Drain(true)
	cur := h.ticket(tk.ID)
	cur.VerifiedSHA = "0000000000000000000000000000000000000000"
	if err := h.st.UpdateTicket(h.ctx, &cur); err != nil {
		t.Fatal(err)
	}
	h.gh.update(n, func(p *PullState) { p.Draft = false })
	h.gh.setCI(n, "success")
	got := h.waitState(tk.ID, domain.StatusReadyForTesting, domain.StageVerify)
	if got.StatusNote != "CI is green; ready for testing" {
		t.Fatalf("note = %q", got.StatusNote)
	}
}

// 4. Red CI → fix, at most 2 fix rounds, then failed.
func TestRedCIFixRoundsThenFailed(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	n, _ := buildToPR(t, h, tk)

	for round := 1; round <= maxFixRounds; round++ {
		h.gh.setCI(n, "failure")
		r := h.nextRun()
		if r.Info.Stage != domain.StageFix {
			t.Fatalf("round %d: run stage = %s, want fix", round, r.Info.Stage)
		}
		tc := h.ticket(tk.ID)
		if tc.FixRounds != round {
			t.Fatalf("fix rounds = %d, want %d", tc.FixRounds, round)
		}
		for _, want := range []string{"kind: ci_failed", "failing checks: test (failure)", "fix round: " + strconv.Itoa(round) + " of 2"} {
			if !strings.Contains(r.Spec.Prompt, want) {
				t.Fatalf("fix prompt lacks %q:\n%s", want, r.Spec.Prompt)
			}
		}
		logs, err := h.o.ToolCILogs(h.ctx, r.ID(), "", 0)
		if err != nil || !strings.Contains(logs, "log of test") {
			t.Fatalf("ci logs = %q, %v", logs, err)
		}
		commit(t, r.Info.Workdir, "feature.txt", "fix "+strconv.Itoa(round)+"\n")
		if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); err != nil {
			t.Fatal(err)
		}
		if err := h.o.ToolStageDone(h.ctx, r.ID(), "fixed"); err != nil {
			t.Fatal(err)
		}
		r.Sess.Finish(agent.Event{})
		h.waitState(tk.ID, domain.StatusWaiting, domain.StageCI)
	}

	h.gh.setCI(n, "failure")
	got := h.waitTicket(tk.ID, "failed", func(t domain.Ticket) bool { return t.Status == domain.StatusFailed })
	if got.StatusNote != "CI failed after 2 fix rounds" {
		t.Fatalf("note = %q", got.StatusNote)
	}
	if h.notes.count(domain.NotifyRunFailed) != 1 {
		t.Fatal("no run_failed notification")
	}
	h.noRun(50 * time.Millisecond)
}

func TestBuildWithoutPRFails(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild})
	r := h.nextRun()
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "done?"); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{})
	got := h.waitTicket(tk.ID, "failed", func(t domain.Ticket) bool { return t.Status == domain.StatusFailed })
	if !strings.Contains(got.StatusNote, "without opening a pull request") {
		t.Fatalf("note = %q", got.StatusNote)
	}
}

// 5. No Tool* method merges; only Orchestrator.MergeAt does.
func TestOnlyUserMerges(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Stage: domain.StageBuild, Issue: 41})
	r := h.nextRun()
	id := r.ID()
	ctx := h.ctx
	commit(t, r.Info.Workdir, "feature.txt", "v1\n")
	writeFile(t, filepath.Join(r.Info.Workdir, "shot.png"), "png")

	// Every app tool, called the way an agent could. Errors are fine; a merge
	// is not.
	calls := map[string]func() error{
		"ToolTicket":            func() error { _, err := h.o.ToolTicket(ctx, id); return err },
		"ToolSubmitQuestions":   func() error { _, err := h.o.ToolSubmitQuestions(ctx, id, domain.QuestionTree{}, ""); return err },
		"ToolSubmitRequirement": func() error { return h.o.ToolSubmitRequirement(ctx, id, "# Dark mode\n\nBody") },
		"ToolSubmitDecisions":   func() error { return h.o.ToolSubmitDecisions(ctx, id, "decisions") },
		"ToolSubmitPrototypes": func() error {
			return h.o.ToolSubmitPrototypes(ctx, id, []domain.Prototype{{Name: "a", Branch: "pa"}, {Name: "b", Branch: "pb"}})
		},
		"ToolUploadArtifact": func() error { _, err := h.o.ToolUploadArtifact(ctx, id, "shot.png"); return err },
		"ToolOpenPR":         func() error { _, err := h.o.ToolOpenPR(ctx, id, "Dark mode", "body", true); return err },
		"ToolPushBranch":     func() error { _, err := h.o.ToolPushBranch(ctx, id); return err },
		"ToolUpdatePR":       func() error { return h.o.ToolUpdatePR(ctx, id, "new body") },
		"ToolCIStatus":       func() error { _, err := h.o.ToolCIStatus(ctx, id, 0); return err },
		"ToolCILogs":         func() error { _, err := h.o.ToolCILogs(ctx, id, "", 0); return err },
		"ToolListPulls":      func() error { _, err := h.o.ToolListPulls(ctx, id); return err },
		"ToolComment":        func() error { return h.o.ToolComment(ctx, id, "hi", 0) },
		"ToolSubmitProof":    func() error { return h.o.ToolSubmitProof(ctx, id, "PASS", "ok", []string{"shot.png"}) },
		"ToolStartPreview":   func() error { _, err := h.o.ToolStartPreview(ctx, id, "", "true", 65000); return err },
		"ToolRepo":           func() error { _, err := h.o.ToolRepo(id); return err },
		"ToolStageDone":      func() error { return h.o.ToolStageDone(ctx, id, "built") },
	}
	order := []string{"ToolTicket", "ToolSubmitQuestions", "ToolSubmitRequirement", "ToolSubmitDecisions", "ToolSubmitPrototypes",
		"ToolUploadArtifact", "ToolOpenPR", "ToolPushBranch", "ToolUpdatePR", "ToolCIStatus", "ToolCILogs", "ToolListPulls",
		"ToolComment", "ToolSubmitProof", "ToolStartPreview", "ToolRepo", "ToolStageDone"}
	typ := reflect.TypeOf(h.o)
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Tool") {
			continue
		}
		if strings.Contains(strings.ToLower(name), "merge") {
			t.Errorf("tool method %s looks like a merge", name)
		}
		if calls[name] == nil {
			t.Errorf("tool method %s is not exercised by this test; add it", name)
		}
	}
	for _, name := range order {
		_ = calls[name]()
		if m := h.gh.mergeCalls(); len(m) != 0 {
			t.Fatalf("%s merged a PR: %v", name, m)
		}
	}
	h.o.StopPreview()
	r.Sess.Finish(agent.Event{})
	got := h.waitState(tk.ID, domain.StatusWaiting, domain.StageCI)
	if got.Title != "Dark mode" || got.Requirement == "" {
		t.Fatalf("requirement tool did not apply: %+v", got)
	}
	h.o.Drain(true)
	h.gh.setCI(got.PR, "success") // nothing in the flow merges on green either
	h.waitState(tk.ID, domain.StatusReadyForTesting, domain.StageVerify)
	h.o.Drain(false)
	h.nextRun()
	if err := h.o.Stop(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	h.waitState(tk.ID, domain.StatusBacklog, domain.StageVerify)
	h.waitInactive(tk.ID)
	if m := h.gh.mergeCalls(); len(m) != 0 {
		t.Fatalf("merged without the user: %v", m)
	}

	// The user merges: the ticket ends done/merged and its worktree goes.
	cur := h.ticket(tk.ID)
	cur.Status, cur.Stage = domain.StatusInReview, domain.StageCI
	head, _ := h.gh.Pull(ctx, "", cur.PR)
	cur.VerifiedSHA = head.HeadSHA // Merge takes only a verified head
	if err := h.st.UpdateTicket(ctx, &cur); err != nil {
		t.Fatal(err)
	}
	if err := h.o.MergeAt(ctx, h.project.ID, cur.PR, "squash", head.HeadSHA); err != nil {
		t.Fatal(err)
	}
	if m := h.gh.mergeCalls(); len(m) != 1 || m[0] != strconv.Itoa(cur.PR)+":squash" {
		t.Fatalf("merges = %v", m)
	}
	done := h.waitState(tk.ID, domain.StatusDone, domain.StageMerged)
	if done.StatusNote != "merged" {
		t.Fatalf("note = %q", done.StatusNote)
	}
	if _, err := os.Stat(r.Info.Workdir); !os.IsNotExist(err) {
		t.Fatalf("worktree still there after merge: %v", err)
	}
	if !strings.Contains(systemPrompt, "no way to merge") {
		t.Fatal("system prompt no longer tells agents they cannot merge")
	}
}

// 6. A spend cap stops a pi run and asks; raise continues, stop parks it.
func TestSpendCapRound(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket(domain.Ticket{Agent: domain.AgentPi, SpendCapUSD: 1})
	r := h.nextRun()
	if r.Spec.Env["OPENROUTER_API_KEY"] != "or-key" {
		t.Fatal("pi run lacks the OpenRouter key")
	}
	r.Sess.Emit(agent.Event{Cost: 0.4})
	r.Sess.Emit(agent.Event{Cost: 0.7})
	got := h.waitState(tk.ID, domain.StatusNeedsInput, domain.StageExplore)
	if !strings.Contains(got.StatusNote, "$1.00 spend cap") {
		t.Fatalf("note = %q", got.StatusNote)
	}
	if got.SpendUSD < 1.09 || got.SpendUSD > 1.11 {
		t.Fatalf("ticket spend = %v", got.SpendUSD)
	}
	h.waitInactive(tk.ID)
	if !r.Sess.isEnded() || r.Sess.interruptCount() != 1 {
		t.Fatalf("capped session ended=%v interrupts=%d", r.Sess.isEnded(), r.Sess.interruptCount())
	}
	run := h.run(r.ID())
	if run.Status != domain.RunCapped || run.CostUSD < 1.09 {
		t.Fatalf("run = %s cost %v", run.Status, run.CostUSD)
	}
	if spent, _ := h.st.Spend(h.ctx, h.o.today()); spent < 1.09 || spent > 1.11 {
		t.Fatalf("daily spend = %v", spent)
	}
	if h.notes.count(domain.NotifyPaused) != 1 {
		t.Fatal("no notification for the cap")
	}
	rd, _ := h.o.OpenRound(h.ctx, tk.ID)
	if rd == nil || rd.Purpose != domain.PurposeSpendCap || rd.Stage != domain.StageExplore {
		t.Fatalf("open round = %+v", rd)
	}
	if _, err := h.o.Answer(h.ctx, tk.ID, rd.N, "cap", domain.Answer{Text: "whatever"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("free text on the cap question: err = %v", err)
	}
	res, err := h.o.Answer(h.ctx, tk.ID, rd.N, "cap", domain.Answer{OptionIDs: []string{"raise"}})
	if err != nil || !res.Completed {
		t.Fatalf("raise = %+v, %v", res, err)
	}
	if res.Ticket.SpendCapUSD != 6 {
		t.Fatalf("raised cap = %v, want 6", res.Ticket.SpendCapUSD)
	}

	r2 := h.nextRun()
	if r2.Info.Stage != domain.StageExplore {
		t.Fatalf("resumed stage = %s", r2.Info.Stage)
	}
	if !strings.Contains(r2.Spec.Prompt, "resumed: user resume") {
		t.Fatalf("prompt lacks the resume reason:\n%s", r2.Spec.Prompt)
	}
	r2.Sess.Emit(agent.Event{Cost: 5})
	h.waitState(tk.ID, domain.StatusNeedsInput, domain.StageExplore)
	h.waitInactive(tk.ID)
	rd, _ = h.o.OpenRound(h.ctx, tk.ID)
	if _, err := h.o.Answer(h.ctx, tk.ID, rd.N, "cap", domain.Answer{OptionIDs: []string{"stop"}}); err != nil {
		t.Fatal(err)
	}
	got = h.waitState(tk.ID, domain.StatusBacklog, domain.StageExplore)
	if got.StatusNote != "stopped at the spend cap" || got.SpendCapUSD != 6 {
		t.Fatalf("after stop: note %q cap %v", got.StatusNote, got.SpendCapUSD)
	}
	h.noRun(50 * time.Millisecond)
}

// 6. The daily cap is checked before a pi run starts; raise lifts today's cap.
func TestDailySpendCapBeforeStart(t *testing.T) {
	h := newHarness(t)
	h.o.Drain(true)
	if err := h.st.SetSetting(h.ctx, SettingDailyCap, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.AddSpend(h.ctx, h.o.today(), 1.5); err != nil {
		t.Fatal(err)
	}
	tk := h.newTicket(domain.Ticket{Agent: domain.AgentPi})
	h.o.Drain(false)
	got := h.waitState(tk.ID, domain.StatusNeedsInput, domain.StageExplore)
	if !strings.Contains(got.StatusNote, "daily cap") {
		t.Fatalf("note = %q", got.StatusNote)
	}
	h.noRun(50 * time.Millisecond)
	rd, _ := h.o.OpenRound(h.ctx, tk.ID)
	if _, err := h.o.Answer(h.ctx, tk.ID, rd.N, "cap", domain.Answer{OptionIDs: []string{"raise"}}); err != nil {
		t.Fatal(err)
	}
	if c := h.o.dailyCap(h.ctx); c != 6 {
		t.Fatalf("daily cap after raise = %v, want 6", c)
	}
	h.nextRun()
}

// 7. A due schedule creates a job ticket that runs once in the project clone
// and ends done.
func TestScheduleFiresJob(t *testing.T) {
	h := newHarness(t)
	sc, err := h.st.CreateSchedule(h.ctx, domain.Schedule{ProjectID: h.project.ID, Name: "Triage", Cron: "*/5 * * * *",
		Prompt: "Look at open PRs", Agent: domain.AgentFake, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	off, err := h.st.CreateSchedule(h.ctx, domain.Schedule{ProjectID: h.project.ID, Name: "Off", Cron: "* * * * *",
		Prompt: "never", Agent: domain.AgentFake})
	if err != nil {
		t.Fatal(err)
	}
	var next time.Time
	eventually(t, "next run time", func() bool {
		s, _ := h.st.Schedule(h.ctx, sc.ID)
		if s.NextRunAt != nil {
			next = *s.NextRunAt
		}
		return s.NextRunAt != nil
	})
	h.noRun(50 * time.Millisecond)

	h.clock.Advance(6 * time.Minute)
	r := h.nextRun()
	if r.Info.Stage != domain.StageJob {
		t.Fatalf("job run stage = %s", r.Info.Stage)
	}
	job := h.ticket(r.Info.TicketID)
	if job.Kind != domain.KindJob || job.JobPrompt != "Look at open PRs" || !strings.HasPrefix(job.Title, "Triage — ") {
		t.Fatalf("job ticket = %+v", job)
	}
	// Jobs get their own clone of the default branch, never repo/ itself.
	if r.Info.Workdir != h.o.workDir(job) {
		t.Fatalf("job workdir = %s, want its own clone %s", r.Info.Workdir, h.o.workDir(job))
	}
	if b := mustGit(t, r.Info.Workdir, "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Fatalf("job clone branch = %q, want main", b)
	}
	if u := mustGit(t, r.Info.Workdir, "config", "remote.origin.url"); u != "https://github.com/acme/demo.git" {
		t.Fatalf("job clone origin = %q, want the credential-free GitHub URL", u)
	}
	if !strings.Contains(r.Spec.Prompt, "## Job") || !strings.Contains(r.Spec.Prompt, "Look at open PRs") {
		t.Fatalf("job prompt:\n%s", r.Spec.Prompt)
	}
	if _, err := h.o.ToolPushBranch(h.ctx, r.ID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("job pushing the default branch: err = %v", err)
	}
	if err := h.o.ToolStageDone(h.ctx, r.ID(), "triaged"); err != nil {
		t.Fatal(err)
	}
	r.Sess.Finish(agent.Event{})
	h.waitState(job.ID, domain.StatusDone, domain.StageJob)
	eventually(t, "job clone removed", func() bool {
		_, err := os.Stat(r.Info.Workdir)
		return os.IsNotExist(err)
	})
	if h.notes.count(domain.NotifyJobDone) != 1 {
		t.Fatal("no job_done notification")
	}

	s, _ := h.st.Schedule(h.ctx, sc.ID)
	if s.LastRunAt == nil || s.NextRunAt == nil || !s.NextRunAt.After(next) {
		t.Fatalf("schedule after firing: last=%v next=%v (was %v)", s.LastRunAt, s.NextRunAt, next)
	}
	// It fires once per due time, and the disabled schedule never fires.
	h.noRun(100 * time.Millisecond)
	ts, _ := h.st.ProjectTickets(h.ctx, h.project.ID)
	if len(ts) != 1 {
		t.Fatalf("tickets = %d, want 1 job", len(ts))
	}
	if o, _ := h.st.Schedule(h.ctx, off.ID); o.NextRunAt != nil || o.LastRunAt != nil {
		t.Fatalf("disabled schedule touched: %+v", o)
	}
}

// 8. upload_artifact accepts files in the run's clone and home and rejects
// symlinks (and paths) that resolve outside them: other parts of the project
// dir included, absolute symlinks included (os.Root semantics).
func TestUploadArtifactRejectsEscapes(t *testing.T) {
	h := newHarness(t)
	h.newTicket(domain.Ticket{})
	r := h.nextRun()
	wd := r.Info.Workdir
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "secret")
	// A sibling project whose id shares the prefix ("1" vs "10").
	sibling := filepath.Join(h.o.projectDir(h.project.ID)+"0", "x.txt")
	writeFile(t, sibling, "other project")

	writeFile(t, filepath.Join(wd, "shot.png"), "\x89PNG\r\n\x1a\n")
	if err := os.Symlink(outside, filepath.Join(wd, "leak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(wd, "sib")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(wd, "shot.png"), filepath.Join(wd, "absinside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shot.png", filepath.Join(wd, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(wd, "dirleak")); err != nil {
		t.Fatal(err)
	}
	// A relative symlinked component climbing out of the clone into the
	// rest of the project dir, and into the data dir.
	if err := os.Symlink("../..", filepath.Join(wd, "up")); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(h.cfg.DataDir, "remote-agent.db")
	writeFile(t, db, "secret db")
	if err := syscall.Mkfifo(filepath.Join(wd, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	repoFile := filepath.Join(h.o.repoDir(h.project.ID), "README.md")

	for _, p := range []string{
		"leak", "sib", "dirleak/secret.txt", outside, "../../../../" + filepath.Base(h.cfg.DataDir), filepath.Join(h.cfg.DataDir, "..", "db.sqlite"),
		"absinside", repoFile, "../../repo/README.md", "up/repo/README.md", "up/../../remote-agent.db", db, "fifo",
	} {
		id, err := h.o.ToolUploadArtifact(h.ctx, r.ID(), p)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("upload %s: id=%q err=%v, want ErrInvalid", p, id, err)
		}
	}
	if err := h.o.ToolSubmitProof(h.ctx, r.ID(), "PASS", "ok", []string{"leak"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("proof with a leaking file: err = %v", err)
	}
	if _, ok, _ := h.st.LatestProof(h.ctx, r.Info.TicketID); ok {
		t.Fatal("proof stored despite the rejected file")
	}
	if _, err := h.o.ToolUploadArtifact(h.ctx, r.ID(), "."); !errors.Is(err, ErrInvalid) {
		t.Fatalf("directory upload: err = %v", err)
	}

	writeFile(t, filepath.Join(h.o.homeDir(domain.Ticket{ID: r.Info.TicketID, ProjectID: h.project.ID}), "shots", "home.png"), "\x89PNG\r\n\x1a\n")
	for _, p := range []string{"shot.png", "inside", filepath.Join(wd, "shot.png"), filepath.Join(h.o.homeDir(domain.Ticket{ID: r.Info.TicketID, ProjectID: h.project.ID}), "shots", "home.png")} {
		id, err := h.o.ToolUploadArtifact(h.ctx, r.ID(), p)
		if err != nil {
			t.Fatalf("upload %s: %v", p, err)
		}
		b, err := os.ReadFile(h.o.ArtifactPath(id))
		if err != nil || string(b) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("stored artifact %s = %q, %v", id, b, err)
		}
		a, err := h.st.Artifact(h.ctx, id)
		if err != nil || a.MIME != "image/png" || a.RunID != r.ID() {
			t.Fatalf("artifact record = %+v, %v", a, err)
		}
	}
	_ = r.Sess.Stop()
}
