package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/questions"
)

// The methods in this file back the app MCP tools. Each is scoped to one
// active run (the MCP server maps the run's bearer token to its id), so an
// agent can only touch its own ticket.

// ErrUnknownRun is returned for a run that is not active.
var ErrUnknownRun = errors.New("unknown or finished run")

func (o *Orchestrator) runByID(runID int64) (*activeRun, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ar := o.byRun[runID]
	if ar == nil {
		return nil, ErrUnknownRun
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
	Ticket     domain.Ticket       `json:"ticket"`
	Project    domain.Project      `json:"project"`
	Rounds     []domain.Round      `json:"rounds"`
	Prototypes []domain.Prototype  `json:"prototypes"`
	Proof      *domain.Proof       `json:"proof,omitempty"`
	Links      []domain.TicketLink `json:"links"`
	// linkDocs holds, by ticket id, the file each dependency and related
	// ticket gets under .ship/<goal>/links/.
	linkDocs map[int64]string
}

// ToolTicket returns the run's ticket, its rounds (with answers) and proof.
func (o *Orchestrator) ToolTicket(ctx context.Context, runID int64) (TicketContext, error) {
	ar, err := o.runByID(runID)
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
	links, err := o.st.TicketLinks(ctx, id)
	if err != nil {
		return tc, err
	}
	tc = TicketContext{Ticket: t, Project: p, Rounds: rds, Prototypes: ps, Links: links}
	if proof, ok, err := o.st.LatestProof(ctx, id); err == nil && ok {
		tc.Proof = &proof
	}
	return tc, nil
}

// ToolSubmitQuestions posts a question round on the run's ticket.
func (o *Orchestrator) ToolSubmitQuestions(ctx context.Context, runID int64, tree domain.QuestionTree, purpose domain.Purpose) (int, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return 0, err
	}
	ar.mu.Lock()
	already := ar.questions
	ar.mu.Unlock()
	if already {
		return 0, invalid("this run already submitted its questions; one submit_questions call per run")
	}
	if err := questions.Validate(tree); err != nil {
		return 0, invalid("%v", err)
	}
	if purpose == "" {
		purpose = tree.Purpose
	}
	if purpose == "" {
		purpose = domain.PurposeFollowUp
	}
	stage := ar.ticket.Stage
	switch purpose {
	case domain.PurposeRequirement, domain.PurposeDesign:
		stage = nextStage(stage)
	case domain.PurposeFollowUp:
	default:
		return 0, invalid("purpose must be requirement, design or follow_up")
	}
	tree.Purpose = purpose
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
func (o *Orchestrator) ToolSubmitRequirement(ctx context.Context, runID int64, markdown string) error {
	ar, err := o.runByID(runID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(markdown) == "" {
		return invalid("the requirement is empty")
	}
	t, err := o.mutate(ctx, ar.ticket.ID, func(t *domain.Ticket) {
		t.Requirement = markdown
		if title := requirementTitle(markdown); title != "" {
			t.Title = title
		}
	})
	if err != nil {
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
func (o *Orchestrator) ToolSubmitDecisions(ctx context.Context, runID int64, markdown string) error {
	ar, err := o.runByID(runID)
	if err != nil {
		return err
	}
	_, err = o.mutate(ctx, ar.ticket.ID, func(t *domain.Ticket) { t.Decisions = markdown })
	return err
}

// ToolSubmitPrototypes stores the prototypes the user will pick from.
func (o *Orchestrator) ToolSubmitPrototypes(ctx context.Context, runID int64, ps []domain.Prototype) error {
	ar, err := o.runByID(runID)
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
func (o *Orchestrator) ToolUploadArtifact(ctx context.Context, runID int64, path string) (string, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return "", err
	}
	return o.storeArtifact(ctx, ar, path)
}

// storeArtifact copies a file into the artifact store. Relative paths
// resolve against the run's workdir; the file must lie in the run's clone or
// its $HOME, the only project dirs its sandbox can write. The app runs
// outside the sandbox, so the file is opened once through an os.Root at that
// dir (no component may be a symlink leading out of it, nor an absolute
// symlink) and everything after reads that handle: swapping a path
// component afterwards changes nothing.
func (o *Orchestrator) storeArtifact(ctx context.Context, ar *activeRun, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(ar.workdir, path)
	}
	path = filepath.Clean(path)
	var base, rel string
	for _, dir := range []string{ar.workdir, o.homeDir(ar.ticket)} {
		if r, err := filepath.Rel(dir, path); err == nil && r != ".." && !strings.HasPrefix(r, "../") {
			base, rel = dir, r
			break
		}
	}
	if base == "" {
		return "", invalid("artifact %s is outside the run's clone and home", path)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// O_NONBLOCK: opening a planted FIFO must not hang the app.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", invalid("artifact %s: %v", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", invalid("artifact %s is not a regular file", path)
	}
	const maxSize = 20 << 20
	if info.Size() > maxSize {
		return "", invalid("artifact %s is larger than 20 MB", path)
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	if err := os.MkdirAll(o.cfg.Path("artifacts"), 0o700); err != nil {
		return "", fmt.Errorf("store artifact: %w", err)
	}
	out, err := os.OpenFile(o.cfg.Path("artifacts", id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("store artifact: %w", err)
	}
	// The file may grow after Stat; never store more than the limit.
	size, err := io.Copy(out, io.LimitReader(io.MultiReader(bytes.NewReader(head), f), maxSize))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(o.cfg.Path("artifacts", id))
		return "", fmt.Errorf("store artifact: %w", err)
	}
	mt := mime.TypeByExtension(filepath.Ext(path))
	if mt == "" {
		mt = http.DetectContentType(head)
	}
	_, err = o.st.AddArtifact(ctx, domain.Artifact{ID: id, TicketID: ar.ticket.ID, RunID: ar.run.ID, Name: filepath.Base(path), MIME: mt, Size: size})
	return id, err
}

// ArtifactPath returns where an artifact's bytes live.
func (o *Orchestrator) ArtifactPath(id string) string {
	return o.cfg.Path("artifacts", filepath.Base(id))
}

// ToolSubmitProof stores the verify stage's evidence and posts it on the PR.
func (o *Orchestrator) ToolSubmitProof(ctx context.Context, runID int64, verdict, report string, files []string) error {
	ar, err := o.runByID(runID)
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

// currentBranch returns the branch checked out in the clone dir. The clone
// is agent-writable, so it reads .git/HEAD (through an os.Root) instead of
// running git there.
func currentBranch(_ context.Context, dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("read current branch: %w", err)
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Join(".git", "HEAD"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("read current branch: %w", err)
	}
	defer f.Close()
	head := make([]byte, 1024)
	n, _ := io.ReadFull(f, head)
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(head[:n])), "ref: refs/heads/")
	if !ok || ref == "" {
		return "", invalid("the clone has no branch checked out")
	}
	return ref, nil
}

// pushBranch imports the branch from the run's clone into repo/ and pushes
// it from there, so no git that reads the agent's config runs with the
// token. The branch lands in a staging ref first and becomes refs/heads/
// <branch> only once checkChangedPaths accepts it.
func (o *Orchestrator) pushBranch(ctx context.Context, ar *activeRun, branch string) error {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if err := checkBranchName(ctx, branch); err != nil {
		return err
	}
	defer o.lockRepo(ar.project.ID)()
	repo := o.repoDir(ar.project.ID)
	staged := "refs/remote-agent/import/" + branch
	if err := o.importBranch(ctx, ar.project.ID, ar.workdir, branch, staged); err != nil {
		return err
	}
	defer func() { _ = gitQuiet(context.WithoutCancel(ctx), repo, "update-ref", "-d", staged) }()
	if err := checkChangedPaths(ctx, repo, staged, ar.project.DefaultBranch, branch); err != nil {
		return err
	}
	if err := git(ctx, "-C", repo, "update-ref", "refs/heads/"+branch, staged); err != nil {
		return err
	}
	return o.gh.Push(ctx, ar.project.Repo, repo, branch)
}

// ticketBranch matches a ticket's goal branch, ra/<ticket id>-<slug>.
var ticketBranch = regexp.MustCompile(`^ra/([0-9]+)-`)

// ToolPushBranch pushes the branch checked out in the run's workdir. A
// ticket's run pushes only its own branch. A job's run, until it has one,
// may push one new branch: not the default branch, not a branch that is
// on GitHub already (another PR's, say) and not another ticket's; the first
// push claims it as the job's branch. The GitHub layer refuses the default
// branch too.
func (o *Orchestrator) ToolPushBranch(ctx context.Context, runID int64) (string, error) {
	ar, err := o.runByID(runID)
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
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return "", err
	}
	switch {
	case t.Branch != "":
		if branch != t.Branch {
			return "", invalid("refusing to push %q: this ticket pushes only its branch %q", branch, t.Branch)
		}
	case t.Kind != domain.KindJob:
		return "", invalid("refusing to push %q: this ticket has no branch yet", branch)
	default:
		if err := o.claimJobBranch(ctx, ar, branch); err != nil {
			return "", err
		}
	}
	if err := o.pushBranch(ctx, ar, branch); err != nil {
		return "", err
	}
	ar.mu.Lock()
	ar.pushed = true
	ar.mu.Unlock()
	o.event(ctx, ar, domain.EventSystem, "pushed "+branch)
	if t.PR != 0 {
		o.RecheckCI(ar.ticket.ID)
	}
	return branch, nil
}

// claimJobBranch makes branch the job's own branch if no one else has it.
func (o *Orchestrator) claimJobBranch(ctx context.Context, ar *activeRun, branch string) error {
	if m := ticketBranch.FindStringSubmatch(branch); m != nil && m[1] != strconv.FormatInt(ar.ticket.ID, 10) {
		return invalid("refusing to push %q: it is ticket %s's branch", branch, m[1])
	}
	if err := checkBranchName(ctx, branch); err != nil {
		return err
	}
	o.claimMu.Lock()
	defer o.claimMu.Unlock()
	// What is on GitHub now, not at the run's start.
	if err := o.syncRepo(ctx, ar.project); err != nil {
		return err
	}
	repo := o.repoDir(ar.project.ID)
	for _, ref := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch} {
		if gitQuiet(ctx, repo, "rev-parse", "--verify", "--quiet", ref) == nil {
			return invalid("refusing to push %q: the branch exists already; a job pushes only a new branch of its own", branch)
		}
	}
	ts, err := o.st.ProjectTickets(ctx, ar.project.ID)
	if err != nil {
		return err
	}
	for _, other := range ts {
		if other.ID != ar.ticket.ID && other.Branch == branch {
			return invalid("refusing to push %q: it is ticket %d's branch", branch, other.ID)
		}
	}
	_, err = o.update(ctx, ar.ticket.ID, func(t *domain.Ticket) error {
		if t.Branch != "" && t.Branch != branch {
			return invalid("refusing to push %q: this job pushes only its branch %q", branch, t.Branch)
		}
		t.Branch = branch
		return nil
	})
	return err
}

// ToolOpenPR opens the ticket's PR (pushing first), or updates it when one
// exists. The body always closes the ticket's issue.
func (o *Orchestrator) ToolOpenPR(ctx context.Context, runID int64, title, body string, draft bool) (PullState, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return PullState{}, err
	}
	branch, err := o.ToolPushBranch(ctx, runID)
	if err != nil {
		return PullState{}, err
	}
	t, err := o.st.Ticket(ctx, ar.ticket.ID)
	if err != nil {
		return PullState{}, err
	}
	body = closesIssue(body, t.Issue)
	if t.PR != 0 {
		if err := o.gh.UpdatePullBody(ctx, ar.project.Repo, t.PR, body); err != nil {
			return PullState{}, err
		}
	} else {
		n, _, err := o.gh.CreatePull(ctx, ar.project.Repo, branch, ar.project.DefaultBranch, title, body, draft)
		if err != nil {
			return PullState{}, err
		}
		if t, err = o.mutate(ctx, t.ID, func(t *domain.Ticket) {
			t.PR = n
			t.Branch = branch
		}); err != nil {
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

// closesIssue makes a PR body close issue unless it mentions it already.
func closesIssue(body string, issue int) string {
	if issue != 0 && !strings.Contains(body, fmt.Sprintf("#%d", issue)) {
		body += fmt.Sprintf("\n\nCloses #%d", issue)
	}
	return body
}

// ToolUpdatePR replaces the PR description.
func (o *Orchestrator) ToolUpdatePR(ctx context.Context, runID int64, body string) error {
	ar, err := o.runByID(runID)
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
	body = closesIssue(body, t.Issue)
	return o.gh.UpdatePullBody(ctx, ar.project.Repo, t.PR, body)
}

// ToolCIStatus returns the ticket's PR state and checks (or PR n for jobs).
func (o *Orchestrator) ToolCIStatus(ctx context.Context, runID int64, number int) (PullState, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return PullState{}, err
	}
	return o.ciStatus(ctx, ar, number)
}

func (o *Orchestrator) ciStatus(ctx context.Context, ar *activeRun, number int) (PullState, error) {
	if number == 0 {
		number = ar.ticket.PR
	}
	if number == 0 {
		return PullState{}, invalid("this ticket has no PR yet")
	}
	return o.gh.Pull(ctx, ar.project.Repo, number)
}

// ToolCILogs returns the log tail of a failed check on the ticket's PR (or PR n).
func (o *Orchestrator) ToolCILogs(ctx context.Context, runID int64, check string, number int) (string, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return "", err
	}
	pr, err := o.ciStatus(ctx, ar, number)
	if err != nil {
		return "", err
	}
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
func (o *Orchestrator) ToolListPulls(ctx context.Context, runID int64) ([]PullState, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return nil, err
	}
	return o.gh.ListPulls(ctx, ar.project.Repo)
}

// ToolComment posts a comment on the ticket's issue (or PR n for jobs).
func (o *Orchestrator) ToolComment(ctx context.Context, runID int64, body string, number int) error {
	ar, err := o.runByID(runID)
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

// ToolStageDone records the run's summary. A stage whose outcome tool was
// called is finished without it.
func (o *Orchestrator) ToolStageDone(ctx context.Context, runID int64, summary string) error {
	ar, err := o.runByID(runID)
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

// ToolRepo returns the run's project repository (owner/name).
func (o *Orchestrator) ToolRepo(runID int64) (string, error) {
	ar, err := o.runByID(runID)
	if err != nil {
		return "", err
	}
	return ar.project.Repo, nil
}
