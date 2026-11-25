// Package skills keeps the agents' skills in a git repository on the server:
// listing, editing (every save is a commit), removing, snapshotting the
// current HEAD for runs, and benchmarking skills with ablation evals.
package skills

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ErrInvalid is returned for a bad skill name or path.
var ErrInvalid = errors.New("invalid skill request")

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Skill is one skill directory (or the agents/ directory).
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
	HasEvals    bool     `json:"hasEvals"`
	Agents      bool     `json:"agents,omitempty"` // the agents/ directory
}

// Manager owns the skills repository.
type Manager struct {
	dir     string // the git repository
	snapDir string // snapshots of HEAD, one per commit
	seed    string // default skills to start from

	mu sync.Mutex
}

// New returns a manager for the repository at dir, seeded from seed on first
// use, with snapshots under snapDir.
func New(dir, snapDir, seed string) *Manager {
	return &Manager{dir: dir, snapDir: snapDir, seed: seed}
}

// Dir returns the repository path.
func (m *Manager) Dir() string { return m.dir }

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", m.dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=remote-agent", "GIT_AUTHOR_EMAIL=remote-agent@localhost",
		"GIT_COMMITTER_NAME=remote-agent", "GIT_COMMITTER_EMAIL=remote-agent@localhost")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Init creates the repository from the seed skills when it does not exist.
func (m *Manager) Init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(filepath.Join(m.dir, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	if m.seed != "" {
		if err := copyTree(m.seed, m.dir); err != nil {
			return fmt.Errorf("seed skills: %w", err)
		}
	}
	if _, err := m.git(ctx, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if _, err := m.git(ctx, "add", "-A"); err != nil {
		return err
	}
	_, err := m.git(ctx, "commit", "-q", "--allow-empty", "-m", "chore: seed default skills")
	return err
}

// List returns every skill, plus the agents directory when present.
func (m *Manager) List(ctx context.Context) ([]Skill, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	out := []Skill{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(m.dir, e.Name())
		files, err := listFiles(dir)
		if err != nil {
			return nil, err
		}
		s := Skill{Name: e.Name(), Files: files}
		if e.Name() == "agents" {
			s.Agents = true
			s.Description = "Subagent definitions"
		} else if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue
		} else {
			s.Description = description(filepath.Join(dir, "SKILL.md"))
		}
		if fi, err := os.Stat(filepath.Join(dir, "evals")); err == nil && fi.IsDir() {
			s.HasEvals = true
		}
		out = append(out, s)
	}
	return out, nil
}

func listFiles(dir string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "results" {
			return filepath.SkipDir // eval results are not part of the skill
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// description reads the description field of a SKILL.md front matter.
func description(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inFront := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			if inFront {
				return ""
			}
			inFront = true
			continue
		}
		if inFront && strings.HasPrefix(line, "description:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), `"'`)
		}
	}
	return ""
}

// path resolves name/file inside the repository, refusing escapes.
func (m *Manager) path(name, file string) (string, error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("%w: bad skill name %q", ErrInvalid, name)
	}
	clean := filepath.Clean("/" + file)[1:]
	if clean == "" || strings.HasPrefix(clean, ".git") {
		return "", fmt.Errorf("%w: bad file %q", ErrInvalid, file)
	}
	return filepath.Join(m.dir, name, clean), nil
}

// Read returns one file of a skill.
func (m *Manager) Read(name, file string) (string, error) {
	p, err := m.path(name, file)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", name, file, err)
	}
	return string(raw), nil
}

// Write saves one file of a skill (creating the skill if new) and commits.
func (m *Manager) Write(ctx context.Context, name, file, content, message string) (string, error) {
	p, err := m.path(name, file)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s/%s: %w", name, file, err)
	}
	if message == "" {
		message = fmt.Sprintf("docs(%s): edit %s", name, file)
	}
	return m.commit(ctx, message, filepath.Join(name, filepath.Clean("/" + file)[1:]))
}

// Delete removes a skill and commits.
func (m *Manager) Delete(ctx context.Context, name, message string) (string, error) {
	p, err := m.path(name, "SKILL.md")
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		return "", fmt.Errorf("%w: no skill %q", ErrInvalid, name)
	}
	if err := os.RemoveAll(filepath.Dir(p)); err != nil {
		return "", fmt.Errorf("remove skill: %w", err)
	}
	if message == "" {
		message = fmt.Sprintf("chore(%s): remove skill", name)
	}
	return m.commit(ctx, message, name)
}

var commitID = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// Restore brings back a skill as it was before commit (the commit that
// removed it) and commits that.
func (m *Manager) Restore(ctx context.Context, name, commit string) (string, error) {
	if _, err := m.path(name, "SKILL.md"); err != nil {
		return "", err
	}
	if !commitID.MatchString(commit) {
		return "", fmt.Errorf("%w: bad commit %q", ErrInvalid, commit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.git(ctx, "checkout", commit+"^", "--", name); err != nil {
		return "", fmt.Errorf("%w: cannot restore %s from %s: %v", ErrInvalid, name, commit, err)
	}
	return m.commit(ctx, fmt.Sprintf("chore(%s): restore skill", name), name)
}

func (m *Manager) commit(ctx context.Context, message, path string) (string, error) {
	if _, err := m.git(ctx, "add", "-A", "--", path); err != nil {
		return "", err
	}
	if out, _ := m.git(ctx, "status", "--porcelain"); out == "" {
		return m.git(ctx, "rev-parse", "HEAD")
	}
	if _, err := m.git(ctx, "commit", "-q", "-m", message); err != nil {
		return "", err
	}
	return m.git(ctx, "rev-parse", "HEAD")
}

// Snapshot exports HEAD to a read-only directory (reused per commit) so a
// run sees one consistent version even if a skill is edited mid-run.
func (m *Manager) Snapshot(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	head, err := m.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	dst := filepath.Join(m.snapDir, head)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	tmp := dst + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", m.dir, "archive", "--format=tar", head)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("git archive: %w", err)
	}
	if err := untar(out, tmp); err != nil {
		_ = cmd.Wait()
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("git archive: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "agents"), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	m.prune(head)
	return dst, nil
}

// prune keeps the newest few snapshots; running agents hold theirs open by path.
func (m *Manager) prune(keep string) {
	entries, err := os.ReadDir(m.snapDir)
	if err != nil {
		return
	}
	type snap struct {
		name string
		mod  int64
	}
	var snaps []snap
	for _, e := range entries {
		if info, err := e.Info(); err == nil && e.Name() != keep {
			snaps = append(snaps, snap{e.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].mod > snaps[j].mod })
	for i, s := range snaps {
		if i >= 4 {
			_ = os.RemoveAll(filepath.Join(m.snapDir, s.name))
		}
	}
}

func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		p := filepath.Join(dst, filepath.Clean("/" + h.Name)[1:])
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
