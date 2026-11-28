package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// User is a GitHub account reference.
type User struct {
	Login string `json:"login"`
}

// Label is an issue label.
type Label struct {
	Name string `json:"name"`
}

// Issue is a GitHub issue.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"` // open | closed
	HTMLURL   string    `json:"html_url"`
	User      User      `json:"user"`
	Labels    []Label   `json:"labels"`
	Comments  int       `json:"comments"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// PullRequest is set when the "issue" is a pull request.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// IssueUpdate changes an issue; nil fields are left alone.
type IssueUpdate struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
	State *string `json:"state,omitempty"` // open | closed
	// StateReason is completed | not_planned | reopened.
	StateReason *string `json:"state_reason,omitempty"`
}

// Comment is an issue or pull request conversation comment.
type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateIssue opens an issue.
func (c *Client) CreateIssue(ctx context.Context, repo, title, body string) (Issue, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Issue{}, err
	}
	var is Issue
	in := map[string]string{"title": title, "body": body}
	if _, err := c.do(ctx, http.MethodPost, p+"/issues", in, &is); err != nil {
		return Issue{}, fmt.Errorf("create issue in %s: %w", repo, err)
	}
	return is, nil
}

// UpdateIssue edits issue number.
func (c *Client) UpdateIssue(ctx context.Context, repo string, number int, u IssueUpdate) (Issue, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Issue{}, err
	}
	var is Issue
	if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/%d", p, number), u, &is); err != nil {
		return Issue{}, fmt.Errorf("update issue %s#%d: %w", repo, number, err)
	}
	return is, nil
}

// GetIssue fetches issue number.
func (c *Client) GetIssue(ctx context.Context, repo string, number int) (Issue, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Issue{}, err
	}
	var is Issue
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d", p, number), nil, &is); err != nil {
		return Issue{}, fmt.Errorf("get issue %s#%d: %w", repo, number, err)
	}
	return is, nil
}

// ListIssues returns the open issues of repo, newest first, excluding pull
// requests.
func (c *Client) ListIssues(ctx context.Context, repo string) ([]Issue, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	all, err := listAll[Issue](ctx, c, p+"/issues?state=open&per_page=100")
	if err != nil {
		return nil, fmt.Errorf("list issues of %s: %w", repo, err)
	}
	out := all[:0]
	for _, is := range all {
		if is.PullRequest == nil {
			out = append(out, is)
		}
	}
	return out, nil
}

// CommentIssue adds a comment to an issue or pull request.
func (c *Client) CommentIssue(ctx context.Context, repo string, number int, body string) (Comment, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Comment{}, err
	}
	var cm Comment
	path := fmt.Sprintf("%s/issues/%d/comments", p, number)
	if _, err := c.do(ctx, http.MethodPost, path, map[string]string{"body": body}, &cm); err != nil {
		return Comment{}, fmt.Errorf("comment on %s#%d: %w", repo, number, err)
	}
	return cm, nil
}

// UpsertComment keeps one app-owned comment per marker on an issue or pull
// request: it edits the first comment whose body contains marker, or creates
// one. marker (e.g. "<!-- remote-agent:summary -->") is appended to body when
// body does not already contain it, so later calls find the comment.
func (c *Client) UpsertComment(ctx context.Context, repo string, number int, marker, body string) (Comment, error) {
	if marker == "" {
		return Comment{}, fmt.Errorf("upsert comment on %s#%d: empty marker", repo, number)
	}
	p, err := repoPath(repo)
	if err != nil {
		return Comment{}, err
	}
	if !strings.Contains(body, marker) {
		body = strings.TrimRight(body, "\n") + "\n\n" + marker
	}
	var found *Comment
	err = list(ctx, c, fmt.Sprintf("%s/issues/%d/comments?per_page=100", p, number), func(page []Comment) bool {
		for i := range page {
			if strings.Contains(page[i].Body, marker) {
				found = &page[i]
				return false
			}
		}
		return true
	})
	if err != nil {
		return Comment{}, fmt.Errorf("upsert comment on %s#%d: %w", repo, number, err)
	}
	if found == nil {
		return c.CommentIssue(ctx, repo, number, body)
	}
	var cm Comment
	path := fmt.Sprintf("%s/issues/comments/%d", p, found.ID)
	if _, err := c.do(ctx, http.MethodPatch, path, map[string]string{"body": body}, &cm); err != nil {
		return Comment{}, fmt.Errorf("edit comment %d on %s#%d: %w", found.ID, repo, number, err)
	}
	return cm, nil
}
