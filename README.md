# remote-agent

A lightweight, self-hosted, mobile-first board for coding agents. You file an idea as a ticket. Agents (Claude Code on your Pro/Max subscriptions, or Pi through OpenRouter) work it through the `/ship` flow:

1. Explore the idea.
2. Ask you every question at once, as a branching tree on the ticket.
3. Design, build and test.
4. Open a pull request.
5. Wait for CI, fix it if it's red, and attach proof with screenshots.

You answer from your phone, watch runs live, and merge when you are satisfied. **Agents can never merge.**

## Features

- **Projects and kanban:** each project has a board (backlog → needs input → ready → working → waiting → in review → done). Issues, PRs and CI sit in one view, with a merge button.
- **Async `/ship`:**
  - Answers to a question round save as you go; the round finishes, and the ticket advances, when you tap Submit.
  - 2–3 agents run in parallel.
  - Cron schedules create jobs.
  - Prototype picking comes with live previews.
- **Claude accounts:**
  - Add several subscription accounts.
  - On a usage limit, the run moves to the next account, or waits for the reset.
  - Runs use only the official `claude` CLI ([ADR 0001](docs/adr/0001-drive-the-official-claude-cli.md)).
- **Pi via OpenRouter:**
  - Pick the model and effort with one tap.
  - Per-ticket and daily spend caps.
- **Live runs:** stream the log, send a message, pause, resume, stop.
- **Skills:**
  - A git repo seeded from [`skills/`](skills/).
  - Edit skills in the app.
  - Run with/without benchmarks via `claude plugin eval`.
- **MCP servers:**
  - Configured in the NixOS module, including Playwright.
  - The app's own MCP server holds the GitHub token, so agents never do ([ADR 0002](docs/adr/0002-agents-hold-no-github-credential.md)).
- **Isolation:** one bubblewrap sandbox per run ([ADR 0003](docs/adr/0003-bubblewrap-per-run.md)).
- **Push notifications:** choose which events notify you.
- **Tailscale only:** never exposed publicly ([ADR 0006](docs/adr/0006-tailscale-only.md)).

## Deploy

NixOS on a 2 GB DigitalOcean droplet, from a prebuilt image, with idle-safe auto-deploy and rollback. See [docs/deploy.md](docs/deploy.md).

## Develop

```sh
nix develop
go test ./...
(cd web && npm ci && npm run build)
go run ./cmd/remote-agent serve --config dev.json
```

Read these too:

- [docs/architecture.md](docs/architecture.md): how the pieces fit.
- [GLOSSARY.md](GLOSSARY.md): the project's terms.
- [docs/adr/](docs/adr/): the decisions behind the design.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
