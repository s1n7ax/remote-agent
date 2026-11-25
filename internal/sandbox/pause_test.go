package sandbox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent/proc"
	"github.com/s1n7ax/remote-agent/internal/sandbox"
)

// Pausing a run sends SIGSTOP to the process group proc creates; it must
// reach the agent inside bubblewrap, not only bwrap itself.
func TestStopAndContReachTheSandboxedAgent(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap is not installed")
	}
	if out, err := exec.Command(bwrap, "--unshare-all", "--ro-bind", "/", "/", "true").CombinedOutput(); err != nil {
		t.Skipf("bwrap cannot create namespaces here (user namespaces disabled?): %v: %s", err, out)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	if sh, err = filepath.EvalSymlinks(sh); err != nil {
		t.Fatal(err)
	}

	var count atomic.Int64
	cfg := sandbox.Config{Bwrap: bwrap, Project: t.TempDir()}
	p, err := proc.Start(proc.Options{
		Argv: []string{sh, "-c", "i=0; while :; do i=$((i+1)); echo $i; sleep 0.01; done"},
		Env:  []string{"PATH=" + os.Getenv("PATH")},
		Wrap: cfg.Wrap(),
		OnStdout: func(line []byte) {
			if n, err := strconv.ParseInt(string(line), 10, 64); err == nil {
				count.Store(n)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pgid := p.PID()
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-p.Done()
	})

	waitUntil := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s (counter %d)", what, count.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitUntil("the counter to start", func() bool { return count.Load() > 5 })

	if err := syscall.Kill(-pgid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // lines already in the pipe drain
	stopped := count.Load()
	time.Sleep(500 * time.Millisecond)
	if got := count.Load(); got != stopped {
		t.Fatalf("counter advanced from %d to %d after SIGSTOP: the agent kept running", stopped, got)
	}

	if err := syscall.Kill(-pgid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	waitUntil("the counter to resume", func() bool { return count.Load() > stopped+5 })
}

// An agent that leaves the run's process group (setsid) is stopped and
// continued too: Signal reaches every process in the sandbox's PID
// namespace.
func TestSignalReachesSetsidChild(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap is not installed")
	}
	if out, err := exec.Command(bwrap, "--unshare-all", "--ro-bind", "/", "/", "true").CombinedOutput(); err != nil {
		t.Skipf("bwrap cannot create namespaces here (user namespaces disabled?): %v: %s", err, out)
	}
	resolve := func(name string) string {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s", name)
		}
		if p, err = filepath.EvalSymlinks(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sh, setsid := resolve("sh"), resolve("setsid")

	var count atomic.Int64
	// The counter runs in a new session; its parent then just waits.
	loop := "i=0; while :; do i=$((i+1)); echo $i; sleep 0.01; done"
	cfg := sandbox.Config{Bwrap: bwrap, Project: t.TempDir(), ReadOnlyTry: []string{filepath.Dir(filepath.Dir(setsid))}}
	p, err := proc.Start(proc.Options{
		Argv: []string{sh, "-c", setsid + " " + sh + " -c '" + loop + "' & wait"},
		Env:  []string{"PATH=" + os.Getenv("PATH")},
		Wrap: cfg.Wrap(),
		OnStdout: func(line []byte) {
			if n, err := strconv.ParseInt(string(line), 10, 64); err == nil {
				count.Store(n)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pgid := p.PID()
	t.Cleanup(func() {
		_, _ = sandbox.Signal(pgid, syscall.SIGKILL)
		<-p.Done()
	})
	waitUntil := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s (counter %d)", what, count.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitUntil("the counter to start", func() bool { return count.Load() > 5 })

	// The group alone does not reach the counter: it left it.
	if err := syscall.Kill(-pgid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	before := count.Load()
	waitUntil("the setsid counter to keep running", func() bool { return count.Load() > before+5 })
	if err := syscall.Kill(-pgid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}

	n, err := sandbox.Signal(pgid, syscall.SIGSTOP)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("signalled %d processes, want bwrap, the sandbox init and the agent's", n)
	}
	time.Sleep(200 * time.Millisecond) // lines already in the pipe drain
	stopped := count.Load()
	time.Sleep(500 * time.Millisecond)
	if got := count.Load(); got != stopped {
		t.Fatalf("counter advanced from %d to %d after Signal(SIGSTOP): the setsid child kept running", stopped, got)
	}

	if _, err := sandbox.Signal(pgid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	waitUntil("the counter to resume", func() bool { return count.Load() > stopped+5 })
}
