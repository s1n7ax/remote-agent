package fake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Environment that selects a script. Each is looked up in Spec.Env first,
// then in the app's own environment.
const (
	// EnvScript is a path to a script file.
	EnvScript = "FAKE_AGENT_SCRIPT"
	// EnvScriptDir holds per-stage scripts named <stage>.json; it wins over
	// EnvScript when the stage's file exists.
	EnvScriptDir = "FAKE_AGENT_SCRIPT_DIR"
	// EnvStage names the run's stage.
	EnvStage = "REMOTE_AGENT_STAGE"
	// EnvMCPURL and EnvMCPToken locate the app MCP server.
	EnvMCPURL   = "REMOTE_AGENT_MCP_URL"
	EnvMCPToken = "REMOTE_AGENT_MCP_TOKEN"
)

// Step is one instruction of a script. Exactly one field is set.
type Step struct {
	// Log emits a text line.
	Log string `json:"log,omitempty"`
	// SleepMS pauses; Interrupt and Stop cut it short.
	SleepMS int `json:"sleep_ms,omitempty"`
	// MCP calls a tool on the app MCP server.
	MCP *MCPCall `json:"mcp,omitempty"`
	// Limit reports a usage limit resetting InS seconds from now and ends
	// the run.
	Limit *LimitStep `json:"limit,omitempty"`
	// Fail ends the run with this error.
	Fail string `json:"fail,omitempty"`
	// Exec runs a shell command in the workdir; a non-zero exit fails the
	// run.
	Exec string `json:"exec,omitempty"`
	// Cost reports spend in USD.
	Cost float64 `json:"cost,omitempty"`
	// WaitMessage blocks until a message is sent.
	WaitMessage bool `json:"wait_message,omitempty"`
	// Done ends the run successfully with this summary.
	Done *string `json:"done,omitempty"`
}

// MCPCall is a tool call on the app MCP server.
type MCPCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args,omitempty"`
}

// LimitStep is a usage limit.
type LimitStep struct {
	InS  int    `json:"in_s"`
	Type string `json:"type,omitempty"`
}

// DefaultScript runs when no script is configured.
var DefaultScript = []Step{
	{Log: "fake agent started"},
	{SleepMS: 10},
	{Log: "fake agent finished"},
}

// LoadScript picks the script for a run: <dir>/<stage>.json, else the
// EnvScript file, else DefaultScript.
func LoadScript(env map[string]string) ([]Step, error) {
	if dir, stage := lookup(env, EnvScriptDir), lookup(env, EnvStage); dir != "" && stage != "" {
		steps, err := readScript(filepath.Join(dir, filepath.Base(stage)+".json"))
		if !errors.Is(err, fs.ErrNotExist) {
			return steps, err
		}
	}
	if path := lookup(env, EnvScript); path != "" {
		return readScript(path)
	}
	return DefaultScript, nil
}

func readScript(path string) ([]Step, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fake: read script: %w", err)
	}
	var steps []Step
	if err := json.Unmarshal(b, &steps); err != nil {
		return nil, fmt.Errorf("fake: parse script %s: %w", path, err)
	}
	return steps, nil
}

func lookup(env map[string]string, key string) string {
	if v, ok := env[key]; ok {
		return v
	}
	return os.Getenv(key)
}
