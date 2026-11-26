# Skills

The default skills and subagents every remote-agent run gets. They are copied from the owner's NixOS config and refactored for the board's asynchronous flow: the user is never in the session, questions go to the ticket's card as one branching tree, and every `/ship` stage is a separate run started by the app.

At install the app seeds its skills repo (`/var/lib/remote-agent/skills`) from this directory. Each run mounts that repo's HEAD read-only at `~/.claude/skills` (Claude Code) and `~/.agents/skills` (pi), and `agents/` as the run's subagents.

## The skills

| Skill            | What it does                                                                                                   | Invoked by                  |
| ---------------- | -------------------------------------------------------------------------------------------------------------- | --------------------------- |
| `ship`           | The stage guide: what each stage run does, which app MCP tool reports its outcome, and that a run never waits on anything. [`ship/STAGES.md`](./ship/STAGES.md) is the contract the orchestrator follows when it starts a run. | the run's first message     |
| `requirement`    | Explore: builds the whole requirement question tree. Design: writes the requirement from the answers, updates `GLOSSARY.md` and `docs/adr/` in the target repo. | `ship` (explore, design)    |
| `implement`      | Design: analyzes, spikes, drafts decisions, builds prototypes, submits the round-2 design questions. Build: implements, validates, reviews, fixes, simplifies. | `ship` (design, build)      |
| `verify`         | Acceptance tests as the end user, before and after the change, with screenshots; renders the proof the verify stage submits. | `ship` (verify), in the verify run |
| `questions`      | The dumbness score, the shape of one question, and the rules for building and submitting a question tree.     | every skill that asks       |
| `code-review`    | Correctness review of a diff at an effort level.                                                               | `implement`, in the build or fix run |
| `simplify`       | Quality-only cleanup aligned with the requirement and decisions.                                              | `implement`, in the build or fix run |
| `error-handling` | Named error types, expected vs unexpected, documented errors.                                                 | the model, while coding     |
| `git-commit`     | Conventional commits; never edits code while committing.                                                       | the model, when committing  |
| `todo-comments`  | Every TODO is a doc comment backed by an issue.                                                                | the model, when writing a TODO |

`ship`, `requirement`, `implement` and `verify` set `disable-model-invocation: true`: a run reads them because its prompt or another skill says so, never on the model's own initiative. The other skills trigger from their `description`.

### Subagents (`agents/`)

| Agent               | Role                                                        | Tools                         |
| ------------------- | ----------------------------------------------------------- | ----------------------------- |
| `code-reviewer`     | runs `code-review`; read-only                               | read, grep, find, ls, bash    |
| `worker`            | runs `simplify`, or any self-contained task                 | all                           |
| `verifier`          | runs `ship`'s implementation check and final validation    | read, grep, find, ls, bash, write |
| `acceptance-tester` | runs `verify`                                               | all (needs Playwright MCP)    |

`tools:` uses pi's tool names. For Claude Code the app rewrites that line through [`agents/claude-tool-names.json`](./agents/claude-tool-names.json) (the same mapping the NixOS config uses), so the harness, not the prompt, keeps the reviewer and verifier from editing. Subagents never call the app's MCP tools; only the stage run does.

Stage runs never dispatch these agents: a run ends with its turn, so it does every step in its own session, and the app starts Claude runs with subagents and background tasks turned off. The definitions stay for running the skills outside the app.

## Stages → skills

| Stage     | Run does                                                                                  | Skills it follows                                   | Ends with (besides `stage_done`)                     |
| --------- | ----------------------------------------------------------------------------------------- | --------------------------------------------------- | ---------------------------------------------------- |
| `explore` | read repo, idea, glossary, ADRs; build round 1                                            | `ship`, `requirement` (explore), `questions`        | `submit_questions` (requirement), or nothing to ask   |
| `design`  | requirement + glossary/ADR commit; analyze, decide, prototypes; build round 2             | `ship`, `requirement` (design), `implement` (design), `questions`, `git-commit` | `submit_requirement`, `submit_decisions`, `submit_questions` (design) |
| `build`   | apply answers; implement, validate, review, fix, simplify; push; draft PR                 | `ship`, `implement` (build), `code-review`, `simplify`, `git-commit` | `push_branch`, `open_pr`                     |
| `ci`      | no run: the app watches CI                                                                | —                                                   | —                                                    |
| `fix`     | fix red CI, a merge conflict or failed testing; review, simplify; push                   | `ship`, `implement` (review, fix, simplify), `git-commit` | `push_branch`                                  |
| `verify`  | ready for testing: final validation, `/verify`, verdict; never fixes                     | `ship`, `verify`                                    | `submit_proof`, `update_pr` (on PASS)               |
| `job`     | a scheduled job's prompt, once                                                            | `ship` (job)                                        | whatever the job needs                               |

Any stage may submit a `follow_up` round instead and end; the app runs the same stage again once it is answered.

## Adding or editing a skill

1. A skill is a directory `skills/<name>/` with a `SKILL.md`. Its front matter needs `name` (equal to the directory name) and `description` (what it does and when to use it; this is what the model triggers on). Add `disable-model-invocation: true` only for a skill that must run only when told to.
2. Keep the house style: a short paragraph on what the skill is, a few numbered rules that "hold the whole skill up", the detail in sections and tables, and a **Never** list at the end.
3. Remember the run's world: the user is never in the session (ask through the `questions` skill and `submit_questions`), GitHub is reachable only through the app's MCP tools, and there is no merge. Skill files live at `~/.claude/skills/<name>/` or `~/.agents/skills/<name>/`; name both when a skill points at a file.
4. Scripts go in `skills/<name>/scripts/`, reference files next to `SKILL.md` (linked as `./FILE.md`).
5. Run `skills/check.sh`. It fails on missing or mismatched front matter, agents without front matter, references to removed tools (the old ship scripts, the GitHub CLI, clipboards), eval files that do not parse, and question-tree examples that break the tree rules.
6. Commit. In the app, the editor commits on save; the next run uses the new HEAD.

## Evals and benchmarks

A skill earns its place only if it changes behavior. Model-invoked skills carry an ablation suite in `skills/<name>/evals/`: each case runs once with the skill and once without, and the delta is what the skill is worth.

```
skills/<name>/evals/<case>/prompt.md       # front matter = execution config (runs, max_turns, allowed_tools), body = the prompt
skills/<name>/evals/<case>/graders/*.md    # front matter = grader config (type: regex | llm | tool_used, weight), body = criteria
```

Every case has a `skill-fired` grader (`type: tool_used`, `tool: Skill`, `input_match: <name>`, `arm: with-only`). It is not scored; it confirms the skill triggered in the with arm. If it fails, the case measures nothing: fix the `description` first. A skill with evals must not disable model invocation, or `skill-fired` can never pass (`check.sh` enforces this).

Run them with the same command the app's benchmark uses:

```sh
CLAUDE_CODE_WALNUT_SPIRE=1 claude plugin eval skills/<name> --ablation with-without --no-publish --threshold 0 --json skills/<name>/evals/results/latest.json
skills/run-evals.sh [name ...]    # the same, for every skill with evals (or the ones named)
```

Results land in `evals/results/` and are not committed.

### Keep or remove

Read the delta table (`CASE  WITH  W/OUT  Δ`):

| Result                              | Verdict                                                                                           |
| ----------------------------------- | ------------------------------------------------------------------------------------------------- |
| `Δ ≥ ~0.15`                         | The skill does real work. Keep it.                                                                |
| `Δ ≈ 0.00`, both arms high          | The base model already does this. Remove the skill.                                              |
| `Δ ≈ 0.00`, both arms low           | The skill is not landing, or the graders test something it never asked for. Read the failing grader before deciding. |
| `skill-fired` fails                 | The case measured nothing. Fix the description and rerun.                                         |

In the app, the benchmark view shows this table per skill, and one tap removes a skill (a commit deleting its directory). The stage skills (`ship`, `requirement`, `implement`, `verify`, `questions`) have no ablation suites: they are followed on instruction, so the end-to-end flow is their test.

Last measured on the source config (2026-09-16, claude-sonnet-5, 3 runs per arm): `todo-comments` +0.69, `error-handling` +0.42, `git-commit` +0.35 — all kept.

### Known limitation

The cases are text-only: the model is asked for finished code in its reply rather than given `Write`/`Bash` against a scaffolded repo. So `git-commit`'s "do not modify the codebase" rule is not observed directly; the cases cover conventional subjects and asking before splitting unrelated changes.

## Credits

`requirement/GLOSSARY-FORMAT.md` and `requirement/ADR-FORMAT.md`, and the glossary/ADR practice in `requirement`, are adapted from Matt Pocock's [domain-modeling and grill-with-docs skills](https://github.com/mattpocock/skills) (MIT; see `requirement/LICENSE-mattpocock`).
