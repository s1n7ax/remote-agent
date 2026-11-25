// Package agent runs coding agents (claude, pi, fake) headlessly and turns
// their output into domain run events.
package agent

import (
	"context"
	"errors"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// ErrStopped is the Done error of a session ended by Stop or by its context.
var ErrStopped = errors.New("agent stopped")

// ErrEnded is returned by Send and Interrupt once the session's run is over.
var ErrEnded = errors.New("agent session ended")

// Spec is everything a runner needs to start one run.
type Spec struct {
	Agent  domain.AgentKind
	Model  string
	Effort string // low | medium | high | xhigh | max (pi: thinking level)

	// Prompt is the first user message.
	Prompt string
	// SystemPrompt is appended to the agent's own system prompt.
	SystemPrompt string

	// Workdir is the directory the agent works in (inside the sandbox it is
	// the same path).
	Workdir string
	// Home is the sandbox's $HOME: per project, holds config dirs.
	Home string

	// ConfigDir is CLAUDE_CONFIG_DIR (claude) or PI_CODING_AGENT_DIR (pi);
	// the caller prepares skills, agents and MCP config in it.
	ConfigDir string
	// MCPConfig is a path to an MCP config JSON file (claude --mcp-config).
	MCPConfig string

	// Env holds extra environment, e.g. CLAUDE_CODE_OAUTH_TOKEN,
	// OPENROUTER_API_KEY. Never logged.
	Env map[string]string

	// Resume is a session file (claude: path to .jsonl) or id to continue.
	Resume string

	// Wrap turns the agent command into the sandboxed command; nil runs the
	// agent directly.
	Wrap func(argv []string, env []string) (cmd []string, cmdEnv []string)
}

// Limit reports a usage limit hit by a subscription account.
type Limit struct {
	ResetsAt time.Time
	Type     string // five_hour, seven_day, …
}

// Usage is a rate-limit utilization update.
type Usage struct {
	Utilization float64
	Warning     bool
	ResetsAt    time.Time
}

// Event is what a running agent emits. Exactly one of the pointer fields, or
// Log, is set.
type Event struct {
	Log *domain.RunEvent // a log line (ID/RunID unset)

	SessionID string // set once the agent reports its session

	Cost  float64 // incremental cost in USD (pi; claude reports total at end)
	Limit *Limit  // the account hit its limit; the run will end
	Usage *Usage  // utilization update

	// Done is set on the final event.
	Done    bool
	Err     error  // nil on success
	Summary string // final result text
}

// Session is one running agent.
type Session interface {
	// Events streams the agent's events; closed after the Done event.
	Events() <-chan Event
	// Send delivers a user message mid-run; the session logs it as an
	// EventUser line.
	Send(ctx context.Context, text string) error
	// Interrupt stops the current turn; the agent stays alive awaiting input.
	Interrupt(ctx context.Context) error
	// Stop ends the agent process.
	Stop() error
	// PID returns the process id (0 if not running).
	PID() int
}

// Runner starts sessions for one agent kind.
type Runner interface {
	Start(ctx context.Context, spec Spec) (Session, error)
}
