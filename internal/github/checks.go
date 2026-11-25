package github

import (
	"context"
	"fmt"
	"net/http"
)

// CheckState is the overall CI result of a commit.
type CheckState string

const (
	CheckPending CheckState = "pending"
	CheckSuccess CheckState = "success"
	CheckFailure CheckState = "failure"
)

// Check is one check run or commit status.
type Check struct {
	Name string `json:"name"`
	// Status is queued | in_progress | completed (check runs) or
	// pending | success | failure | error (commit statuses).
	Status string `json:"status"`
	// Conclusion is set on completed check runs: success, failure, neutral,
	// cancelled, skipped, timed_out, action_required, stale, startup_failure.
	Conclusion string     `json:"conclusion,omitempty"`
	URL        string     `json:"url,omitempty"`
	CheckRunID int64      `json:"checkRunId,omitempty"` // 0 for commit statuses
	State      CheckState `json:"state"`
}

// Checks is the combined CI picture of one commit.
type Checks struct {
	SHA    string     `json:"sha"`
	State  CheckState `json:"state"`
	Checks []Check    `json:"checks"`
}

type checkRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	DetailsURL string `json:"details_url"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	} `json:"output"`
}

type checkRunPage struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []checkRun `json:"check_runs"`
}

type commitStatus struct {
	Context   string `json:"context"`
	State     string `json:"state"`
	TargetURL string `json:"target_url"`
}

// CombinedChecks merges the latest check runs and commit statuses of sha.
//
// The overall state is failure when any check failed, else pending when any
// is still running, else success. A commit with no checks at all is pending
// (CI may not have registered yet); callers decide how long to wait.
func (c *Client) CombinedChecks(ctx context.Context, repo, sha string) (Checks, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Checks{}, err
	}
	out := Checks{SHA: sha, Checks: []Check{}}
	runsPath := fmt.Sprintf("%s/commits/%s/check-runs?filter=latest&per_page=100", p, sha)
	for i := 0; runsPath != "" && i < maxPages; i++ {
		var page checkRunPage
		next, err := c.do(ctx, http.MethodGet, runsPath, nil, &page)
		if err != nil {
			return Checks{}, fmt.Errorf("check runs of %s@%s: %w", repo, sha, err)
		}
		for _, r := range page.CheckRuns {
			out.Checks = append(out.Checks, Check{
				Name: r.Name, Status: r.Status, Conclusion: r.Conclusion,
				URL: r.HTMLURL, CheckRunID: r.ID, State: runState(r.Status, r.Conclusion),
			})
		}
		runsPath = next
	}
	var combined struct {
		Statuses []commitStatus `json:"statuses"`
	}
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/commits/%s/status?per_page=100", p, sha), nil, &combined); err != nil {
		return Checks{}, fmt.Errorf("statuses of %s@%s: %w", repo, sha, err)
	}
	for _, s := range combined.Statuses {
		out.Checks = append(out.Checks, Check{
			Name: s.Context, Status: s.State, URL: s.TargetURL, State: statusState(s.State),
		})
	}
	out.State = overall(out.Checks)
	return out, nil
}

func runState(status, conclusion string) CheckState {
	if status != "completed" {
		return CheckPending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return CheckSuccess
	case "":
		return CheckPending
	default: // failure, cancelled, timed_out, action_required, stale, startup_failure
		return CheckFailure
	}
}

func statusState(state string) CheckState {
	switch state {
	case "success":
		return CheckSuccess
	case "failure", "error":
		return CheckFailure
	default:
		return CheckPending
	}
}

func overall(checks []Check) CheckState {
	if len(checks) == 0 {
		return CheckPending
	}
	state := CheckSuccess
	for _, ch := range checks {
		switch ch.State {
		case CheckFailure:
			return CheckFailure
		case CheckPending:
			state = CheckPending
		}
	}
	return state
}
