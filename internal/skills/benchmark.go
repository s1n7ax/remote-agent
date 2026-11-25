package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// CaseResult is one eval case's with/without score.
type CaseResult struct {
	Name    string  `json:"name"`
	With    float64 `json:"with"`
	Without float64 `json:"without"`
	Delta   float64 `json:"delta"`
}

// Result is one skill's latest benchmark.
type Result struct {
	Skill     string       `json:"skill"`
	Status    string       `json:"status"` // running | done | failed
	Error     string       `json:"error,omitempty"`
	With      float64      `json:"with"`
	Without   float64      `json:"without"`
	Delta     float64      `json:"delta"`
	CostUSD   float64      `json:"costUsd"`
	Cases     []CaseResult `json:"cases"`
	Verdict   string       `json:"verdict,omitempty"` // keep | remove? | investigate
	StartedAt time.Time    `json:"startedAt"`
	EndedAt   *time.Time   `json:"endedAt,omitempty"`
}

// Bench runs ablation evals (`claude plugin eval --ablation with-without`)
// one skill at a time in the background and keeps the latest result per skill.
type Bench struct {
	m         *Manager
	claudeBin string
	outDir    string
	// Env returns the environment for an eval run (a Claude account token).
	Env func(ctx context.Context) ([]string, error)
	// Sandbox, when set, wraps an eval run (which lets Claude use tools) so
	// it sees only its work dir read-write, like an agent run. nil runs it
	// unsandboxed (dev and tests only).
	Sandbox func(workdir string) func(argv, env []string) ([]string, []string)

	mu      sync.Mutex
	running bool
}

// NewBench returns a benchmark runner writing results under outDir.
func NewBench(m *Manager, claudeBin, outDir string, env func(context.Context) ([]string, error)) *Bench {
	return &Bench{m: m, claudeBin: claudeBin, outDir: outDir, Env: env}
}

// Results returns the latest result of every benchmarked skill.
func (b *Bench) Results() ([]Result, error) {
	entries, err := os.ReadDir(b.outDir)
	if os.IsNotExist(err) {
		return []Result{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Result{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var r Result
		raw, err := os.ReadFile(filepath.Join(b.outDir, e.Name()))
		if err != nil || json.Unmarshal(raw, &r) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// Start benchmarks the named skills (all with evals when names is empty) in
// the background. It returns an error when a benchmark is already running.
func (b *Bench) Start(ctx context.Context, names []string) ([]string, error) {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil, fmt.Errorf("%w: a benchmark is already running", ErrInvalid)
	}
	b.running = true
	b.mu.Unlock()

	list, err := b.m.List(ctx)
	if err != nil {
		b.done()
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var targets []string
	for _, s := range list {
		if s.HasEvals && (len(want) == 0 || want[s.Name]) {
			targets = append(targets, s.Name)
		}
	}
	if len(targets) == 0 {
		b.done()
		return nil, fmt.Errorf("%w: no skill with evals matches", ErrInvalid)
	}
	snap, err := b.m.Snapshot(ctx)
	if err != nil {
		b.done()
		return nil, err
	}
	env, err := b.Env(ctx)
	if err != nil {
		b.done()
		return nil, err
	}
	for _, n := range targets {
		b.write(Result{Skill: n, Status: "running", StartedAt: time.Now().UTC()})
	}
	go func() {
		defer b.done()
		bg := context.WithoutCancel(ctx)
		for _, n := range targets {
			b.write(b.run(bg, snap, n, env))
		}
	}()
	return targets, nil
}

func (b *Bench) done() {
	b.mu.Lock()
	b.running = false
	b.mu.Unlock()
}

func (b *Bench) write(r Result) {
	if err := os.MkdirAll(b.outDir, 0o755); err != nil {
		return
	}
	raw, _ := json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(filepath.Join(b.outDir, r.Skill+".json"), raw, 0o644)
}

func (b *Bench) run(ctx context.Context, snap, name string, env []string) Result {
	r := Result{Skill: name, Status: "failed", StartedAt: time.Now().UTC()}
	defer func() { t := time.Now().UTC(); r.EndedAt = &t }()
	work, err := os.MkdirTemp("", "skill-eval-")
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer os.RemoveAll(work)
	// The eval writes results next to the suite; run it on a writable copy.
	if err := copyTree(filepath.Join(snap, name), filepath.Join(work, name)); err != nil {
		r.Error = err.Error()
		return r
	}
	out := filepath.Join(work, "result.json")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	argv := []string{b.claudeBin, "plugin", "eval", filepath.Join(work, name),
		"--ablation", "with-without", "--no-publish", "--threshold", "0", "--json", out,
		// The suite is the user's own skill and the eval runs sandboxed; without
		// this the CLI stops to ask and a headless run exits 1.
		"--trust-plugin"}
	env = append(append([]string{}, env...), "CLAUDE_CODE_WALNUT_SPIRE=1", "HOME="+work)
	if b.Sandbox != nil {
		argv, env = b.Sandbox(work)(argv, env)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env = env
	log, runErr := cmd.CombinedOutput()
	raw, err := readResult(work, filepath.Base(out))
	if err != nil && runErr != nil {
		r.Error = fmt.Sprintf("%v: %s", runErr, tail(string(log), 2000))
		return r
	}
	if err != nil {
		r.Error = err.Error()
		return r
	}
	parsed, err := ParseEval(raw)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	parsed.Skill, parsed.StartedAt = name, r.StartedAt
	return parsed
}

// readResult reads the eval's output from its sandbox-writable work dir
// without following symlinks out of it or blocking on a planted FIFO.
func readResult(work, name string) ([]byte, error) {
	root, err := os.OpenRoot(work)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	const limit = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && len(raw) > limit {
		err = fmt.Errorf("%s is larger than 16 MB", name)
	}
	return raw, err
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ParseEval reads `claude plugin eval --json` output.
func ParseEval(raw []byte) (Result, error) {
	var doc struct {
		CostUSD float64 `json:"costUsd"`
		Cases   []struct {
			Name       string `json:"name"`
			Aggregates struct {
				Score        float64 `json:"score"`
				ScoreWithout float64 `json:"scoreWithout"`
				Delta        float64 `json:"delta"`
			} `json:"aggregates"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Result{}, fmt.Errorf("parse eval result: %w", err)
	}
	r := Result{Status: "done", CostUSD: doc.CostUSD, Cases: []CaseResult{}}
	for _, c := range doc.Cases {
		r.Cases = append(r.Cases, CaseResult{Name: c.Name, With: c.Aggregates.Score, Without: c.Aggregates.ScoreWithout, Delta: c.Aggregates.Delta})
		r.With += c.Aggregates.Score
		r.Without += c.Aggregates.ScoreWithout
	}
	if n := float64(len(doc.Cases)); n > 0 {
		r.With /= n
		r.Without /= n
		r.Delta = r.With - r.Without
	}
	r.Verdict = verdict(r)
	return r, nil
}

// verdict applies the EVALS.md rule of thumb: Δ ≥ 0.15 keeps a skill; Δ ≈ 0
// with both arms high means the model already does it; both low needs a look.
func verdict(r Result) string {
	switch {
	case r.Delta >= 0.15:
		return "keep"
	case r.With >= 0.7 && r.Without >= 0.7:
		return "remove"
	default:
		return "investigate"
	}
}
