package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "questions", "evals", "case1"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "questions", "SKILL.md"), []byte("---\nname: questions\ndescription: Ask good questions\n---\nbody\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "questions", "evals", "case1", "prompt.md"), []byte("x"), 0o644))
	must(os.MkdirAll(filepath.Join(dir, "agents"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "agents", "worker.md"), []byte("---\nname: worker\n---\n"), 0o644))
	return dir
}

func TestEditCommitSnapshotDelete(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	m := New(filepath.Join(root, "repo"), filepath.Join(root, "snaps"), seed(t))
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := m.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	var q Skill
	for _, s := range list {
		if s.Name == "questions" {
			q = s
		}
	}
	if q.Description != "Ask good questions" || !q.HasEvals {
		t.Fatalf("questions = %+v", q)
	}
	snap1, err := m.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := m.Write(ctx, "questions", "SKILL.md", "---\nname: questions\ndescription: v2\n---\n", "")
	if err != nil || sha == "" {
		t.Fatalf("write: %v", err)
	}
	snap2, _ := m.Snapshot(ctx)
	if snap1 == snap2 {
		t.Fatal("snapshot did not change after an edit")
	}
	got, _ := os.ReadFile(filepath.Join(snap2, "questions", "SKILL.md"))
	if string(got) != "---\nname: questions\ndescription: v2\n---\n" {
		t.Fatalf("snapshot content %q", got)
	}
	old, _ := os.ReadFile(filepath.Join(snap1, "questions", "SKILL.md"))
	if string(old) == string(got) {
		t.Fatal("old snapshot was modified")
	}
	if _, err := m.Delete(ctx, "questions", ""); err != nil {
		t.Fatal(err)
	}
	list, _ = m.List(ctx)
	if len(list) != 1 {
		t.Fatalf("after delete %+v", list)
	}
	log, _ := m.git(ctx, "log", "--oneline")
	if n := len(strings.Split(log, "\n")); n != 3 {
		t.Fatalf("want 3 commits, got %d:\n%s", n, log)
	}
}

func TestPathEscapesRejected(t *testing.T) {
	m := New(t.TempDir(), t.TempDir(), "")
	for _, c := range []struct{ name, file string }{{"../x", "SKILL.md"}, {"ok", ".git/config"}, {"ok", ""}} {
		if _, err := m.path(c.name, c.file); !errors.Is(err, ErrInvalid) {
			t.Errorf("path(%q,%q) = %v, want ErrInvalid", c.name, c.file, err)
		}
	}
	p, err := m.path("ok", "../../etc/passwd")
	if err != nil || !strings.HasPrefix(p, filepath.Join(m.dir, "ok")+"/") {
		t.Fatalf("traversal not contained: %s %v", p, err)
	}
}

func TestParseEval(t *testing.T) {
	raw := []byte(`{"costUsd":0.5,"cases":[{"name":"a","aggregates":{"score":0.8,"scoreWithout":0.4,"delta":0.4}},{"name":"b","aggregates":{"score":1,"scoreWithout":0.6,"delta":0.4}}]}`)
	r, err := ParseEval(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.With != 0.9 || r.Without != 0.5 || r.Verdict != "keep" || len(r.Cases) != 2 {
		t.Fatalf("got %+v", r)
	}
	r, _ = ParseEval([]byte(`{"cases":[{"name":"a","aggregates":{"score":0.95,"scoreWithout":0.9}}]}`))
	if r.Verdict != "remove" {
		t.Fatalf("verdict %s", r.Verdict)
	}
}
