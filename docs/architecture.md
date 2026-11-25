# Architecture

remote-agent is one Go binary (`remote-agent`) with an embedded Svelte SPA and a SQLite database. It drives coding agents (the official `claude` CLI and the `pi` coding agent) inside bubblewrap sandboxes, one run per ticket stage, and talks to GitHub on their behalf.

```
 phone ──tailnet/HTTPS──> tailscale serve ──> 127.0.0.1:7700  remote-agent
                                                  │  HTTP API + SSE + SPA
                                                  │  orchestrator (queue, accounts, schedules, CI poller)
                                                  │  app MCP server (127.0.0.1:7701, per-run bearer token)
                                                  └─ bwrap ─> claude / pi  (one per run)
```

## Packages

| Package                 | Owns                                                                                  |
| ----------------------- | ------------------------------------------------------------------------------------- |
| `cmd/remote-agent`      | flags, config file, wiring, `serve` and `healthcheck` subcommands                     |
| `internal/config`       | the JSON config file (see Config)                                                     |
| `internal/store`        | SQLite schema, migrations, typed queries                                              |
| `internal/domain`       | shared types: Ticket, Stage, Run, QuestionTree, … (no I/O)                            |
| `internal/questions`    | question-tree validation, visibility (`showIf`) evaluation, completeness              |
| `internal/github`       | REST client (issues, PRs, checks, merge, comments, push via git over HTTPS)           |
| `internal/sandbox`      | bubblewrap command builder                                                            |
| `internal/agent`        | `Runner` interface; `claude`, `pi` and `fake` implementations                         |
| `internal/orchestrator` | ticket state machine, run queue, concurrency + browser semaphore, accounts, spend, CI |
| `internal/mcpserver`    | the app MCP server exposing stage tools to runs                                       |
| `internal/skills`       | skills git repo, edit/commit, benchmark runner                                        |
| `internal/push`         | Web Push (VAPID) delivery and per-event preferences                                   |
| `internal/api`          | HTTP JSON API, SSE event stream, static SPA                                           |
| `web/`                  | Svelte 5 SPA (mobile-first PWA, service worker for push)                              |
| `skills/`               | default skills and agent definitions, refactored for the asynchronous flow            |
| `nix/`                  | package, NixOS module, VM test, DigitalOcean image, example host                      |
| `e2e/`                  | Playwright smoke test against the real binary with fake GitHub and fake agents        |

## Ticket lifecycle

A ticket's `status` is its board column; its `stage` says which `/ship` step runs next.

| Status        | Meaning                                                                    |
| ------------- | -------------------------------------------------------------------------- |
| `backlog`     | created, not started                                                       |
| `needs_input` | a question round (or prototype pick, or spend-cap question) is open        |
| `ready`       | queued for the next stage's run                                            |
| `working`     | a run is active                                                            |
| `waiting`     | waiting for a usage-limit reset (`waitUntil`) or for CI                    |
| `in_review`   | PR green and out of draft, proof attached; waiting for the user to merge   |
| `done`        | PR merged (or job finished)                                                |
| `failed`      | a run failed after retries; the card shows why and offers retry            |

Stages, in order (a stage may post an extra round, which sends the ticket to `needs_input` and returns to the same stage once answered):

1. `explore` — read the repo and the idea; post round 1 (requirement questions, full branching tree).
2. `design` — write the requirement (incl. glossary/ADR updates on the goal branch), explore the design; post round 2 (requirement confirmation + design tree). With prototypes requested: build 2–3 prototype branches, start previews, submit prototypes; the pick is a question in round 2.
3. `build` — implement, validate, review (subagent), fix, simplify (subagent), push, open draft PR.
4. `ci` — no agent: the app polls CI. Green → `verify`. Red/conflict → `fix`.
5. `fix` — fix CI failure or merge conflict, push; back to `ci` (max 2 rounds, then `failed`).
6. `verify` — final validation + `/verify`; submit proof; app marks PR ready, waits for CI once more; → `in_review`.
7. merged (polled) → `done`.

Starting: `backlog` → `ready(explore)` by the Start button or a ticket schedule (`startAt`). Answering the last visible question of a round moves `needs_input` → `ready` with no click.

Jobs (scheduled per project, cron) create a ticket of `kind: job` that goes straight to `ready(job)`, runs its prompt once, and ends `done`.

## Question tree

```jsonc
{
  "round": 1,
  "title": "Requirement",
  "intro": "markdown shown above the questions",
  "questions": [
    {
      "id": "q1",
      "kind": "single",            // single | multi | text | confirm
      "title": "Which droplet size must the app fit on?",
      "context": "markdown: why it matters",
      "options": [
        { "id": "a", "label": "4 GB", "description": "…", "recommended": true,
          "preview": { "markdown": "…", "image": "artifact id" } }
      ],
      "allowOther": true,          // free-text answer allowed (default true for single/multi)
      "showIf": { "question": "q0", "anyOf": ["b"] }   // optional; also "all": [cond…], "any": [cond…]
    }
  ]
}
```

An answer is `{ "optionIds": [...], "text": "..." }`. A question is visible when its `showIf` holds against current answers; hidden questions' answers are ignored. The round is complete when every visible question has an answer (a `confirm` needs `optionIds: ["yes"]` or a text correction).

## HTTP API (JSON, under `/api`)

| Method & path                                  | Purpose                                               |
| ---------------------------------------------- | ----------------------------------------------------- |
| `GET /api/state`                               | bootstrap: projects, accounts summary, settings, models |
| `GET/POST /api/projects`, `PATCH/DELETE /api/projects/{id}` | projects (repo `owner/name`, defaults)    |
| `GET /api/projects/{id}/board`                 | tickets grouped by status                             |
| `POST /api/projects/{id}/tickets`              | create from `{ idea, agent, model, effort, prototypes }` |
| `GET/PATCH /api/tickets/{id}`                  | ticket detail; patch agent/model/effort/startAt       |
| `POST /api/tickets/{id}/start`                 | backlog → ready                                       |
| `PUT /api/tickets/{id}/rounds/{n}/answers/{qid}` | save one answer; response says whether round completed |
| `POST /api/tickets/{id}/message`               | send a message to the active run                      |
| `POST /api/tickets/{id}/pause` / `resume` / `stop` / `retry` | run control                     |
| `GET /api/tickets/{id}/runs/{runId}/events`    | run log (paged)                                       |
| `GET /api/projects/{id}/github`                | issues, PRs, CI status                                |
| `POST /api/projects/{id}/pulls/{n}/merge`      | merge (user only; method from repo settings)          |
| `GET/POST/PATCH/DELETE /api/projects/{id}/schedules` | cron jobs                                       |
| `GET/POST/DELETE /api/accounts`                | Claude accounts (token), OpenRouter key, priority     |
| `GET/PUT /api/skills/...`, `POST /api/skills/benchmark`, `DELETE /api/skills/{name}` | skills editor + benchmarks |
| `GET/PUT /api/notifications`, `POST /api/push/subscribe` | push prefs + subscription                   |
| `GET /api/artifacts/{id}`                      | screenshots / reports                                 |
| `GET /api/events` (SSE)                        | live updates: `ticket`, `run_event`, `board`          |
| `GET /healthz`                                 | health check (DB reachable, orchestrator alive)       |
| `GET /api/idle`, `POST /api/drain`             | used by the auto-deploy timer                          |

## Workspaces and the sandbox boundary

The app runs as the same uid as the agents, outside their sandboxes, and holds the GitHub token and the database. It must stay safe when an agent is fully malicious inside its sandbox, so everything a sandbox can write is treated as hostile.

```
<dataDir>/projects/<id>/
  repo/            the app's clone of GitHub. No sandbox ever sees it; the only clone git runs in with the token.
  work/<ticket>/   the ticket's (or job's) own clone of repo/: objects copied, no hardlinks, no alternates;
                   origin = the GitHub URL without credentials
  home/            the sandbox $HOME: Claude/Pi config dirs, skill symlinks, mcp.json
```

- A run's (and a preview's) sandbox binds read-write only its own `work/<ticket>/` and `home/`; the skills snapshot is read-only. `repo/` and other tickets' clones are not visible.
- A ticket clone is built in a hidden `work/.<ticket>.new-*` directory (fetch from `repo/`, checkout) and renamed into place, so nothing agent-controlled exists while the app runs git in it. A path in `work/` that is not a real clone is moved aside (`<ticket>.stale-*`). Job clones are deleted when the job ends; ticket clones when the PR merges.
- Git the app runs never reads an agent's config outside a sandbox: `push_branch` fetches the branch into `repo/` with `upload-pack` running in a sandbox that sees only that clone (read-only), then pushes from `repo/`; refreshing a clone's `origin/*` from `repo/` runs `git fetch` in a sandbox that sees the clone (read-write) and `repo/` (read-only). The current branch is read from `.git/HEAD`, not with git. Every git the app runs also gets `core.hooksPath=/dev/null`, `core.fsmonitor=false`, no submodules (and git on local clones only the file transport), `GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null` (so no filter or credential driver is configured).
- Fetch from GitHub uses the project's stored `owner/name`, never a remote URL read from a clone.
- File operations the app does under `home/` or a clone (skill symlinks, config dirs, `mcp.json`, copying a transcript to another account on resume, reading an artifact) go through an `os.Root` at that directory, so a planted symlink cannot lead outside it (absolute symlinks are refused). Files are created exclusively after removing what was there; artifacts are opened once and read from that handle, and must lie in the run's clone or `home/`.
- Skill benchmarks (`claude plugin eval`, which uses tools) run in the same sandbox with only their temporary work dir writable.
- The workspace layout is versioned in the `workspace_layout` setting. Project directories from before version 2, when the whole project directory was writable from the sandbox, are moved aside once to `projects/<id>.legacy-<time>` and rebuilt from GitHub; unpushed work there is left for the operator.

## App MCP tools (per run)

`submit_questions(tree)`, `submit_requirement(markdown)`, `submit_decisions(markdown)`, `submit_prototypes(list)`, `start_preview(command, port)`, `submit_proof(report_markdown, files)`, `upload_artifact(path)`, `push_branch()`, `open_pr(title, body, draft)`, `update_pr(body)`, `ci_status()`, `ci_logs(check)`, `get_issue()`, `comment(body)`, `list_pulls()`, `request_wait_ci()`, `stage_done(summary)`. There is deliberately no merge tool.

## Config

`/var/lib/remote-agent/config.json` (generated by the NixOS module; secrets read from files named in it):

```jsonc
{
  "listen": "127.0.0.1:7700",
  "mcpListen": "127.0.0.1:7701",
  "dataDir": "/var/lib/remote-agent",
  "maxParallelRuns": 2,
  "githubTokenFile": "/run/secrets/github-token",
  "openrouterKeyFile": "/run/secrets/openrouter",
  "claudeBin": "/nix/store/…/bin/claude",
  "piBin": "/nix/store/…/bin/pi",
  "bwrapBin": "/nix/store/…/bin/bwrap",
  "sandboxPaths": ["/nix/store", "/run/current-system/sw"],
  "mcpServers": { "playwright": { "command": "…", "args": ["--headless"] } },
  "browserMcpServers": ["playwright"],
  "skillsSeedDir": "/nix/store/…/share/remote-agent/skills",
  "publicURL": "https://agent.tailnet.ts.net",
  "previewURL": "https://agent.tailnet.ts.net:8443",
  "previewListen": "127.0.0.1:7702",
  "sandboxEnv": "/nix/store/…-remote-agent-sandbox-env", // its bin/ is the sandbox PATH
  "sandboxPath": "",            // overrides sandboxEnv/bin
  "sandbox": true,              // false runs agents without bubblewrap (dev/tests)
  "ciPollSeconds": 60,
  "githubApiUrl": "https://api.github.com",          // fakes in e2e
  "githubGraphqlUrl": "https://api.github.com/graphql",
  "gitUrl": "https://github.com",
  "fakeAgents": false           // every run uses the scripted fake agent (e2e)
}
```
