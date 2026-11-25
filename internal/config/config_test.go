package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c, err := Load(write(`{"sandboxEnv":"/nix/store/x-env"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Sandbox {
		t.Fatal("sandbox must default to on when the key is absent")
	}
	if c.SandboxPATH != "/nix/store/x-env/bin" {
		t.Fatalf("SandboxPATH = %q", c.SandboxPATH)
	}
	c, _ = Load(write(`{"sandbox":false}`))
	if c.Sandbox {
		t.Fatal("explicit false must disable the sandbox")
	}
}
