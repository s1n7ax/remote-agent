package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Project directory layout under <dataDir>/projects/<id>/ — the only host
// directory a project's runs can see:
//
//	repo/            main clone (default branch)
//	work/<ticket>/   the ticket's git worktree on its goal branch
//	home/            $HOME of the sandbox; per-account config dirs live here
func (o *Orchestrator) projectDir(projectID int64) string {
	return o.cfg.Path("projects", fmt.Sprint(projectID))
}

func (o *Orchestrator) repoDir(projectID int64) string {
	return filepath.Join(o.projectDir(projectID), "repo")
}

func (o *Orchestrator) workDir(t domain.Ticket) string {
	return filepath.Join(o.projectDir(t.ProjectID), "work", fmt.Sprint(t.ID))
}

func (o *Orchestrator) homeDir(projectID int64) string {
	return filepath.Join(o.projectDir(projectID), "home")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// branchName returns a ticket's goal branch: ra/<id>-<slug>.
func branchName(t domain.Ticket) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(t.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		slug = "ticket"
	}
	return fmt.Sprintf("ra/%d-%s", t.ID, slug)
}

// prepareWorkspace makes sure the project clone and the ticket's worktree
// exist and are up to date.
func (o *Orchestrator) prepareWorkspace(ctx context.Context, p domain.Project, t *domain.Ticket) (string, error) {
	repo := o.repoDir(p.ID)
	if err := os.MkdirAll(o.homeDir(p.ID), 0o700); err != nil {
		return "", fmt.Errorf("create home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if err := o.gh.Clone(ctx, p.Repo, repo); err != nil {
			return "", fmt.Errorf("clone %s: %w", p.Repo, err)
		}
	} else if err := o.gh.Fetch(ctx, repo); err != nil {
		return "", fmt.Errorf("fetch %s: %w", p.Repo, err)
	}
	if t.Kind == domain.KindJob {
		// Jobs work on the project clone itself (they look at PRs and create
		// their own branches through the tools).
		return repo, nil
	}
	if t.Branch == "" {
		t.Branch = branchName(*t)
	}
	work := o.workDir(*t)
	if _, err := os.Stat(filepath.Join(work, ".git")); err == nil {
		return work, nil
	}
	if err := os.MkdirAll(filepath.Dir(work), 0o755); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	base := "origin/" + p.DefaultBranch
	args := []string{"-C", repo, "worktree", "add", "-b", t.Branch, work, base}
	if git(ctx, "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+t.Branch) == nil {
		args = []string{"-C", repo, "worktree", "add", work, t.Branch}
	} else if git(ctx, "-C", repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+t.Branch) == nil {
		args = []string{"-C", repo, "worktree", "add", "-b", t.Branch, work, "origin/" + t.Branch}
	}
	if err := git(ctx, args...); err != nil {
		return "", fmt.Errorf("create worktree: %w", err)
	}
	return work, nil
}

func git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeWorkspace deletes a finished ticket's worktree; the branch stays.
func (o *Orchestrator) removeWorkspace(ctx context.Context, t domain.Ticket) {
	work := o.workDir(t)
	if _, err := os.Stat(work); err != nil {
		return
	}
	if err := git(ctx, "-C", o.repoDir(t.ProjectID), "worktree", "remove", "--force", work); err != nil {
		o.log.Warn("remove worktree", "ticket", t.ID, "err", err)
	}
}
