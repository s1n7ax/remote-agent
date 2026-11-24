// Package mcpserver is the app's MCP server for agent runs. Each run gets a
// bearer token; its tool calls go to a Host on behalf of that run. There is
// deliberately no tool that merges a pull request.
package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported to MCP clients.
const Version = "1.0.0"

// Server maps run tokens to per-run MCP servers and serves them over
// streamable HTTP. It is safe for concurrent use.
type Server struct {
	host    Host
	handler http.Handler

	mu   sync.RWMutex
	runs map[string]*registered // by token
}

type registered struct {
	run Run
	mcp *mcp.Server
}

const extraKey = "run"

// New returns a server whose tools call host.
func New(host Host) *Server {
	s := &Server{host: host, runs: map[string]*registered{}}
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if ti := auth.TokenInfoFromContext(r.Context()); ti != nil {
			if reg, ok := ti.Extra[extraKey].(*registered); ok {
				return reg.mcp
			}
		}
		return nil
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	s.handler = auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(streamable)
	return s
}

// ServeHTTP serves MCP over streamable HTTP. Requests without a registered,
// unexpired bearer token get 401.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Register starts accepting run and returns its bearer token.
func (s *Server) Register(run Run) (string, error) {
	if !filepath.IsAbs(run.Workdir) {
		return "", fmt.Errorf("mcpserver: register run %d: workdir %q is not absolute", run.ID, run.Workdir)
	}
	for _, name := range run.Tools {
		if !slices.Contains(ToolNames, name) {
			return "", fmt.Errorf("mcpserver: register run %d: unknown tool %q", run.ID, name)
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mcpserver: register run %d: %w", run.ID, err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	reg := &registered{run: run, mcp: s.newMCPServer(run)}
	s.mu.Lock()
	s.runs[token] = reg
	s.mu.Unlock()
	return token, nil
}

// Revoke stops accepting token; later requests with it get 401.
func (s *Server) Revoke(token string) {
	s.mu.Lock()
	delete(s.runs, token)
	s.mu.Unlock()
}

func (s *Server) verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	s.mu.RLock()
	reg, ok := s.runs[token]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown run token: %w", auth.ErrInvalidToken)
	}
	if !reg.run.Expires.IsZero() && time.Now().After(reg.run.Expires) {
		s.Revoke(token)
		return nil, fmt.Errorf("run token expired: %w", auth.ErrInvalidToken)
	}
	sum := sha256.Sum256([]byte(token))
	return &auth.TokenInfo{
		UserID:     hex.EncodeToString(sum[:8]),
		Expiration: reg.run.Expires,
		Extra:      map[string]any{extraKey: reg},
	}, nil
}

func (s *Server) newMCPServer(run Run) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "remote-agent", Version: Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	t := &tools{host: s.host, run: run}
	for _, def := range t.defs() {
		if len(run.Tools) == 0 || slices.Contains(run.Tools, def.tool.Name) {
			def.add(srv)
		}
	}
	return srv
}

const instructions = `Tools of the remote-agent app for this run. The app owns GitHub: you have no
GitHub credential, so push, open/update the pull request, read CI and comment only
through these tools. Nobody but the user can merge. The user is not watching live:
ask them things with submit_questions and finish your stage with stage_done.`
