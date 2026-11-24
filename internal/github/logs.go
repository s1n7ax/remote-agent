package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// MaxLogLines bounds what FailedLogs returns.
const MaxLogLines = 200

// maxLogBytes bounds how much of a job log's end is kept (the error is near
// the end).
const maxLogBytes = 32 << 20

// errorLine matches lines that typically mark where a job failed.
var errorLine = regexp.MustCompile(`##\[error\]|(?i)\b(error|fail(ed|ure)?|panic)\b[:!]?|^\s*--- FAIL|Process completed with exit code [1-9]`)

// FailedLogs returns the part of a check run's log that leads to its
// failure: up to MaxLogLines lines ending a few lines after the last line
// that looks like an error (or the log's tail when none does).
//
// For GitHub Actions the check run id is the job id, so the job log is
// fetched. Checks from other CI systems have no log here; their check run
// output (title, summary, text) is returned instead.
func (c *Client) FailedLogs(ctx context.Context, repo string, checkRunID int64) (string, error) {
	p, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	text, err := c.jobLog(ctx, fmt.Sprintf("%s/actions/jobs/%d/logs", p, checkRunID))
	if err == nil {
		return Tail(text, MaxLogLines), nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || (apiErr.StatusCode != http.StatusNotFound && apiErr.StatusCode != http.StatusForbidden && apiErr.StatusCode != http.StatusGone) {
		return "", fmt.Errorf("logs of check run %d in %s: %w", checkRunID, repo, err)
	}
	var run checkRun
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/check-runs/%d", p, checkRunID), nil, &run); err != nil {
		return "", fmt.Errorf("check run %d in %s: %w", checkRunID, repo, err)
	}
	parts := []string{}
	for _, s := range []string{run.Output.Title, run.Output.Summary, run.Output.Text} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("check %q has no log or output; see %s", run.Name, run.DetailsURL), nil
	}
	lines := strings.Split(strings.Join(parts, "\n\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-MaxLogLines):], "\n"), nil
}

// jobLog downloads a job log. GitHub answers with a redirect to signed blob
// storage, which is fetched without the token.
func (c *Client) jobLog(ctx context.Context, path string) (string, error) {
	target, err := c.resolve(path)
	if err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	noFollow := *c.httpClient()
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := send(&noFollow, req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 == 3 {
		_ = resp.Body.Close()
		loc, err := resp.Location()
		if err != nil {
			return "", fmt.Errorf("github: job log redirect: %w", err)
		}
		blob, err := http.NewRequestWithContext(ctx, http.MethodGet, loc.String(), nil)
		if err != nil {
			return "", fmt.Errorf("github: job log redirect: %w", err)
		}
		if resp, err = send(c.httpClient(), blob); err != nil {
			return "", err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	tb := &tailBuffer{max: maxLogBytes}
	if _, err := io.Copy(tb, resp.Body); err != nil {
		return "", fmt.Errorf("github: read job log: %w", err)
	}
	return string(tb.bytes()), nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) bytes() []byte {
	if len(t.buf) > t.max {
		return t.buf[len(t.buf)-t.max:]
	}
	return t.buf
}

// Tail returns at most n lines of log: the window ending three lines after
// the last error-looking line, or the last n lines when there is none.
func Tail(log string, n int) string {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if errorLine.MatchString(lines[i]) {
			end = min(i+4, len(lines))
			break
		}
	}
	return strings.Join(lines[max(0, end-n):end], "\n")
}
