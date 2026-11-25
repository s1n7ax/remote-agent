package skills

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// An eval runs through the sandbox, in its own work dir and with HOME there.
func TestBenchRunsInSandbox(t *testing.T) {
	snap := seed(t)
	fixture := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(fixture, []byte(`{"costUsd":0.5,"cases":[{"name":"c","aggregates":{"score":1,"scoreWithout":0.5,"delta":0.5}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var workdirs []string
	var inner []string
	b := NewBench(nil, "/nonexistent/claude", t.TempDir(), nil)
	b.Sandbox = func(workdir string) func(argv, env []string) ([]string, []string) {
		workdirs = append(workdirs, workdir)
		return func(argv, env []string) ([]string, []string) {
			inner = argv
			if !slices.Contains(env, "HOME="+workdir) {
				t.Errorf("eval HOME is not its work dir: %v", env)
			}
			// Stand-in for bwrap: write the result where the eval would.
			out := argv[slices.Index(argv, "--json")+1]
			return []string{"cp", fixture, out}, env
		}
	}
	r := b.run(context.Background(), snap, "questions", []string{"PATH=" + os.Getenv("PATH")})
	if r.Status != "done" || r.Delta != 0.5 {
		t.Fatalf("result = %+v", r)
	}
	if len(workdirs) != 1 || len(inner) == 0 || inner[0] != "/nonexistent/claude" {
		t.Fatalf("sandbox calls %v, argv %v", workdirs, inner)
	}
	if !slices.Contains(inner, "--trust-plugin") {
		t.Fatalf("eval argv %v lacks --trust-plugin; a headless run would stop to ask", inner)
	}
	if inner[3] != filepath.Join(workdirs[0], "questions") {
		t.Fatalf("eval suite %s is not in the sandbox work dir %s", inner[3], workdirs[0])
	}
	if _, err := os.Stat(workdirs[0]); !os.IsNotExist(err) {
		t.Fatal("work dir not cleaned up")
	}
}
