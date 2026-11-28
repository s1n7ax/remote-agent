package app

import (
	"context"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/github"
	"github.com/s1n7ax/remote-agent/internal/mcpserver"
	"github.com/s1n7ax/remote-agent/internal/orchestrator"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// Host carries MCP tool calls to the orchestrator, keyed by run id.
type Host struct {
	O  *orchestrator.Orchestrator
	St *store.Store
	C  *github.Client
}

var _ mcpserver.Host = (*Host)(nil)

func pull(p orchestrator.PullState) github.Pull {
	return github.Pull{
		Number: p.Number, Title: p.Title, State: p.State, Draft: p.Draft, Merged: p.Merged,
		HTMLURL: p.URL, Head: github.Ref{Ref: p.Branch, SHA: p.HeadSHA}, MergeableState: p.MergeableState,
	}
}

// SubmitQuestions implements mcpserver.Host.
func (h *Host) SubmitQuestions(ctx context.Context, run mcpserver.Run, purpose domain.Purpose, tree domain.QuestionTree) (int, error) {
	return h.O.ToolSubmitQuestions(ctx, run.ID, tree, purpose)
}

// SubmitRequirement implements mcpserver.Host.
func (h *Host) SubmitRequirement(ctx context.Context, run mcpserver.Run, md string) error {
	return h.O.ToolSubmitRequirement(ctx, run.ID, md)
}

// SubmitDecisions implements mcpserver.Host.
func (h *Host) SubmitDecisions(ctx context.Context, run mcpserver.Run, md string) error {
	return h.O.ToolSubmitDecisions(ctx, run.ID, md)
}

// SubmitPrototypes implements mcpserver.Host.
func (h *Host) SubmitPrototypes(ctx context.Context, run mcpserver.Run, ps []domain.Prototype) error {
	return h.O.ToolSubmitPrototypes(ctx, run.ID, ps)
}

// StartPreview implements mcpserver.Host.
func (h *Host) StartPreview(ctx context.Context, run mcpserver.Run, p mcpserver.Preview) (string, error) {
	info, err := h.O.ToolStartPreview(ctx, run.ID, p.Branch, p.Command, p.Port)
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

// SubmitProof implements mcpserver.Host.
func (h *Host) SubmitProof(ctx context.Context, run mcpserver.Run, p mcpserver.Proof) ([]string, error) {
	return h.O.ToolSubmitProof(ctx, run.ID, p.Verdict, p.Report, p.Files)
}

// UploadArtifact implements mcpserver.Host.
func (h *Host) UploadArtifact(ctx context.Context, run mcpserver.Run, path string) (domain.Artifact, error) {
	id, err := h.O.ToolUploadArtifact(ctx, run.ID, path)
	if err != nil {
		return domain.Artifact{}, err
	}
	return h.St.Artifact(ctx, id)
}

// PushBranch implements mcpserver.Host.
func (h *Host) PushBranch(ctx context.Context, run mcpserver.Run) (mcpserver.Pushed, error) {
	branch, err := h.O.ToolPushBranch(ctx, run.ID)
	return mcpserver.Pushed{Branch: branch}, err
}

// OpenPR implements mcpserver.Host.
func (h *Host) OpenPR(ctx context.Context, run mcpserver.Run, pr mcpserver.NewPR) (github.Pull, error) {
	p, err := h.O.ToolOpenPR(ctx, run.ID, pr.Title, pr.Body, pr.Draft)
	return pull(p), err
}

// UpdatePR implements mcpserver.Host.
func (h *Host) UpdatePR(ctx context.Context, run mcpserver.Run, number int, body string) (github.Pull, error) {
	if err := h.O.ToolUpdatePR(ctx, run.ID, body); err != nil {
		return github.Pull{}, err
	}
	p, err := h.O.ToolCIStatus(ctx, run.ID, number)
	return pull(p), err
}

// CIStatus implements mcpserver.Host.
func (h *Host) CIStatus(ctx context.Context, run mcpserver.Run, number int) (github.Checks, error) {
	p, err := h.O.ToolCIStatus(ctx, run.ID, number)
	if err != nil {
		return github.Checks{}, err
	}
	cs := github.Checks{SHA: p.HeadSHA, State: github.CheckState(p.CI), Checks: []github.Check{}}
	for _, c := range p.Checks {
		cs.Checks = append(cs.Checks, github.Check{Name: c.Name, Status: c.Status, Conclusion: c.Conclusion, URL: c.URL, CheckRunID: c.ID})
	}
	return cs, nil
}

// CILogs implements mcpserver.Host.
func (h *Host) CILogs(ctx context.Context, run mcpserver.Run, number int, check string) (string, error) {
	return h.O.ToolCILogs(ctx, run.ID, check, number)
}

// GetIssue implements mcpserver.Host; number 0 means the ticket's issue.
func (h *Host) GetIssue(ctx context.Context, run mcpserver.Run, number int) (github.Issue, error) {
	repo, err := h.O.ToolRepo(run.ID)
	if err != nil {
		return github.Issue{}, err
	}
	if number == 0 {
		t, err := h.St.Ticket(ctx, run.TicketID)
		if err != nil {
			return github.Issue{}, err
		}
		number = t.Issue
	}
	return h.C.GetIssue(ctx, repo, number)
}

// Comment implements mcpserver.Host.
func (h *Host) Comment(ctx context.Context, run mcpserver.Run, number int, body string) (github.Comment, error) {
	return github.Comment{Body: body}, h.O.ToolComment(ctx, run.ID, body, number)
}

// ListPulls implements mcpserver.Host.
func (h *Host) ListPulls(ctx context.Context, run mcpserver.Run) ([]github.Pull, error) {
	ps, err := h.O.ToolListPulls(ctx, run.ID)
	out := make([]github.Pull, 0, len(ps))
	for _, p := range ps {
		out = append(out, pull(p))
	}
	return out, err
}

// GetTicket implements mcpserver.Host.
func (h *Host) GetTicket(ctx context.Context, run mcpserver.Run) (any, error) {
	return h.O.ToolTicket(ctx, run.ID)
}

// CreateIssue implements mcpserver.Host.
func (h *Host) CreateIssue(ctx context.Context, run mcpserver.Run, title, body string) (github.Issue, error) {
	repo, err := h.O.ToolRepo(run.ID)
	if err != nil {
		return github.Issue{}, err
	}
	return h.C.CreateIssue(ctx, repo, title, body)
}

// StageDone implements mcpserver.Host.
func (h *Host) StageDone(ctx context.Context, run mcpserver.Run, summary string) error {
	return h.O.ToolStageDone(ctx, run.ID, summary)
}
