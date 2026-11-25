package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommandShape(t *testing.T) {
	c := Config{Bwrap: "/bin/bwrap", Project: "/p/a", ReadWrite: []string{"/cfg"}, ReadOnly: []string{"/skills"}}
	got, err := c.Command([]string{"claude", "-p"}, []string{"HOME=/p/a", "A=b=c", "junk"})
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Join(got, " ")
	for _, want := range []string{
		"/bin/bwrap --unshare-all --share-net --die-with-parent --proc /proc --dev /dev --tmpfs /tmp",
		"--ro-bind /nix/store /nix/store",
		"--ro-bind /skills /skills",
		"--ro-bind-try /etc/resolv.conf /etc/resolv.conf",
		"--bind /p/a /p/a --bind /cfg /cfg --clearenv --setenv HOME /p/a --setenv A b=c --chdir /p/a -- claude -p",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("command lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "--new-session") {
		t.Error("--new-session takes the agent out of the process group Pause signals")
	}
	if strings.Contains(s, "junk") {
		t.Error("malformed env entry passed through")
	}
	if _, err := (Config{}).Command([]string{"x"}, nil); err == nil {
		t.Error("want error without a project or workdir")
	}
	// Without a project, only the explicit binds are writable.
	got, err = Config{Workdir: "/w", ReadWrite: []string{"/w", "/h"}, ReadOnly: []string{"/r"}}.Command([]string{"x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s = strings.Join(got, " ")
	if !strings.Contains(s, "--ro-bind /r /r") || !strings.Contains(s, "--bind /w /w --bind /h /h --clearenv --chdir /w -- x") {
		t.Errorf("workdir-only command:\n%s", s)
	}
	argv, env := Config{}.Wrap()([]string{"x"}, nil)
	if argv[0] == "x" || env == nil {
		t.Error("misconfigured Wrap must not run the command unsandboxed")
	}
}

// TestIsolation runs a real sandbox.
func TestIsolation(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}
	if err := exec.Command(bwrap, "--unshare-all", "--ro-bind", "/", "/", "true").Run(); err != nil {
		t.Skipf("user namespaces unavailable: %v", err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	if sh, err = filepath.EvalSymlinks(sh); err != nil || !strings.HasPrefix(sh, "/nix/store/") {
		t.Skip("sh is not in /nix/store")
	}

	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	other := filepath.Join(parent, "other")
	for _, d := range []string{project, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(other, "secret")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST_ONLY", "leak")

	script := `set -e
test ! -e "$SECRET" || { echo "secret visible"; exit 1; }
test ! -e "$OTHER" || { echo "sibling dir visible"; exit 1; }
echo written > "$HOME/out.txt"
test "$(pwd)" = "$HOME"
set -- /nix/store/*; test -e "$1" || { echo "store not listable"; exit 1; }
exec 3< "$SH_BIN"; exec 3<&- # a store file opens for reading
test -z "${HOST_ONLY:-}" || { echo "host env leaked"; exit 1; }
if (: > /nix/store/should-fail) 2>/dev/null; then echo "store writable"; exit 1; fi
echo ok`
	c := Config{Bwrap: bwrap, Project: project}
	argv, env := c.Wrap()([]string{sh, "-c", script},
		[]string{"HOME=" + project, "SECRET=" + secret, "OTHER=" + other, "SH_BIN=" + sh, "PATH=" + filepath.Dir(sh)})
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("sandbox check failed: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(project, "out.txt"))
	if err != nil || string(b) != "written\n" {
		t.Fatalf("project not writable from the sandbox: %v %q", err, b)
	}
	if !slices.Contains(argv, "--clearenv") {
		t.Error("no --clearenv")
	}
}
