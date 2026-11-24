package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/questions"
)

// The methods in this file back the app MCP tools. Each is scoped to the run
// that owns the bearer token, so an agent can only touch its own ticket.

// ErrUnknownToken is returned for a token no active run holds.
var ErrUnknownToken = errors.New("unknown or expired run token")

func (o *Orchestrator) tokenRun(token string) (*activeRun, error) {
	ar := o.runForToken(token)
	if ar == nil {
		return nil, ErrUnknownToken
	}
	return ar, nil
}

// nextStage is the stage a round answered during stage s resumes.
func nextStage(s domain.Stage) domain.Stage {
	switch s {
	case domain.StageExplore:
		return domain.StageDesign
	case domain.StageDesign:
		return domain.StageBuild
	}
	return s
}

// TicketContext is what a run learns about its ticket.
type TicketContext struct {
	Ticket     domain.Ticket      `json:"ticket"`
	Project    domain.Project     `json:"project"`
	Rounds     []domain.Round     `json:"rounds"`
	Prototypes []domain.Prototype `json:"prototypes"`
	Proof      *domain.Proof      `json:"proof,omitempty"`
}

// ToolTicket returns the run's ticket, its rounds (with answers) and proof.
func (o *Orchestrator) ToolTicket(ctx context.Context, token string) (TicketContext, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return TicketContext{}, err
	}
	return o.ticketContext(ctx, ar.ticket.ID)
}

func (o *Orchestrator) ticketContext(ctx context.Context, id int64) (TicketContext, error) {
	var tc TicketContext
	t, err := o.st.Ticket(ctx, id)
	if err != nil {
		return tc, err
	}
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		return tc, err
	}
	rds, err := o.st.Rounds(ctx, id)
	if err != nil {
		return tc, err
	}
	ps, err := o.st.Prototypes(ctx, id)
	if err != nil {
		return tc, err
	}
	tc = TicketContext{Ticket: t, Project: p, Rounds: rds, Prototypes: ps}
	if proof, ok, err := o.st.LatestProof(ctx, id); err == nil && ok {
		tc.Proof = &proof
	}
	return tc, nil
}

// ToolSubmitQuestions posts a question round on the run's ticket.
func (o *Orchestrator) ToolSubmitQuestions(ctx context.Context, token string, tree domain.QuestionTree, purpose domain.Purpose) (int, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return 0, err
	}
	if err := questions.Validate(tree); err != nil {
		return 0, invalid("%v", err)
	}
	if purpose == "" {
		switch ar.ticket.Stage {
		case domain.StageExplore:
			purpose = domain.PurposeRequirement
		case domain.StageDesign:
			purpose = domain.PurposeDesign
		default:
			purpose = domain.PurposeFollowUp
		}
	}
	stage := ar.ticket.Stage
	if purpose == domain.PurposeRequirement || purpose == domain.PurposeDesign {
		stage = nextStage(stage)
	}
	rd, err := o.st.AddRound(ctx, domain.Round{TicketID: ar.ticket.ID, Purpose: purpose, Stage: stage, Tree: tree})
	if err != nil {
		return 0, err
	}
	ar.mu.Lock()
	ar.questions = true
	ar.mu.Unlock()
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return 0, err
	}
	o.bus.Publish(Message{Type: "ticket", ProjectID: t.ProjectID, TicketID: t.ID, Data: t})
	o.event(ctx, ar, domain.EventSystem, fmt.Sprintf("posted question round %d (%d questions)", rd.N, len(tree.Questions)))
	o.notify(ctx, domain.NotifyQuestions, t, fmt.Sprintf("%d questions are waiting for you", len(tree.Questions)))
	return rd.N, nil
}

// ToolSubmitRequirement stores the requirement and mirrors it on the issue.
func (o *Orchestrator) ToolSubmitRequirement(ctx context.Context, token, markdown string) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	if strings.TrimSpace(markdown) == "" {
		return invalid("the requirement is empty")
	}
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return err
	}
	t.Requirement = markdown
	if title := requirementTitle(markdown); title != "" {
		t.Title = title
	}
	if err := o.save(ctx, &t); err != nil {
		return err
	}
	if t.Issue != 0 {
		body := markdown + fmt.Sprintf("\n\n---\n_Original idea:_\n\n%s\n\n_Tracked by remote-agent (ticket %d)._", quote(t.Idea), t.ID)
		if err := o.gh.UpdateIssueBody(ctx, ar.project.Repo, t.Issue, body); err != nil {
			o.log.Warn("mirror requirement to issue", "err", err)
		}
	}
	return nil
}

func requirementTitle(md string) string {
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "# ") {
			return titleFrom(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func quote(s string) string {
	return "> " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n> ")
}

// ToolSubmitDecisions stores the decisions record.
func (o *Orchestrator) ToolSubmitDecisions(ctx context.Context, token, markdown string) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return err
	}
	t.Decisions = markdown
	return o.save(ctx, &t)
}

// ToolSubmitPrototypes stores the prototypes the user will pick from.
func (o *Orchestrator) ToolSubmitPrototypes(ctx context.Context, token string, ps []domain.Prototype) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	if len(ps) < 2 || len(ps) > 3 {
		return invalid("submit 2 or 3 prototypes, got %d", len(ps))
	}
	for _, p := range ps {
		if p.Name == "" || p.Branch == "" {
			return invalid("every prototype needs a name and a branch")
		}
	}
	if err := o.st.SetPrototypes(ctx, ar.ticket.ID, ps); err != nil {
		return err
	}
	o.event(ctx, ar, domain.EventSystem, fmt.Sprintf("submitted %d prototypes", len(ps)))
	return nil
}

// ToolUploadArtifact stores a file from the run's project directory and
// returns its artifact id.
func (o *Orchestrator) ToolUploadArtifact(ctx context.Context, token, path string) (string, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return "", err
	}
	return o.storeArtifact(ctx, ar, path)
}

// storeArtifact copies a file into the artifact store. The path must resolve
// (after symlinks) inside the project's directory: the app runs outside the
// sandbox, so a symlink must not let a run read the app's own files.
func (o *Orchestrator) storeArtifact(ctx context.Context, ar *activeRun, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(ar.workdir, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", invalid("artifact %s: %v", path, err)
	}
	root, err := filepath.EvalSymlinks(o.projectDir(ar.project.ID))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, real); err != nil || strings.HasPrefix(rel, "..") {
		return "", invalid("artifact %s is outside the project directory", path)
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", invalid("artifact %s is not a regular file", path)
	}
	if info.Size() > 20<<20 {
		return "", invalid("artifact %s is larger than 20 MB", path)
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	dst := o.cfg.Path("artifacts", id)
	if err := copyFile(real, dst); err != nil {
		return "", fmt.Errorf("store artifact: %w", err)
	}
	mt := mime.TypeByExtension(filepath.Ext(real))
	if mt == "" {
		f, err := os.Open(real)
		if err == nil {
			head := make([]byte, 512)
			n, _ := io.ReadFull(f, head)
			f.Close()
			mt = http.DetectContentType(head[:n])
		}
	}
	_, err = o.st.AddArtifact(ctx, domain.Artifact{ID: id, TicketID: ar.ticket.ID, RunID: ar.run.ID, Name: filepath.Base(real), MIME: mt, Size: info.Size()})
	return id, err
}

// ArtifactPath returns where an artifact's bytes live.
func (o *Orchestrator) ArtifactPath(id string) string { return o.cfg.Path("artifacts", filepath.Base(id)) }

// ToolSubmitProof stores the verify stage's evidence and posts it on the PR.
func (o *Orchestrator) ToolSubmitProof(ctx context.Context, token, verdict, report string, files []string) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	verdict = strings.ToUpper(strings.TrimSpace(verdict))
	if verdict != "PASS" && verdict != "FAIL" {
		return invalid("verdict must be PASS or FAIL")
	}
	ids := []string{}
	for _, f := range files {
		id, err := o.storeArtifact(ctx, ar, f)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	proof, err := o.st.AddProof(ctx, domain.Proof{TicketID: ar.ticket.ID, RunID: ar.run.ID, Verdict: verdict, Report: report, Files: ids})
	if err != nil {
		return err
	}
	ar.mu.Lock()
	ar.proof = &proof
	ar.mu.Unlock()
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err == nil && t.PR != 0 {
		const marker = "<!-- remote-agent:proof -->"
		body := fmt.Sprintf("%s\n## Verification: %s\n\n%s\n\n_%d screenshot(s) attached to the ticket in remote-agent._", marker, verdict, report, len(ids))
		if err := o.gh.UpsertComment(ctx, ar.project.Repo, t.PR, marker, body); err != nil {
			o.log.Warn("post proof on PR", "err", err)
		}
	}
	o.bus.Publish(Message{Type: "ticket", ProjectID: ar.ticket.ProjectID, TicketID: ar.ticket.ID, Data: t})
	return nil
}

// currentBranch returns the branch checked out in dir.
func currentBranch(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ToolPushBranch pushes the branch checked out in the run's workdir. The
// GitHub layer refuses the default branch.
func (o *Orchestrator) ToolPushBranch(ctx context.Context, token string) (string, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return "", err
	}
	branch, err := currentBranch(ctx, ar.workdir)
	if err != nil {
		return "", err
	}
	if branch == ar.project.DefaultBranch {
		return "", invalid("refusing to push the default branch %q", branch)
	}
	if err := o.gh.Push(ctx, ar.project.Repo, ar.workdir, branch); err != nil {
		return "", err
	}
	o.event(ctx, ar, domain.EventSystem, "pushed "+branch)
	if ar.ticket.PR != 0 {
		o.RecheckCI(ar.ticket.ID)
	}
	return branch, nil
}

// ToolOpenPR opens the ticket's PR (pushing first), or updates it when one
// exists. The body always closes the ticket's issue.
func (o *Orchestrator) ToolOpenPR(ctx context.Context, token, title, body string, draft bool) (PullState, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return PullState{}, err
	}
	branch, err := o.ToolPushBranch(ctx, token)
	if err != nil {
		return PullState{}, err
	}
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return PullState{}, err
	}
	if t.Issue != 0 && !strings.Contains(body, fmt.Sprintf("#%d", t.Issue)) {
		body += fmt.Sprintf("\n\nCloses #%d", t.Issue)
	}
	if t.PR != 0 {
		if err := o.gh.UpdatePullBody(ctx, ar.project.Repo, t.PR, body); err != nil {
			return PullState{}, err
		}
	} else {
		n, _, err := o.gh.CreatePull(ctx, ar.project.Repo, branch, ar.project.DefaultBranch, title, body, draft)
		if err != nil {
			return PullState{}, err
		}
		t.PR = n
		t.Branch = branch
		if err := o.save(ctx, &t); err != nil {
			return PullState{}, err
		}
		ar.ticket.PR = n
	}
	ar.mu.Lock()
	ar.prOpened = true
	ar.mu.Unlock()
	o.event(ctx, ar, domain.EventSystem, fmt.Sprintf("PR #%d", t.PR))
	return o.gh.Pull(ctx, ar.project.Repo, t.PR)
}

// ToolUpdatePR replaces the PR description.
func (o *Orchestrator) ToolUpdatePR(ctx context.Context, token, body string) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return err
	}
	if t.PR == 0 {
		return invalid("this ticket has no PR yet; use open_pr")
	}
	if t.Issue != 0 && !strings.Contains(body, fmt.Sprintf("#%d", t.Issue)) {
		body += fmt.Sprintf("\n\nCloses #%d", t.Issue)
	}
	return o.gh.UpdatePullBody(ctx, ar.project.Repo, t.PR, body)
}

// ToolCIStatus returns the ticket's PR state and checks (or PR n for jobs).
func (o *Orchestrator) ToolCIStatus(ctx context.Context, token string, number int) (PullState, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return PullState{}, err
	}
	if number == 0 {
		number = ar.ticket.PR
	}
	if number == 0 {
		return PullState{}, invalid("this ticket has no PR yet")
	}
	return o.gh.Pull(ctx, ar.project.Repo, number)
}

// ToolCILogs returns the log tail of a failed check on the ticket's PR (or PR n).
func (o *Orchestrator) ToolCILogs(ctx context.Context, token, check string, number int) (string, error) {
	pr, err := o.ToolCIStatus(ctx, token, number)
	if err != nil {
		return "", err
	}
	ar, _ := o.tokenRun(token)
	var out strings.Builder
	for _, c := range pr.Checks {
		if check != "" && c.Name != check {
			continue
		}
		if c.Conclusion != "failure" && c.Conclusion != "timed_out" && c.Conclusion != "cancelled" {
			continue
		}
		logs, err := o.gh.FailedLogs(ctx, ar.project.Repo, c)
		if err != nil {
			logs = "could not fetch logs: " + err.Error()
		}
		fmt.Fprintf(&out, "## %s (%s)\n%s\n\n", c.Name, c.Conclusion, logs)
	}
	if out.Len() == 0 {
		return "no failed checks", nil
	}
	return out.String(), nil
}

// ToolListPulls lists the project's open PRs (for scheduled jobs).
func (o *Orchestrator) ToolListPulls(ctx context.Context, token string) ([]PullState, error) {
	ar, err := o.tokenRun(token)
	if err != nil {
		return nil, err
	}
	return o.gh.ListPulls(ctx, ar.project.Repo)
}

// ToolComment posts a comment on the ticket's issue (or PR n for jobs).
func (o *Orchestrator) ToolComment(ctx context.Context, token, body string, number int) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	if number == 0 {
		number = ar.ticket.Issue
	}
	if number == 0 {
		return invalid("no issue or PR to comment on")
	}
	return o.gh.UpsertComment(ctx, ar.project.Repo, number, "", body)
}

// ToolStageDone records the run's summary; the run should exit after it.
func (o *Orchestrator) ToolStageDone(ctx context.Context, token, summary string) error {
	ar, err := o.tokenRun(token)
	if err != nil {
		return err
	}
	ar.mu.Lock()
	ar.stageDone = true
	ar.summary = summary
	ar.mu.Unlock()
	o.event(ctx, ar, domain.EventSystem, "stage done: "+summary)
	return nil
}
