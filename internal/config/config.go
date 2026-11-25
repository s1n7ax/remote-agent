// Package config loads remote-agent's JSON config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MCPServer is one MCP server agents may use.
type MCPServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Type    string            `json:"type,omitempty"` // stdio (default) | http
}

// Config is the whole config file.
type Config struct {
	Listen          string `json:"listen"`
	MCPListen       string `json:"mcpListen"`
	PreviewListen   string `json:"previewListen"`
	DataDir         string `json:"dataDir"`
	MaxParallelRuns int    `json:"maxParallelRuns"`
	// CIPollSeconds is how often waiting PRs are polled (default 60).
	CIPollSeconds int `json:"ciPollSeconds"`

	GitHubTokenFile   string `json:"githubTokenFile"`
	GitHubAPIURL      string `json:"githubApiUrl"`
	GitHubGraphQLURL  string `json:"githubGraphqlUrl"`
	GitURL            string `json:"gitUrl"`
	OpenRouterKeyFile string `json:"openrouterKeyFile"`
	// LoginTokenFile holds the token that pairs a browser and authenticates
	// host scripts; empty means <dataDir>/login-token, created on first start.
	LoginTokenFile string `json:"loginTokenFile"`

	ClaudeBin string `json:"claudeBin"`
	PiBin     string `json:"piBin"`
	BwrapBin  string `json:"bwrapBin"`
	// Sandbox runs agents in bubblewrap; it defaults to true when the key
	// is absent, and only tests or development set it to false.
	Sandbox bool `json:"sandbox"`
	// SandboxPaths are bound read-only into every sandbox.
	SandboxPaths []string `json:"sandboxPaths"`
	// SandboxPATH is $PATH inside the sandbox; defaults to SandboxEnv/bin.
	SandboxPATH string `json:"sandboxPath"`
	// SandboxEnv is a store path whose bin/ holds the sandbox's tools.
	SandboxEnv string `json:"sandboxEnv"`

	MCPServers map[string]MCPServer `json:"mcpServers"`
	// BrowserMCPServers name the MCP servers that start a browser; runs
	// holding one take the browser slot.
	BrowserMCPServers []string `json:"browserMcpServers"`

	SkillsSeedDir string `json:"skillsSeedDir"`
	PublicURL     string `json:"publicURL"`
	PreviewURL    string `json:"previewURL"`

	// FakeAgents makes every run use the fake agent (e2e tests).
	FakeAgents bool `json:"fakeAgents"`
}

// Load reads path and fills defaults.
func Load(path string) (Config, error) {
	c := Config{Sandbox: true}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", path, err)
	}
	c.defaults()
	return c, nil
}

func (c *Config) defaults() {
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&c.Listen, "127.0.0.1:7700")
	def(&c.MCPListen, "127.0.0.1:7701")
	def(&c.PreviewListen, "127.0.0.1:7702")
	def(&c.DataDir, "/var/lib/remote-agent")
	def(&c.LoginTokenFile, filepath.Join(c.DataDir, "login-token"))
	def(&c.GitHubAPIURL, "https://api.github.com")
	def(&c.GitHubGraphQLURL, "https://api.github.com/graphql")
	def(&c.GitURL, "https://github.com")
	def(&c.ClaudeBin, "claude")
	def(&c.PiBin, "pi")
	def(&c.BwrapBin, "bwrap")
	if c.SandboxEnv != "" {
		def(&c.SandboxPATH, c.SandboxEnv+"/bin")
	}
	def(&c.SandboxPATH, "/run/current-system/sw/bin")
	if c.MaxParallelRuns <= 0 {
		c.MaxParallelRuns = 2
	}
}

// Default returns a config with every default set.
func Default() Config {
	c := Config{Sandbox: true}
	c.defaults()
	return c
}

// Path joins elements under the data dir.
func (c Config) Path(elem ...string) string {
	return filepath.Join(append([]string{c.DataDir}, elem...)...)
}

// ReadSecret reads a secret file, trimming whitespace; empty path → "".
func ReadSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret %s: %w", path, err)
	}
	return strings.TrimSpace(string(raw)), nil
}
