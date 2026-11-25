package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1n7ax/remote-agent/internal/github"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// bare creates <root>/<repo>.git with one commit on main and returns it.
func bare(t *testing.T, root, repo, content string) string {
	t.Helper()
	work := t.TempDir()
	git(t, work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "f"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "f")
	git(t, work, "commit", "-q", "-m", content)
	dst := filepath.Join(root, repo+".git")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", "-q", "--bare", work, dst)
	return git(t, dst, "rev-parse", "refs/heads/main")
}

// Fetch uses the project's stored repo, not the clone's origin URL (which a
// run could have pointed elsewhere).
func TestFetchIgnoresTamperedOrigin(t *testing.T) {
	root := t.TempDir()
	want := bare(t, root, "acme/demo", "real")
	evil := bare(t, root, "evil/demo", "evil")
	g := &GitHub{G: &github.Git{BaseURL: "file://" + root, Token: "tok"}}
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "repo")
	if err := g.Clone(ctx, "acme/demo", dir); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "config", "remote.origin.url", "file://"+root+"/evil/demo.git")
	git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	if err := g.Fetch(ctx, "acme/demo", dir); err != nil {
		t.Fatal(err)
	}
	got := git(t, dir, "rev-parse", "refs/remotes/origin/main")
	if got == evil || got != want {
		t.Fatalf("origin/main = %s, want %s from acme/demo (evil is %s)", got, want, evil)
	}
}
