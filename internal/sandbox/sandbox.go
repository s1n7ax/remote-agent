// Package sandbox builds the bubblewrap command a run executes in. The
// sandbox sees only its project directory (read-write, also $HOME), the
// extra directories it is given and read-only system paths; every namespace
// but the network is unshared and the environment is exactly the one passed.
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
	// Project is bound read-write; it is the agent's $HOME and default
	// workdir.
	Project string
	// Workdir is the directory the command starts in; Project when empty.
	Workdir string
	// ReadWrite are extra read-write binds (e.g. a config dir kept outside
	// the project).
	ReadWrite []string
	// ReadOnly must exist and are bound read-only, after DefaultReadOnly.
	ReadOnly []string
	// ReadOnlyTry are bound read-only when present, after
	// DefaultReadOnlyTry (e.g. the skills repo).
	ReadOnlyTry []string
}

// Command returns the bwrap argv running argv with exactly env (KEY=VALUE)
// inside the sandbox.
func (c Config) Command(argv, env []string) ([]string, error) {
	if c.Project == "" {
		return nil, errors.New("sandbox: no project directory")
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

	args := []string{
		bwrap,
		"--unshare-all", "--share-net",
		"--die-with-parent", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
	}
	for _, p := range append(append([]string{}, DefaultReadOnly...), c.ReadOnly...) {
		args = append(args, "--ro-bind", p, p)
	}
	for _, p := range append(append([]string{}, DefaultReadOnlyTry...), c.ReadOnlyTry...) {
		args = append(args, "--ro-bind-try", p, p)
	}
	// Read-write binds last, so they win over a read-only parent.
	for _, p := range append([]string{c.Project}, c.ReadWrite...) {
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
// without a project yields a command that fails to start rather than an
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
