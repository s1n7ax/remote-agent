package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// graphql runs one GraphQL operation and decodes its "data" into out.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	body := map[string]any{"query": query, "variables": vars}
	req, err := c.newRequest(ctx, http.MethodPost, c.graphqlURL(), body)
	if err != nil {
		return err
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("github: decode graphql: %w", err)
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, e := range res.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("github: graphql: %s", strings.Join(msgs, "; "))
	}
	if out != nil && len(res.Data) > 0 {
		if err := json.Unmarshal(res.Data, out); err != nil {
			return fmt.Errorf("github: decode graphql data: %w", err)
		}
	}
	return nil
}

const markReadyMutation = `mutation($id: ID!) {
  markPullRequestReadyForReview(input: {pullRequestId: $id}) {
    pullRequest { isDraft }
  }
}`

// MarkReady takes pull request number out of draft. It is a no-op when the
// pull request is not a draft. REST has no endpoint for this, so it uses the
// GraphQL markPullRequestReadyForReview mutation.
func (c *Client) MarkReady(ctx context.Context, repo string, number int) error {
	pr, err := c.GetPull(ctx, repo, number)
	if err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	if !pr.Draft {
		return nil
	}
	if pr.NodeID == "" {
		return fmt.Errorf("mark ready %s#%d: pull request has no node id", repo, number)
	}
	if err := c.graphql(ctx, markReadyMutation, map[string]any{"id": pr.NodeID}, nil); err != nil {
		return fmt.Errorf("mark ready %s#%d: %w", repo, number, err)
	}
	return nil
}
