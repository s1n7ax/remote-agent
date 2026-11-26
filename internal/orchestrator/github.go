package orchestrator

import "context"

// Check is one CI check on a PR's head commit.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued | in_progress | completed
	Conclusion string `json:"conclusion"` // success | failure | … (empty while running)
	URL        string `json:"url"`
	ID         int64  `json:"id"`
	// Pending is true while the check has not finished.
	Pending bool `json:"pending"`
}

// PullState is what the CI poller needs to know about a PR.
type PullState struct {
	Number         int     `json:"number"`
	Title          string  `json:"title"`
	Branch         string  `json:"branch"`
	URL            string  `json:"url"`
	State          string  `json:"state"` // open | closed
	Merged         bool    `json:"merged"`
	Draft          bool    `json:"draft"`
	MergeableState string  `json:"mergeableState"` // clean | dirty | blocked | unstable | unknown …
	HeadSHA        string  `json:"headSha"`
	CI             string  `json:"ci"` // pending | success | failure | none
	Checks         []Check `json:"checks"`
}

// Issue is an open GitHub issue.
type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Labels []string `json:"labels"`
	Author string   `json:"author"`
}

// GitHub is the part of the GitHub client the orchestrator uses. Agents never
// see it; they reach it only through the app MCP tools.
type GitHub interface {
	DefaultBranch(ctx context.Context, repo string) (string, error)
	CreateIssue(ctx context.Context, repo, title, body string) (int, error)
	ListIssues(ctx context.Context, repo string) ([]Issue, error)
	UpdateIssueBody(ctx context.Context, repo string, number int, body string) error
	// UpsertComment edits the comment containing marker, or creates one.
	UpsertComment(ctx context.Context, repo string, number int, marker, body string) error
	CreatePull(ctx context.Context, repo, head, base, title, body string, draft bool) (number int, url string, err error)
	UpdatePullBody(ctx context.Context, repo string, number int, body string) error
	MarkReady(ctx context.Context, repo string, number int) error
	// MergePull merges a PR; method is merge | squash | rebase ("" = repo
	// default). It merges only when the PR's head is headSHA (GitHub
	// refuses otherwise); "" means the head at the time of the call.
	MergePull(ctx context.Context, repo string, number int, method, headSHA string) error
	ListPulls(ctx context.Context, repo string) ([]PullState, error)
	Pull(ctx context.Context, repo string, number int) (PullState, error)
	FailedLogs(ctx context.Context, repo string, check Check) (string, error)
	// Clone clones repo into dir, the app's own clone (repo/).
	Clone(ctx context.Context, repo, dir string) error
	// Fetch updates the remote-tracking branches of the app's clone dir
	// from repo. The repo is the project's stored one, never a remote read
	// from a clone's config.
	Fetch(ctx context.Context, repo, dir string) error
	// Push pushes branch of the app's clone dir to repo; it refuses the
	// default branch. dir is never agent-writable.
	Push(ctx context.Context, repo, dir, branch string) error
}
