// Package github is a small GitHub REST (and one GraphQL) client plus a git
// helper that pushes with the app's token. The base URLs are configurable so
// tests and the e2e fake GitHub can stand in for github.com.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBaseURL is the public GitHub REST API.
const DefaultBaseURL = "https://api.github.com"

// maxPages bounds every paginated listing.
const maxPages = 20

// ErrNotFound is wrapped by an APIError with status 404.
var ErrNotFound = errors.New("not found")

// APIError is a non-2xx response from GitHub.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string // GitHub's "message", or the raw body
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// Is makes errors.Is(err, ErrNotFound) true for 404s.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.StatusCode == http.StatusNotFound
}

// Client talks to the GitHub REST API with one token.
type Client struct {
	// BaseURL is the REST root, e.g. https://api.github.com (no trailing slash).
	BaseURL string
	// GraphQLURL is the GraphQL endpoint; empty means BaseURL + "/graphql".
	GraphQLURL string
	// Token is a PAT or installation token; empty sends no Authorization.
	Token string
	// HTTP is the client used for requests; nil means a client with a 30s
	// timeout.
	HTTP *http.Client
}

// New returns a client for baseURL ("" means DefaultBaseURL).
func New(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) graphqlURL() string {
	if c.GraphQLURL != "" {
		return c.GraphQLURL
	}
	return strings.TrimRight(c.BaseURL, "/") + "/graphql"
}

// repoPath validates "owner/name" and returns "/repos/owner/name".
func repoPath(repo string) (string, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("github: repo %q is not owner/name", repo)
	}
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

// resolve turns an API path, or a full URL from a pagination link, into a
// URL. Full URLs must point back at BaseURL so the token never leaks.
func (c *Client) resolve(path string) (string, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		return base + path, nil
	}
	if !strings.HasPrefix(path, base+"/") {
		return "", fmt.Errorf("github: refusing link outside %s: %s", base, path)
	}
	return path, nil
}

// newRequest builds a request to the absolute URL target.
func (c *Client) newRequest(ctx context.Context, method, target string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("github: encode %s %s: %w", method, target, err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, target, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// send performs req and returns the response for 2xx, else an *APIError.
// The caller closes the body.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	return send(c.httpClient(), req)
}

// send performs req with hc and returns the response for 2xx and unfollowed
// 3xx, else an *APIError. The caller closes the body.
func send(hc *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", req.Method, req.URL.Path, err)
	}
	if resp.StatusCode/100 == 2 || resp.StatusCode/100 == 3 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := strings.TrimSpace(string(raw))
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		msg = e.Message
	}
	return nil, &APIError{Method: req.Method, Path: req.URL.Path, StatusCode: resp.StatusCode, Message: msg}
}

// do sends a JSON request and decodes a JSON response into out (if non-nil).
// It returns the Link header's "next" URL, if any.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (next string, err error) {
	target, err := c.resolve(path)
	if err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, method, target, body)
	if err != nil {
		return "", err
	}
	resp, err := c.send(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return "", fmt.Errorf("github: decode %s %s: %w", method, path, err)
		}
	}
	return nextLink(resp.Header.Get("Link")), nil
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(h string) string {
	if m := linkNext.FindStringSubmatch(h); m != nil {
		return m[1]
	}
	return ""
}

// list GETs path and every following page (up to maxPages), calling page
// with each decoded page; page returns false to stop early.
func list[T any](ctx context.Context, c *Client, path string, page func([]T) bool) error {
	for i := 0; path != "" && i < maxPages; i++ {
		var items []T
		next, err := c.do(ctx, http.MethodGet, path, nil, &items)
		if err != nil {
			return err
		}
		if !page(items) {
			return nil
		}
		path = next
	}
	return nil
}

// listAll collects every item of a paginated listing.
func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	err := list(ctx, c, path, func(items []T) bool {
		all = append(all, items...)
		return true
	})
	return all, err
}
