# Stage-run contract

What the orchestrator sends and prepares for every agent run of the `/ship` flow, and what it can expect back. The skills (`ship`, `requirement`, `implement`, `verify`, `questions`) are written against this contract; change both together.

## Before every run

The orchestrator prepares the run's world before it sends the first message:

1. **Checkout.** The run's working directory is the project's checkout for this ticket, on the goal branch (job runs: on the default branch), with `git fetch origin` done just before the run so `origin/<base>` and every PR branch are current. The same checkout is kept for every stage of the ticket and is never re-cloned mid-ticket. The goal branch is created locally from `origin/<base>` before the ticket's first run (explore never commits to it; design is the first stage that does).
2. **Goal directory.** In the checkout, `.ship/.gitignore` contains `*`, and `.ship/<goal>/` holds, freshly written from the database:

   | Path                                     | Content                                                                          | Present from |
   | ---------------------------------------- | -------------------------------------------------------------------------------- | ------------ |
   | `.ship/<goal>/requirement/requirement.md` | latest `submit_requirement` markdown                                             | design       |
   | `.ship/<goal>/decisions/decisions.md`    | latest `submit_decisions` markdown                                               | design       |
   | `.ship/<goal>/rounds/<n>.json`           | `{ "tree": <submitted tree>, "answers": { "<qid>": {optionIds, text} } }`, visible questions only | after round n is answered |
   | `.ship/<goal>/prototypes.json`           | the latest `submit_prototypes` list, plus `"picked": "<name>"` once answered     | design (prototypes) |

   Everything else under `.ship/<goal>/` (`code-review/`, `verification/`, `verify/`, `prototypes/<name>/` worktrees) is written by runs and must survive between runs.
3. **Skills.** The skills repo HEAD is mounted read-only at `~/.claude/skills` (Claude) and `~/.agents/skills` (pi); `skills/agents/*.md` become the run's subagents (`~/.claude/agents` with `tools:` mapped through `skills/agents/claude-tool-names.json`; `~/.pi/agent/agents` as is).
4. **MCP.** The app MCP server (with this run's bearer token) plus the configured servers (Playwright for browser runs).

## The first user message

Every run gets one message built from this template. Lines in `[…]` are included only when they apply; `<…>` are variables (see the table below). Keep the wording; the skills key off the headings.

```markdown
You are running the `<stage>` stage of the /ship flow for one ticket on the remote-agent board.
Read <skillsDir>/ship/SKILL.md, then follow its "Stage: <stage>" section in full.
You cannot reach the user except through the app's MCP tools. End the run with stage_done.

## Run

- ticket: <ticketId> — <ticketTitle>
- stage: <stage>
- project: <owner>/<repo>
- base branch: <baseBranch>
- goal: <goal>
- goal branch: <goalBranch>
- goal directory: .ship/<goal>/
- issue: #<issueNumber> <issueURL>
- pull request: #<prNumber> <prURL>        [or: none]
- next round: <nextRound>
- prototypes: no                            [or: yes, <count|2–3>]
- critical: no                              [or: yes]
- review effort: medium                     [or the ticket's level]
[- resumed: <reason: follow_up answered | retry after failure | account switch | user resume>]

[## Idea

<idea>]

[## Answers

Round <n> (<purpose>) — full tree and answers: .ship/<goal>/rounds/<n>.json

- <qid> — <question title>: <selected option labels, comma-separated> [— "<text>"]
- …]

[## Trigger

- kind: ci_failed | conflict
- head: <sha>
- failing checks: <name> (<conclusion>), …
- fix round: <1|2> of 2]

[## Job

<job name> (<cron>)

<job prompt, verbatim>]
```

### Sections per stage

| Stage     | Idea | Answers                                           | Trigger | Job | Notes                                                      |
| --------- | ---- | ------------------------------------------------- | ------- | --- | ---------------------------------------------------------- |
| `explore` | yes  | only when resumed after a `follow_up` round       | —       | —   | `next round` is 1 (or the next free number)                |
| `design`  | yes  | round 1, plus every `follow_up` round since       | —       | —   | `next round` is 2 (or the next free number)                |
| `build`   | —    | round 2, plus every `follow_up` round since       | —       | —   | `pull request` is set when a resumed build already opened it |
| `fix`     | —    | only `follow_up` rounds since the last build      | yes     | —   | `pull request` always set                                  |
| `verify`  | —    | only `follow_up` rounds raised by verify          | —       | —   | `pull request` always set                                  |
| `job`     | —    | —                                                 | —       | yes | `goal`, `goal branch`, `issue`, `pull request`, `next round`, `prototypes`, `critical` are omitted |

Answers list visible questions only, in tree order; a hidden question's answer is never sent.

### Variables

| Variable       | Source                                                                                         |
| -------------- | ---------------------------------------------------------------------------------------------- |
| `stage`        | ticket's current stage: `explore` `design` `build` `fix` `verify` `job`                        |
| `skillsDir`    | `~/.claude/skills` for Claude runs, `~/.agents/skills` for pi runs                             |
| `ticketId`, `ticketTitle` | ticket                                                                              |
| `owner/repo`, `baseBranch` | project (base = the repo's default branch unless the project overrides it)         |
| `goal`         | ticket slug: short kebab-case from the title, fixed at creation, unique per project            |
| `goalBranch`   | branch the ticket ships on (e.g. `ra/<ticketId>-<goal>`); fixed at creation                    |
| `issueNumber`, `issueURL` | the ticket's GitHub issue, created with the ticket                                 |
| `prNumber`, `prURL` | the PR `open_pr` created, once it exists                                                  |
| `nextRound`    | 1 + the highest round number on the ticket                                                     |
| `prototypes`   | ticket flag from creation (`{ prototypes }`), with the count if the user gave one              |
| `critical`     | ticket flag (default no): adds the implementation check in build                               |
| `idea`         | the user's text from ticket creation, verbatim                                                 |
| answers        | per round: the stored tree and answers                                                         |
| trigger        | the CI poller's result for the PR head: failing check runs, or `mergeable: false`              |
| job            | the schedule's name, cron and prompt                                                           |

## What each run hands back, and where the ticket goes

A run ends when it calls `stage_done(summary)` (the summary goes on the card and into the issue/PR summaries the app posts). The ticket's next state depends on what the run submitted:

| Stage     | Expected calls before `stage_done`                                                      | Next                                                                                 |
| --------- | --------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `explore` | `submit_questions` (`purpose: requirement`), or none                                     | questions → `needs_input`, then `ready(design)`; none → `ready(design)`              |
| `design`  | `submit_requirement`, `submit_decisions`, `submit_questions` (`purpose: design`); with prototypes also `upload_artifact`, `submit_prototypes`, `start_preview` | `needs_input`, then `ready(build)` |
| `build`   | `push_branch`, `open_pr(draft: true)` (or `update_pr` when resumed)                     | `waiting` (CI, stage `ci`)                                                           |
| `fix`     | `push_branch`                                                                           | `waiting` (CI); after 2 fix rounds still red → `failed`                              |
| `verify`  | `submit_proof`, `update_pr`                                                             | app marks the PR ready, waits for CI once more → `in_review`; red → `fix`            |
| `job`     | anything the job needs                                                                  | `done`                                                                               |

- **A `follow_up` round** (`submit_questions` with `purpose: follow_up`) from any stage → `needs_input`, then `ready(<same stage>)` with `resumed: follow_up answered`.
- **Missing expected calls** (e.g. a build that ends without a PR, with no `follow_up` round) → treat the run as failed: retry per the retry policy, then `failed`.
- **No `stage_done`** (crash, kill, usage limit) → the run did not finish: re-queue the same stage with `resumed: …`; the skills resume from the checkout's state.
- **`submit_questions` more than once in a run** → reject the second call with an error; the skills never do it.

## Contract points the app MCP must honour

The skills rely on these; they go beyond the tool list in `docs/architecture.md`.

1. **`purpose` on the question tree**: `"requirement" | "design" | "follow_up"`, top-level next to `round`. It decides the transition above. Default when absent: `follow_up`.
2. **`push_branch()` pushes the currently checked-out branch** to `origin` (no force) and refuses the default branch. Stage runs may push only the ticket's goal branch; job runs use it on PR branches, but never on a ticket's `ra/<id>-*` goal branch.
3. **`open_pr` opens from the goal branch into the base branch**; `update_pr`, `ci_status`, `ci_logs`, `get_issue` and `comment` target the ticket's PR or issue. Job runs have no ticket PR/issue, so for jobs these tools need an optional `number` argument (PR or issue number). `ci_status` in a job reads the checked-out branch's head.
4. **`start_preview(command, port)`** runs `command` from the checkout root in the ticket's sandbox, keeps it running after the run ends, and stops the previous preview. `submit_prototypes(list)` items carry `{ name, branch, description, artifacts: [ids], preview: { command, port } }` so the app can switch the preview when the user asks.
5. **`submit_proof(report_markdown, files)`**: `files` are paths relative to the checkout; image links in the markdown use the same relative paths (`.ship/<goal>/verify/<n>/<file>.png`), which the app rewrites to its artifact URLs.
6. **No merge tool**, and no tool that returns the GitHub token.

## Example — the first message of a design run

```markdown
You are running the `design` stage of the /ship flow for one ticket on the remote-agent board.
Read ~/.claude/skills/ship/SKILL.md, then follow its "Stage: design" section in full.
You cannot reach the user except through the app's MCP tools. End the run with stage_done.

## Run

- ticket: 42 — Export a board
- stage: design
- project: s1n7ax/remote-agent
- base branch: main
- goal: board-export
- goal branch: ra/42-board-export
- goal directory: .ship/board-export/
- issue: #17 https://github.com/s1n7ax/remote-agent/issues/17
- pull request: none
- next round: 2
- prototypes: yes, 2–3
- critical: no
- review effort: medium

## Idea

let me export a board

## Answers

Round 1 (requirement) — full tree and answers: .ship/board-export/rounds/1.json

- export-who — Who needs to export a board?: Only you, the owner
- export-trigger — Does an export happen only when you ask for it, or also on a schedule?: Only when I tap Export
- export-done-tickets — Should tickets in Done be in the export?: Yes, everything on the board — "but mark them as done"
```
