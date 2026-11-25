package mcpserver

import (
	"context"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/github"
)

// Run is the agent run a bearer token belongs to.
type Run struct {
	ID       int64
	TicketID int64
	Stage    domain.Stage
	// Workdir is the absolute directory the run works in; file paths the
	// agent passes must resolve (after symlinks) inside it.
	Workdir string
	// Tools limits the tools the run sees; empty means every tool. Names are
	// the Tool* constants.
	Tools []string
	// Expires, when set, makes the token invalid afterwards.
	Expires time.Time
}

// Preview asks the host to serve a dev server for the user.
type Preview struct {
	Branch  string // optional: the branch the preview shows (prototypes)
	Command string // shell command that starts the server in Workdir
	Port    int    // port the command listens on
}

// Proof is a run's test evidence. Files are absolute paths inside Workdir.
type Proof struct {
	Verdict string // PASS | FAIL
	Report  string // markdown
	Files   []string
}

// NewPR is a pull request the agent asks to open from the ticket's branch.
type NewPR struct {
	Title string
	Body  string
	Draft bool
}

// Pushed reports a branch push.
type Pushed struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

// Host carries out tool calls; the orchestrator implements it. Inputs are
// validated before the host sees them. An error is shown to the agent as the
// tool's error text, so it should say what to do differently.
type Host interface {
	// SubmitQuestions posts a validated question round on the ticket and
	// returns its round number.
	SubmitQuestions(ctx context.Context, run Run, purpose domain.Purpose, tree domain.QuestionTree) (round int, err error)
	// SubmitRequirement stores the ticket's requirement (markdown).
	SubmitRequirement(ctx context.Context, run Run, markdown string) error
	// SubmitDecisions stores the ticket's design decisions (markdown).
	SubmitDecisions(ctx context.Context, run Run, markdown string) error
	// SubmitPrototypes records prototype options for the user to pick from.
	SubmitPrototypes(ctx context.Context, run Run, prototypes []domain.Prototype) error
	// StartPreview starts (or restarts) a preview server and returns the URL
	// the user opens.
	StartPreview(ctx context.Context, run Run, p Preview) (url string, err error)
	// SubmitProof attaches test evidence to the ticket.
	SubmitProof(ctx context.Context, run Run, p Proof) error
	// UploadArtifact stores the file at path (absolute, inside Workdir).
	UploadArtifact(ctx context.Context, run Run, path string) (domain.Artifact, error)
	// PushBranch pushes the ticket's goal branch (never the default branch).
	PushBranch(ctx context.Context, run Run) (Pushed, error)
	// OpenPR opens the ticket's pull request from its goal branch.
	OpenPR(ctx context.Context, run Run, pr NewPR) (github.Pull, error)
	// UpdatePR replaces the ticket's pull request description.
	UpdatePR(ctx context.Context, run Run, number int, body string) (github.Pull, error)
	// CIStatus reports CI on the head of the ticket's pull request.
	CIStatus(ctx context.Context, run Run, number int) (github.Checks, error)
	// CILogs returns the failure log of the named check on that head.
	CILogs(ctx context.Context, run Run, number int, check string) (string, error)
	// GetIssue returns the ticket's GitHub issue.
	GetIssue(ctx context.Context, run Run, number int) (github.Issue, error)
	// Comment posts on the ticket's pull request, or its issue when there is
	// no pull request yet.
	Comment(ctx context.Context, run Run, number int, body string) (github.Comment, error)
	// ListPulls lists the project's open pull requests.
	ListPulls(ctx context.Context, run Run) ([]github.Pull, error)
	// GetTicket returns the run's ticket with its rounds, answers and proof.
	GetTicket(ctx context.Context, run Run) (any, error)
	// CreateIssue opens an issue in the project.
	CreateIssue(ctx context.Context, run Run, title, body string) (github.Issue, error)
	// StageDone ends the run's stage with a summary.
	StageDone(ctx context.Context, run Run, summary string) error
}
