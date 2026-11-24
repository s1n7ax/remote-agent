package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultGitBaseURL is where repositories are cloned from.
const DefaultGitBaseURL = "https://github.com"

// ErrPushRefused is wrapped when PushBranch will not push a branch.
var ErrPushRefused = errors.New("push refused")

// Git runs git against GitHub with the app's token. The token travels only as
// an HTTP header set through GIT_CONFIG_* environment variables for one git
// process: it is never written to a config file, a remote URL, or argv.
// Every command talks to an explicit URL under BaseURL, never to a remote
// named in the (agent-writable) repository config.
type Git struct {
	// Bin is the git executable; empty means "git" on PATH.
	Bin string
	// BaseURL is the git host root, e.g. https://github.com; empty means
	// DefaultGitBaseURL.
	BaseURL string
	// Token authenticates as x-access-token.
	Token string
}

// RemoteURL returns the HTTPS clone URL of repo ("owner/name").
func (g *Git) RemoteURL(repo string) (string, error) {
	if _, err := repoPath(repo); err != nil {
		return "", err
	}
	return g.baseURL() + "/" + repo + ".git", nil
}

func (g *Git) baseURL() string {
	if g.BaseURL == "" {
		return DefaultGitBaseURL
	}
	return strings.TrimRight(g.BaseURL, "/")
}

// Clone clones repo into dir (which must not exist or be empty).
func (g *Git) Clone(ctx context.Context, repo, dir string) error {
	remote, err := g.RemoteURL(repo)
	if err != nil {
		return err
	}
	if _, err := g.run(ctx, "", "clone", "--", remote, dir); err != nil {
		return fmt.Errorf("clone %s: %w", repo, err)
	}
	return nil
}

// Fetch updates the remote-tracking branches of the clone in dir from repo.
func (g *Git) Fetch(ctx context.Context, repo, dir string) error {
	remote, err := g.RemoteURL(repo)
	if err != nil {
		return err
	}
	if err := g.checkConfig(ctx, dir); err != nil {
		return fmt.Errorf("fetch %s: %w", repo, err)
	}
	if _, err := g.run(ctx, dir, "fetch", "--prune", "--no-tags", "--", remote,
		"+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return fmt.Errorf("fetch %s: %w", repo, err)
	}
	return nil
}

// DefaultBranch asks the remote which branch its HEAD points at.
func (g *Git) DefaultBranch(ctx context.Context, repo string) (string, error) {
	remote, err := g.RemoteURL(repo)
	if err != nil {
		return "", err
	}
	out, err := g.run(ctx, "", "ls-remote", "--symref", "--", remote, "HEAD")
	if err != nil {
		return "", fmt.Errorf("default branch of %s: %w", repo, err)
	}
	for _, line := range strings.Split(out, "\n") {
		// ref: refs/heads/main	HEAD
		if rest, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			if name, _, ok := strings.Cut(rest, "\t"); ok && name != "" {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("default branch of %s: remote HEAD is not a branch", repo)
}

// PushBranch pushes local branch refs/heads/<branch> of the clone in dir to
// refs/heads/<branch> of repo and returns the pushed commit. It never forces,
// and refuses (wrapping ErrPushRefused) the remote's default branch, names
// that are not plain branch names, and clones whose config could redirect or
// intercept the authenticated request.
func (g *Git) PushBranch(ctx context.Context, repo, dir, branch string) (string, error) {
	if err := g.checkBranch(ctx, branch); err != nil {
		return "", err
	}
	remote, err := g.RemoteURL(repo)
	if err != nil {
		return "", err
	}
	def, err := g.DefaultBranch(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("%w: cannot determine default branch: %w", ErrPushRefused, err)
	}
	if branch == def {
		return "", fmt.Errorf("%w: %q is the default branch of %s", ErrPushRefused, branch, repo)
	}
	if err := g.checkConfig(ctx, dir); err != nil {
		return "", err
	}
	ref := "refs/heads/" + branch
	sha, err := g.run(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%w: no local branch %q", ErrPushRefused, branch)
	}
	if _, err := g.run(ctx, dir, "push", "--porcelain", "--", remote, ref+":"+ref); err != nil {
		return "", fmt.Errorf("push %s to %s: %w", branch, repo, err)
	}
	return strings.TrimSpace(sha), nil
}

// checkBranch accepts only plain branch names: no refspec syntax, no
// leading "refs/" or "-", and valid per git check-ref-format.
func (g *Git) checkBranch(ctx context.Context, branch string) error {
	if branch == "" || branch == "HEAD" || strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, "refs/") || strings.ContainsAny(branch, ":+*?[\\ ~^") {
		return fmt.Errorf("%w: invalid branch name %q", ErrPushRefused, branch)
	}
	if _, err := g.run(ctx, "", "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("%w: invalid branch name %q", ErrPushRefused, branch)
	}
	return nil
}

// riskyConfig lists local config keys (lowercase prefixes) that could send
// the token elsewhere, weaken TLS, or run commands while git holds it.
var riskyConfig = []string{
	"http.", "https.", "url.", "credential", "include.", "includeif.",
	"core.hookspath", "core.fsmonitor", "core.sshcommand", "core.gitproxy",
	"core.askpass", "protocol.", "remote.", "pushurl", "transfer.", "uploadpack.",
	"receivepack.", "gpg.", "push.",
}

// checkConfig refuses a repository whose local or worktree config sets
// riskyConfig keys other than the plain origin entries a clone writes.
func (g *Git) checkConfig(ctx context.Context, dir string) error {
	out, err := g.run(ctx, dir, "config", "--list", "--show-scope", "--name-only", "--no-includes")
	if err != nil {
		return fmt.Errorf("read git config: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		scope, key, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || (scope != "local" && scope != "worktree") {
			continue
		}
		key = strings.ToLower(key)
		if key == "remote.origin.url" || key == "remote.origin.fetch" || strings.HasPrefix(key, "branch.") {
			continue
		}
		for _, p := range riskyConfig {
			if strings.HasPrefix(key, p) {
				return fmt.Errorf("%w: git config %q is not allowed", ErrPushRefused, key)
			}
		}
	}
	return nil
}

// run executes git (in dir when non-empty) with the token header scoped to
// BaseURL and hooks disabled, returning stdout. The token is redacted from
// errors.
func (g *Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)
	if dir != "" {
		full = append([]string{"-C", dir}, full...)
	}
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	if g.Token != "" {
		cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.Token))
		cmd.Env = append(cmd.Env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+g.baseURL()+"/.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+cred,
		)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if g.Token != "" {
			msg = strings.ReplaceAll(msg, g.Token, "***")
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.String(), nil
}
