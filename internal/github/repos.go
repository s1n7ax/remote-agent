package github

import (
	"context"
	"fmt"
	"net/http"
)

// MergeMethod is how a pull request is merged.
type MergeMethod string

const (
	MergeSquash MergeMethod = "squash"
	MergeMerge  MergeMethod = "merge"
	MergeRebase MergeMethod = "rebase"
)

// Repo is the part of a repository the app needs.
type Repo struct {
	FullName         string `json:"full_name"`
	DefaultBranch    string `json:"default_branch"`
	Private          bool   `json:"private"`
	HTMLURL          string `json:"html_url"`
	AllowSquashMerge bool   `json:"allow_squash_merge"`
	AllowMergeCommit bool   `json:"allow_merge_commit"`
	AllowRebaseMerge bool   `json:"allow_rebase_merge"`
}

// MergeMethods lists the allowed merge methods, preferring squash, then
// merge, then rebase.
func (r Repo) MergeMethods() []MergeMethod {
	var out []MergeMethod
	if r.AllowSquashMerge {
		out = append(out, MergeSquash)
	}
	if r.AllowMergeCommit {
		out = append(out, MergeMerge)
	}
	if r.AllowRebaseMerge {
		out = append(out, MergeRebase)
	}
	return out
}

// GetRepo fetches repo ("owner/name").
func (c *Client) GetRepo(ctx context.Context, repo string) (Repo, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Repo{}, err
	}
	var r Repo
	if _, err := c.do(ctx, http.MethodGet, p, nil, &r); err != nil {
		return Repo{}, fmt.Errorf("get repo %s: %w", repo, err)
	}
	return r, nil
}
