// Package proc holds what the process-backed runners share: starting an agent
// command (optionally sandboxed), line-oriented stdio, graceful shutdown and
// the event stream handed to the orchestrator.
package proc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Shutdown escalation, measured from the Shutdown call.
const (
	DefaultInterruptAfter = 5 * time.Second
	DefaultKillAfter      = 15 * time.Second
)

// drainTimeout bounds how long readers may keep going after the process
// exited; a grandchild holding the pipes open must not hang the run.
const drainTimeout = 2 * time.Second

// Options describes one agent process.
type Options struct {
	Argv []string
	Env  []string
	Dir  string
	// Wrap, when set, turns Argv and Env into the sandboxed command.
	Wrap func(argv, env []string) ([]string, []string)

	// OnStdout and OnStderr receive each line (without the trailing LF or
	// CR). Each is called from its own goroutine, one line at a time.
	OnStdout func(line []byte)
	OnStderr func(line []byte)

	// InterruptAfter and KillAfter override the shutdown escalation.
	InterruptAfter time.Duration
	KillAfter      time.Duration
}

// Process is a started agent command.
type Process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	opts  Options

	mu          sync.Mutex
	stdinClosed bool

	shutdown sync.Once
	reaped   chan struct{} // closed when Wait returned
	exited   chan struct{} // closed when output is delivered too
	err      error
}

// Start runs the command and its readers. Done is closed once the process has
// exited and its output has been delivered.
func Start(opts Options) (*Process, error) {
	if len(opts.Argv) == 0 {
		return nil, errors.New("proc: empty command")
	}
	if opts.InterruptAfter == 0 {
		opts.InterruptAfter = DefaultInterruptAfter
	}
	if opts.KillAfter == 0 {
		opts.KillAfter = DefaultKillAfter
	}
	argv, env := opts.Argv, opts.Env
	if opts.Wrap != nil {
		argv, env = opts.Wrap(argv, env)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = opts.Dir
	// Own process group, so signals reach helpers the agent spawned.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("proc: stdin pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("proc: stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, fmt.Errorf("proc: stderr pipe: %w", err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{outR, outW, errR, errW} {
			f.Close()
		}
		return nil, fmt.Errorf("proc: start %s: %w", argv[0], err)
	}
	outW.Close()
	errW.Close()

	p := &Process{cmd: cmd, stdin: stdin, opts: opts,
		reaped: make(chan struct{}), exited: make(chan struct{})}
	var readers sync.WaitGroup
	readers.Add(2)
	go readLines(&readers, outR, opts.OnStdout)
	go readLines(&readers, errR, opts.OnStderr)
	go p.wait(&readers, outR, errR)
	return p, nil
}

func (p *Process) wait(readers *sync.WaitGroup, files ...*os.File) {
	p.err = p.cmd.Wait()
	close(p.reaped)
	drained := make(chan struct{})
	go func() { readers.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(drainTimeout):
		for _, f := range files {
			f.Close()
		}
		<-drained
	}
	for _, f := range files {
		f.Close()
	}
	close(p.exited)
}

// readLines splits r on LF only (JSON strings may hold U+2028) and strips an
// optional CR.
func readLines(wg *sync.WaitGroup, r io.Reader, fn func([]byte)) {
	defer wg.Done()
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = trimEOL(line)
			if fn != nil && len(line) > 0 {
				fn(line)
			}
		}
		if err != nil {
			return
		}
	}
}

func trimEOL(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}

// WriteJSON writes v as one JSON line to the process's stdin.
func (p *Process) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("proc: encode: %w", err)
	}
	b = append(b, '\n')
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdinClosed {
		return os.ErrClosed
	}
	if _, err := p.stdin.Write(b); err != nil {
		return fmt.Errorf("proc: write stdin: %w", err)
	}
	return nil
}

// CloseStdin closes the process's stdin; idempotent.
func (p *Process) CloseStdin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stdinClosed {
		p.stdinClosed = true
		p.stdin.Close()
	}
}

// Shutdown closes stdin, then sends SIGINT after InterruptAfter and SIGKILL
// after KillAfter if the process is still running. It does not wait; use
// Done. Safe to call more than once.
func (p *Process) Shutdown() {
	p.shutdown.Do(func() {
		p.CloseStdin()
		go p.escalate()
	})
}

func (p *Process) escalate() {
	interrupt := time.NewTimer(p.opts.InterruptAfter)
	defer interrupt.Stop()
	kill := time.NewTimer(p.opts.KillAfter)
	defer kill.Stop()
	for {
		select {
		case <-p.reaped:
			return
		case <-interrupt.C:
			p.signal(syscall.SIGINT)
		case <-kill.C:
			p.signal(syscall.SIGKILL)
			return
		}
	}
}

// signal sends sig to the process group. Under bubblewrap the group holds
// bwrap; its --die-with-parent takes the sandbox down with it.
func (p *Process) signal(sig syscall.Signal) {
	select {
	case <-p.reaped:
		return
	default:
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}

// Done is closed once the process has exited and its output is delivered.
func (p *Process) Done() <-chan struct{} { return p.exited }

// Err is the process's exit error; valid after Done.
func (p *Process) Err() error { return p.err }

// PID returns the process id, or 0 once it has exited.
func (p *Process) PID() int {
	select {
	case <-p.reaped:
		return 0
	default:
		return p.cmd.Process.Pid
	}
}
