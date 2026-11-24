package github

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Ref is one end of a pull request.
type Ref struct {
	Ref string `json:"ref"` // branch name
	SHA string `json:"sha"`
}

// Pull is a GitHub pull request.
type Pull struct {
	Number  int    `json:"number"`
	NodeID  string `json:"node_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"` // open | closed
	Draft   bool   `json:"draft"`
	Merged  bool   `json:"merged"` // only filled by GetPull
	HTMLURL string `json:"html_url"`
	User    User   `json:"user"`
	Head    Ref    `json:"head"`
	Base    Ref    `json:"base"`

	// Mergeable is nil while GitHub is still computing it (GetPull only).
	Mergeable *bool `json:"mergeable"`
	// MergeableState is clean, dirty (conflict), blocked, behind, unstable,
	// draft, has_hooks or unknown (GetPull only).
	MergeableState string `json:"mergeable_state"`

	MergedAt  *time.Time `json:"merged_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// NewPull describes a pull request to open.
type NewPull struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"` // branch name
	Base  string `json:"base"` // usually the default branch
	Draft bool   `json:"draft"`
}

// PullUpdate changes a pull request; nil fields are left alone.
type PullUpdate struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
}

// MergeResult is GitHub's answer to a merge.
type MergeResult struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

// ListPulls returns the open pull requests of repo plus those merged at or
// after mergedSince (zero means none), newest first within each group.
func (c *Client) ListPulls(ctx context.Context, repo string, mergedSince time.Time) ([]Pull, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	open, err := listAll[Pull](ctx, c, p+"/pulls?state=open&sort=created&direction=desc&per_page=100")
	if err != nil {
		return nil, fmt.Errorf("list pulls of %s: %w", repo, err)
	}
	if mergedSince.IsZero() {
		return open, nil
	}
	// Closed pulls sorted by update time: once a page's last pull was updated
	// before mergedSince, no later page can hold a recent merge.
	out := open
	err = list(ctx, c, p+"/pulls?state=closed&sort=updated&direction=desc&per_page=100", func(page []Pull) bool {
		for _, pr := range page {
			if pr.MergedAt != nil && !pr.MergedAt.Before(mergedSince) {
				pr.Merged = true
				out = append(out, pr)
			}
		}
		return len(page) > 0 && !page[len(page)-1].UpdatedAt.Before(mergedSince)
	})
	if err != nil {
		return nil, fmt.Errorf("list merged pulls of %s: %w", repo, err)
	}
	return out, nil
}

// GetPull fetches pull request number, including mergeability.
func (c *Client) GetPull(ctx context.Context, repo string, number int) (Pull, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Pull{}, err
	}
	var pr Pull
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", p, number), nil, &pr); err != nil {
		return Pull{}, fmt.Errorf("get pull %s#%d: %w", repo, number, err)
	}
	return pr, nil
}

// CreatePull opens a pull request.
func (c *Client) CreatePull(ctx context.Context, repo string, np NewPull) (Pull, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Pull{}, err
	}
	var pr Pull
	if _, err := c.do(ctx, http.MethodPost, p+"/pulls", np, &pr); err != nil {
		return Pull{}, fmt.Errorf("create pull %s from %s: %w", repo, np.Head, err)
	}
	return pr, nil
}

// UpdatePull edits a pull request's title and/or body.
func (c *Client) UpdatePull(ctx context.Context, repo string, number int, u PullUpdate) (Pull, error) {
	p, err := repoPath(repo)
	if err != nil {
		return Pull{}, err
	}
	var pr Pull
	if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/pulls/%d", p, number), u, &pr); err != nil {
		return Pull{}, fmt.Errorf("update pull %s#%d: %w", repo, number, err)
	}
	return pr, nil
}

// MergePull merges pull request number with method. A non-empty headSHA
// makes GitHub refuse (409) when the head moved since it was reviewed.
// Only the user's merge button may call this; agents have no merge tool.
func (c *Client) MergePull(ctx context.Context, repo string, number int, method MergeMethod, headSHA string) (MergeResult, error) {
	p, err := repoPath(repo)
	if err != nil {
		return MergeResult{}, err
	}
	in := map[string]string{"merge_method": string(method)}
	if headSHA != "" {
		in["sha"] = headSHA
	}
	var res MergeResult
	if _, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/pulls/%d/merge", p, number), in, &res); err != nil {
		return MergeResult{}, fmt.Errorf("merge pull %s#%d: %w", repo, number, err)
	}
	return res, nil
}
