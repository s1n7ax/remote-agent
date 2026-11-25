// Package sandbox builds the bubblewrap command a run executes in. The
// sandbox sees only the directories it is given (read-write or read-only)
// and read-only system paths; every namespace but the network (and, with
// NoNetwork, the network too) is unshared and the environment is exactly
// the one passed.
package sandbox

import (
	"errors"
	"strings"
)

// DefaultReadOnly are bound read-only and must exist.
var DefaultReadOnly = []string{"/nix/store"}

// DefaultReadOnlyTry are bound read-only when they exist: the NixOS system
// profile, /bin/sh and /usr/bin/env, TLS roots, name resolution and the user
// database.
var DefaultReadOnlyTry = []string{
	"/run/current-system/sw",
	"/bin", "/usr/bin",
	"/etc/static", "/etc/ssl", "/etc/pki",
	"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf",
	"/etc/passwd", "/etc/group",
}

// Config describes one sandbox.
type Config struct {
	// Bwrap is the bubblewrap executable; "bwrap" when empty.
	Bwrap string
	// Project, when set, is bound read-write and is the default workdir.
	Project string
	// Workdir is the directory the command starts in; Project when empty.
	// One of the two must be set.
	Workdir string
	// ReadWrite are (further) read-write binds, e.g. a run's ticket clone
	// and its $HOME.
	ReadWrite []string
	// ReadOnly must exist and are bound read-only, after DefaultReadOnly.
	ReadOnly []string
	// ReadOnlyTry are bound read-only when present, after
	// DefaultReadOnlyTry (e.g. the skills repo).
	ReadOnlyTry []string
	// NoNetwork unshares the network namespace too.
	NoNetwork bool
}

// Command returns the bwrap argv running argv with exactly env (KEY=VALUE)
// inside the sandbox.
func (c Config) Command(argv, env []string) ([]string, error) {
	if c.Project == "" && c.Workdir == "" {
		return nil, errors.New("sandbox: no project directory or workdir")
	}
	if len(argv) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	bwrap := c.Bwrap
	if bwrap == "" {
		bwrap = "bwrap"
	}
	workdir := c.Workdir
	if workdir == "" {
		workdir = c.Project
	}

	// No --new-session: the agent must stay in the process group proc
	// creates (Setpgid), or Pause's SIGSTOP to that group would reach only
	// bwrap while the agent keeps running and spending. --new-session
	// guards against TIOCSTI injection into a controlling terminal; agents
	// have none (stdio are pipes, the app runs as a systemd service).
	args := []string{bwrap, "--unshare-all"}
	if !c.NoNetwork {
		args = append(args, "--share-net")
	}
	args = append(args, "--die-with-parent", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp")
	for _, p := range append(append([]string{}, DefaultReadOnly...), c.ReadOnly...) {
		args = append(args, "--ro-bind", p, p)
	}
	for _, p := range append(append([]string{}, DefaultReadOnlyTry...), c.ReadOnlyTry...) {
		args = append(args, "--ro-bind-try", p, p)
	}
	// Read-write binds last, so they win over a read-only parent.
	rw := c.ReadWrite
	if c.Project != "" {
		rw = append([]string{c.Project}, rw...)
	}
	for _, p := range rw {
		args = append(args, "--bind", p, p)
	}
	args = append(args, "--clearenv")
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		args = append(args, "--setenv", k, v)
	}
	args = append(args, "--chdir", workdir, "--")
	return append(args, argv...), nil
}

// Wrap adapts c to agent.Spec.Wrap. The returned environment for bwrap
// itself is empty: the agent's environment travels as --setenv. A config
// without a project or workdir yields a command that fails to start rather than an
// unsandboxed one.
func (c Config) Wrap() func(argv, env []string) ([]string, []string) {
	return func(argv, env []string) ([]string, []string) {
		cmd, err := c.Command(argv, env)
		if err != nil {
			return []string{"/nonexistent/sandbox-misconfigured"}, []string{}
		}
		return cmd, []string{}
	}
}
