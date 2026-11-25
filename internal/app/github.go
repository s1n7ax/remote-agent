package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/s1n7ax/remote-agent/internal/github"
	"github.com/s1n7ax/remote-agent/internal/orchestrator"
)

// GitHub adapts the REST client and the git helper to the orchestrator.
type GitHub struct {
	C *github.Client
	G *github.Git
}

var _ orchestrator.GitHub = (*GitHub)(nil)

// DefaultBranch implements orchestrator.GitHub.
func (g *GitHub) DefaultBranch(ctx context.Context, repo string) (string, error) {
	r, err := g.C.GetRepo(ctx, repo)
	if err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

// CreateIssue implements orchestrator.GitHub.
func (g *GitHub) CreateIssue(ctx context.Context, repo, title, body string) (int, error) {
	is, err := g.C.CreateIssue(ctx, repo, title, body)
	return is.Number, err
}

// ListIssues implements orchestrator.GitHub.
func (g *GitHub) ListIssues(ctx context.Context, repo string) ([]orchestrator.Issue, error) {
	is, err := g.C.ListIssues(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]orchestrator.Issue, 0, len(is))
	for _, i := range is {
		labels := make([]string, 0, len(i.Labels))
		for _, l := range i.Labels {
			labels = append(labels, l.Name)
		}
		out = append(out, orchestrator.Issue{Number: i.Number, Title: i.Title, URL: i.HTMLURL, Labels: labels, Author: i.User.Login})
	}
	return out, nil
}

// UpdateIssueBody implements orchestrator.GitHub.
func (g *GitHub) UpdateIssueBody(ctx context.Context, repo string, number int, body string) error {
	_, err := g.C.UpdateIssue(ctx, repo, number, github.IssueUpdate{Body: &body})
	return err
}

// UpsertComment implements orchestrator.GitHub; an empty marker always
// creates a new comment.
func (g *GitHub) UpsertComment(ctx context.Context, repo string, number int, marker, body string) error {
	var err error
	if marker == "" {
		_, err = g.C.CommentIssue(ctx, repo, number, body)
	} else {
		_, err = g.C.UpsertComment(ctx, repo, number, marker, body)
	}
	return err
}

// CreatePull implements orchestrator.GitHub.
func (g *GitHub) CreatePull(ctx context.Context, repo, head, base, title, body string, draft bool) (int, string, error) {
	p, err := g.C.CreatePull(ctx, repo, github.NewPull{Title: title, Body: body, Head: head, Base: base, Draft: draft})
	return p.Number, p.HTMLURL, err
}

// UpdatePullBody implements orchestrator.GitHub.
func (g *GitHub) UpdatePullBody(ctx context.Context, repo string, number int, body string) error {
	_, err := g.C.UpdatePull(ctx, repo, number, github.PullUpdate{Body: &body})
	return err
}

// MarkReady implements orchestrator.GitHub.
func (g *GitHub) MarkReady(ctx context.Context, repo string, number int) error {
	return g.C.MarkReady(ctx, repo, number)
}

// MergePull implements orchestrator.GitHub. It merges only the head the
// user saw (the current head), so a push racing the tap cannot sneak in.
func (g *GitHub) MergePull(ctx context.Context, repo string, number int, method string) error {
	p, err := g.C.GetPull(ctx, repo, number)
	if err != nil {
		return err
	}
	if method == "" {
		r, err := g.C.GetRepo(ctx, repo)
		if err != nil {
			return err
		}
		if ms := r.MergeMethods(); len(ms) > 0 {
			method = string(ms[0])
		}
	}
	_, err = g.C.MergePull(ctx, repo, number, github.MergeMethod(method), p.Head.SHA)
	return err
}

func (g *GitHub) state(ctx context.Context, repo string, p github.Pull) (orchestrator.PullState, error) {
	ps := orchestrator.PullState{
		Number: p.Number, Title: p.Title, Branch: p.Head.Ref, URL: p.HTMLURL, State: p.State,
		Merged: p.Merged || p.MergedAt != nil, Draft: p.Draft, MergeableState: p.MergeableState, HeadSHA: p.Head.SHA,
		CI: "none", Checks: []orchestrator.Check{},
	}
	if ps.Merged || ps.State == "closed" {
		return ps, nil
	}
	cs, err := g.C.CombinedChecks(ctx, repo, p.Head.SHA)
	if err != nil {
		return ps, err
	}
	if len(cs.Checks) > 0 {
		ps.CI = string(cs.State)
	}
	for _, c := range cs.Checks {
		ps.Checks = append(ps.Checks, orchestrator.Check{Name: c.Name, Status: c.Status, Conclusion: c.Conclusion, URL: c.URL, ID: c.CheckRunID})
	}
	return ps, nil
}

// Pull implements orchestrator.GitHub.
func (g *GitHub) Pull(ctx context.Context, repo string, number int) (orchestrator.PullState, error) {
	p, err := g.C.GetPull(ctx, repo, number)
	if err != nil {
		return orchestrator.PullState{}, err
	}
	return g.state(ctx, repo, p)
}

// ListPulls implements orchestrator.GitHub: open PRs with their CI state.
func (g *GitHub) ListPulls(ctx context.Context, repo string) ([]orchestrator.PullState, error) {
	ps, err := g.C.ListPulls(ctx, repo, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	out := make([]orchestrator.PullState, 0, len(ps))
	for _, p := range ps {
		st, err := g.state(ctx, repo, p)
		if err != nil {
			st.CI = "unknown"
		}
		out = append(out, st)
	}
	return out, nil
}

// FailedLogs implements orchestrator.GitHub.
func (g *GitHub) FailedLogs(ctx context.Context, repo string, c orchestrator.Check) (string, error) {
	if c.ID == 0 {
		return "this check is a commit status without logs; see " + c.URL, nil
	}
	return g.C.FailedLogs(ctx, repo, c.ID)
}

// Clone implements orchestrator.GitHub.
func (g *GitHub) Clone(ctx context.Context, repo, dir string) error { return g.G.Clone(ctx, repo, dir) }

// Fetch implements orchestrator.GitHub.
func (g *GitHub) Fetch(ctx context.Context, dir string) error {
	repo, err := originRepo(ctx, dir)
	if err != nil {
		return err
	}
	return g.G.Fetch(ctx, repo, dir)
}

// Push implements orchestrator.GitHub.
func (g *GitHub) Push(ctx context.Context, repo, dir, branch string) error {
	_, err := g.G.PushBranch(ctx, repo, dir, branch)
	return err
}

// originRepo returns owner/name from a checkout's origin remote.
func originRepo(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("read origin of %s: %w", dir, err)
	}
	u := strings.TrimSuffix(strings.TrimSpace(string(out)), ".git")
	parts := strings.Split(strings.ReplaceAll(u, ":", "/"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("origin %q of %s is not a GitHub repository URL", u, dir)
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1], nil
}
