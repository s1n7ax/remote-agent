package github

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// gitServer serves bare repos under root via git http-backend and requires
// the token as Basic auth.
func gitServer(t *testing.T) (root string, g *Git, unauth *atomic.Int32) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	root = t.TempDir()
	backend := &cgi.Handler{
		Path: gitBin,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=app"},
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+testToken))
	unauth = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			unauth.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return root, &Git{BaseURL: srv.URL, Token: testToken}, unauth
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// seed creates bare repo o/r.git with one commit on main.
func seed(t *testing.T, root string) {
	t.Helper()
	bare := filepath.Join(root, "o", "r.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, bare, "init", "--bare", "-b", "main")
	work := t.TempDir()
	git(t, work, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "init")
	git(t, work, "push", bare, "main")
}

func TestGitCloneFetchPush(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root, g, _ := gitServer(t)
	seed(t, root)
	dir := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone(ctx, "o/r", dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), testToken) || strings.Contains(string(cfg), base64.StdEncoding.EncodeToString([]byte("x-access-token:"+testToken))) {
		t.Fatalf("token written to .git/config:\n%s", cfg)
	}
	if def, err := g.DefaultBranch(ctx, "o/r"); err != nil || def != "main" {
		t.Fatalf("DefaultBranch = %q, %v", def, err)
	}

	git(t, dir, "checkout", "-b", "goal")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "a")
	want := git(t, dir, "rev-parse", "HEAD")

	sha, err := g.PushBranch(ctx, "o/r", dir, "goal")
	if err != nil {
		t.Fatal(err)
	}
	if sha != want {
		t.Errorf("pushed %s, want %s", sha, want)
	}
	if got := git(t, filepath.Join(root, "o", "r.git"), "rev-parse", "refs/heads/goal"); got != want {
		t.Errorf("remote goal = %s, want %s", got, want)
	}

	// Rewritten history is not force-pushed.
	git(t, dir, "commit", "--amend", "-m", "amended")
	if _, err := g.PushBranch(ctx, "o/r", dir, "goal"); err == nil {
		t.Error("non-fast-forward push succeeded; PushBranch must never force")
	} else if !strings.Contains(err.Error(), "rejected") && !strings.Contains(err.Error(), "non-fast-forward") {
		t.Errorf("amended push failed for another reason: %v", err)
	}

	if err := g.Fetch(ctx, "o/r", dir); err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "rev-parse", "refs/remotes/origin/goal"); got != want {
		t.Errorf("fetched origin/goal = %s, want %s", got, want)
	}
}

func TestGitPushRefusals(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root, g, _ := gitServer(t)
	seed(t, root)
	dir := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone(ctx, "o/r", dir); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "branch", "feature")
	for _, branch := range []string{
		"main", "", "HEAD", "refs/heads/feature", "feature:main", "+feature", "-f", "a b", "x..y", "missing",
	} {
		if _, err := g.PushBranch(ctx, "o/r", dir, branch); !errors.Is(err, ErrPushRefused) {
			t.Errorf("PushBranch(%q) = %v, want ErrPushRefused", branch, err)
		}
	}
	for _, kv := range [][2]string{
		{"url.https://evil.example/.insteadOf", g.BaseURL + "/"},
		{"http.sslVerify", "false"},
		{"credential.helper", "store"},
		{"include.path", "/tmp/x"},
		{"core.sshCommand", "touch /tmp/pwned"},
	} {
		t.Run(kv[0], func(t *testing.T) {
			git(t, dir, "config", kv[0], kv[1])
			defer git(t, dir, "config", "--unset", kv[0])
			if _, err := g.PushBranch(ctx, "o/r", dir, "feature"); !errors.Is(err, ErrPushRefused) {
				t.Errorf("push with %s set = %v, want ErrPushRefused", kv[0], err)
			}
		})
	}
	// Hooks in the agent-writable clone never run.
	hook := filepath.Join(dir, ".git", "hooks", "pre-push")
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PushBranch(ctx, "o/r", dir, "feature"); err != nil {
		t.Fatalf("PushBranch(feature) = %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("pre-push hook ran")
	}
}

func TestGitNoTokenIsRejected(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root, g, unauth := gitServer(t)
	seed(t, root)
	g.Token = "wrong"
	err := g.Clone(ctx, "o/r", filepath.Join(t.TempDir(), "c"))
	if err == nil || unauth.Load() == 0 {
		t.Fatalf("clone with wrong token = %v (unauth hits %d)", err, unauth.Load())
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("token not redacted from error: %v", err)
	}
}
