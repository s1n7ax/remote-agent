// Package app wires remote-agent's parts together and serves them.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/agent/claude"
	"github.com/s1n7ax/remote-agent/internal/agent/fake"
	"github.com/s1n7ax/remote-agent/internal/agent/pi"
	"github.com/s1n7ax/remote-agent/internal/api"
	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/github"
	"github.com/s1n7ax/remote-agent/internal/mcpserver"
	"github.com/s1n7ax/remote-agent/internal/orchestrator"
	"github.com/s1n7ax/remote-agent/internal/sandbox"
	"github.com/s1n7ax/remote-agent/internal/skills"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// Serve runs the app until ctx ends.
func Serve(ctx context.Context, cfg config.Config, spa fs.FS, version string, log *slog.Logger) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	st, err := store.Open(cfg.Path("remote-agent.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	token, err := config.ReadSecret(cfg.GitHubTokenFile)
	if err != nil {
		return err
	}
	client := github.New(cfg.GitHubAPIURL, token)
	client.GraphQLURL = cfg.GitHubGraphQLURL
	gh := &GitHub{C: client, G: &github.Git{BaseURL: cfg.GitURL, Token: token}}

	sk := skills.New(cfg.Path("skills"), cfg.Path("skills-snapshots"), cfg.SkillsSeedDir)
	if err := sk.Init(ctx); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	bench := skills.NewBench(sk, cfg.ClaudeBin, cfg.Path("benchmarks"), func(ctx context.Context) ([]string, error) {
		return benchEnv(ctx, st)
	})

	subject := cfg.PublicURL
	if subject == "" {
		subject = "https://github.com/s1n7ax/remote-agent"
	}
	notifier, err := NewNotifier(ctx, st, subject, log)
	if err != nil {
		return err
	}

	runners := map[domain.AgentKind]agent.Runner{
		domain.AgentClaude: &claude.Runner{Bin: cfg.ClaudeBin},
		domain.AgentPi:     &pi.Runner{Bin: cfg.PiBin},
		domain.AgentFake:   &fake.Runner{},
	}
	if cfg.FakeAgents {
		runners[domain.AgentClaude] = runners[domain.AgentFake]
		runners[domain.AgentPi] = runners[domain.AgentFake]
	}

	host := &Host{St: st, C: client}
	mcp := mcpserver.New(host)
	opts := orchestrator.Options{
		Config: cfg, Store: st, GitHub: gh, Runners: runners, Notifier: notifier, Skills: sk, Log: log,
		Register: func(ri orchestrator.RunInfo) (string, error) {
			// The agent may hand over any file in its project directory
			// (screenshots can land outside the ticket checkout).
			return mcp.Register(mcpserver.Run{ID: ri.RunID, TicketID: ri.TicketID, Stage: ri.Stage, Workdir: ri.Workdir, Root: ri.ProjectDir})
		},
		Revoke: mcp.Revoke,
	}
	if cfg.Sandbox {
		opts.Sandbox = func(projectDir, workdir string, ro []string) func(argv, env []string) ([]string, []string) {
			return sandbox.Config{
				Bwrap: cfg.BwrapBin, Project: projectDir, Workdir: workdir,
				ReadOnly: append(append([]string{}, cfg.SandboxPaths...), ro...),
			}.Wrap()
		}
	}
	orch := orchestrator.New(opts)
	host.O = orch

	srv := &api.Server{
		Orch: orch, Store: st, GitHub: gh, Skills: sk, Bench: bench, Push: notifier, SPA: spa, Log: log, Version: version,
		OpenRouterConfigured: func() bool {
			k, err := config.ReadSecret(cfg.OpenRouterKeyFile)
			return err == nil && k != ""
		},
		MaxParallel: func() int { return cfg.MaxParallelRuns },
	}

	servers := []*http.Server{
		{Addr: cfg.Listen, Handler: srv.Handler()},
		{Addr: cfg.MCPListen, Handler: mcp},
		{Addr: cfg.PreviewListen, Handler: http.HandlerFunc(orch.ServePreview)},
	}
	errc := make(chan error, len(servers)+1)
	for _, s := range servers {
		s.ReadHeaderTimeout = 10 * time.Second
		log.Info("listening", "addr", s.Addr)
		go func() {
			if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := orch.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			errc <- err
		}
	}()

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	cancel()
	<-done
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
	return err
}

// benchEnv gives a skill benchmark the first usable Claude account.
func benchEnv(ctx context.Context, st *store.Store) ([]string, error) {
	accounts, err := st.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, a := range accounts {
		if a.ExhaustedTill != nil && a.ExhaustedTill.After(now) {
			continue
		}
		tok, err := st.AccountToken(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		return []string{"PATH=" + os.Getenv("PATH"), "CLAUDE_CODE_OAUTH_TOKEN=" + tok}, nil
	}
	return nil, errors.New("no Claude account is available; add one or wait for a usage reset")
}
