---
name: ship
description: The stage guide for the remote-agent board's /ship flow. Each ticket moves through explore → design → build → (CI, run by the app) → fix → verify, and every stage is a separate agent run started by the app; this skill says exactly what each run does, which app MCP tools it calls, and that it ends with stage_done. Also covers scheduled job runs. Only use when a stage run's prompt sends you here; never trigger on your own.
disable-model-invocation: true
---

# Ship

This skill is the conductor, split into stages. The app — not you — drives the ticket: it starts one run per stage, waits for the user's answers, waits for CI, and starts the next run. Each run does one stage, hands its results to the app through the app's MCP tools, and ends with `stage_done`. The user is pulled in only for decisions that are theirs, and only through question rounds on the ticket's card.

The run's first message names the stage and gives its context (see [STAGES.md](./STAGES.md) for the exact contract). Find your stage below and follow it in full.

Six rules hold the whole skill up. Everything below is detail.

1. **One stage per run, then `stage_done`.** Do exactly your stage's work, call its tools, and end with `stage_done(summary)`. Never start the next stage's work; a new run does it.
2. **The stage skills do the work.** This skill never re-implements a stage. Each stage — or the subagent running it — reads its skill file and follows it in full, except where this skill overrides it.
3. **Checks run in fresh subagents.** A checker never saw how the work was done. Its only inputs are the documents and the code, and the requirement is its source of truth. It reports; it never fixes.
4. **The user is reached only through `submit_questions`.** No chat, no waiting for a reply. A run that needs the user submits a round and ends.
5. **GitHub only through the app.** You hold no GitHub credential. Pushing, PRs, CI, issues and comments go through the app's MCP tools; nothing else can reach GitHub, so never try.
6. **Never merge.** There is no merge tool, and there never will be. Only the user merges, from the app, after review. Never try another way.

## The pipeline

```
explore   run      read, build the requirement tree       → round 1 → user answers
design    run      requirement + glossary/ADRs, decisions  → round 2 → user answers
                   ↳ prototypes, only when asked
build     run      implement, validate, review, fix, simplify, push, draft PR
ci        app      waits for CI — no agent runs
fix       run      only on red CI or a merge conflict; push → ci again (max 2)
verify    run      final validation + /verify, proof → app marks the PR ready → user merges
job       run      a scheduled job's prompt, once
```

Any stage may need the user again: it submits a `follow_up` round and ends, and the app starts the same stage again once the round is answered.

## The run's world

- **The checkout.** The run's working directory is the project's checkout, already on the goal branch, already fetched from `origin` (so `origin/<base>` is current). The app made it, owns it, and keeps it for every stage of this ticket. Never create another clone, and never switch the checkout to another branch except as a stage below says.
- **The goal's directory.** `.ship/<goal>/` in the checkout holds the ticket's working state. `.ship/` is never committed: create `.ship/.gitignore` containing `*` if it is missing. Before each run the app writes the latest documents into it — `requirement/requirement.md`, `decisions/decisions.md`, and each answered round as `rounds/<n>.json` — and keeps what earlier runs left there (`code-review/`, `verification/`, `verify/`, `prototypes/`). The app's copy is the source of truth: when you change a document, write the file and submit it again.
- **Skills.** `<skills>` is `~/.claude/skills` under Claude Code and `~/.agents/skills` under pi. The stage skills are not model-invocable, so do not call them through a skill tool: read `<skills>/<name>/SKILL.md` and follow it.
- **Messages.** The user may send a message into a live run. It is the user speaking: follow it, unless it breaks a rule in this skill's Never list; then say so in the summary.
- **Resuming.** A stage can run again — after a `follow_up` round, a retry, or an account switch. Read the checkout and `.ship/<goal>/` first; never redo a finished step the state shows (commits, review files, reports), and never re-ask what a round already answered.

## App MCP tools

| Tool                                 | Used by             | Does                                                                         |
| ------------------------------------ | ------------------- | ---------------------------------------------------------------------------- |
| `submit_questions(tree)`             | any                 | posts a question round to the card (`questions` skill); the ticket waits      |
| `submit_requirement(markdown)`       | design, build       | stores the requirement on the ticket; replaces the previous one              |
| `submit_decisions(markdown)`         | design, build       | stores the decisions file on the ticket; replaces the previous one           |
| `upload_artifact(path)`              | design, verify      | stores a file (a screenshot) and returns its artifact id                     |
| `submit_prototypes(list)`            | design              | posts the prototypes, with screenshots and preview commands, to the card     |
| `start_preview(command, port)`       | design              | serves one prototype on the tailnet preview URL (one at a time)              |
| `submit_proof(report_markdown, files)` | verify            | attaches the `/verify` report and its screenshots to the ticket              |
| `push_branch()`                      | build, fix, verify, job | pushes the checked-out branch to `origin`; refuses the default branch    |
| `open_pr(title, body, draft)`        | build               | opens the ticket's PR from the goal branch                                   |
| `update_pr(body)`                    | verify              | replaces the PR description                                                  |
| `ci_status()`, `ci_logs(check)`      | fix, job            | reads the checks on the branch's head, and one check's log                   |
| `get_issue()`, `comment(body)`       | explore, any        | reads the ticket's issue; comments on it                                     |
| `list_pulls()`                       | job                 | lists the project's open PRs                                                 |
| `stage_done(summary)`                | every run, last     | ends the stage; the summary goes on the card                                 |

No tool waits for CI. Never poll it: after `open_pr` or a push, end the run; the app watches CI and starts the next run.

## Stage: explore

Follow `/requirement`'s explore run.

1. Read the idea in the prompt and the issue (`get_issue()`), then the repo, `GLOSSARY.md` and `docs/adr/`.
2. Build the full requirement tree — breadth-first: who, done, must, must never, failure, out of scope — plus the language questions, shaped by the `questions` skill.
3. `submit_questions(tree)` with `purpose: "requirement"` and the prompt's round number — or nothing, when nothing is the user's to decide.
4. `stage_done(summary)`: the restatement, the question count, the assumptions you made.

Writes nothing to the repo.

## Stage: design

First `/requirement`'s design run, then `/implement`'s design run, in this one run.

1. Write the requirement from round 1's answers; save it and `submit_requirement(markdown)`.
2. Update `GLOSSARY.md` and add ADRs that qualify; commit them on the goal branch as one `docs:` commit. Do not push.
3. Analyze and Decide as `/implement` says; spike unproven claims.
4. With prototypes asked for: build them in their worktrees, screenshot them, `upload_artifact` each screenshot, `start_preview` the recommended one, `submit_prototypes(list)`.
5. `submit_decisions(markdown)` — the draft, with the user's decisions under Pending.
6. `submit_questions(tree)` with `purpose: "design"`: the requirement `confirm` first, then the prototype pick, then the design questions.
7. `stage_done(summary)`.

When round 1's answers unravel the idea, or a requirement gap unravels the requirement, submit a `follow_up` round of requirement questions instead of steps 2–6, and end the run.

## Stage: build

1. Follow `/implement`'s build run: apply round 2's answers (requirement confirmation first, then the decisions, ADRs, and the prototype pick), then Implement, Validate, Review (subagent), Fix, Simplify (subagent). Resubmit the requirement and decisions when they changed.
2. Run the gate (below) until green.
3. With `critical: yes` in the prompt: run the implementation check (below) and fix until it passes.
4. `push_branch()`.
5. `open_pr(title, body, draft: true)` — or, when the ticket already has a PR (a resumed build), `update_pr(body)`. The title is the requirement's title. The body carries the requirement's Goal and Acceptance criteria (reviewers cannot open `.ship/`), `Closes #<issue>`, the decisions the user made, and the checks that ran so far with their results. Never put `/verify` results in it; the app posts the proof.
6. `stage_done(summary)`: what was built, review findings fixed vs skipped, what simplify changed, validation results.

A mid-build surprise that only the user can decide: commit what is done (no push, no PR yet), submit a `follow_up` round, and `stage_done`.

## Stage: fix

The prompt says why: a red CI run (with the failing checks) or a merge conflict with the base branch.

- **Red CI.** Read `ci_status()` and `ci_logs(check)` for each failing check. Reproduce the failure locally with the same command CI runs, fix the cause — never the check — and rerun the command until it passes.
- **Merge conflict.** `git merge origin/<base>` on the goal branch, resolve every conflict keeping both sides' intent (the requirement decides when they clash), and commit the merge. Never rebase or force-push a pushed branch.

Then run the gate until green, `push_branch()`, and `stage_done(summary)`: the cause, the fix, and the commands that now pass. The app waits for CI again. A failure you cannot fix without the user (a missing secret, a flaky external service, a requirement that cannot pass CI): submit a `follow_up` round instead of guessing, and end the run.

## Stage: verify

1. **Final validation** — always, in a `verifier` subagent (below). On FAIL: fix it here, run the gate, `push_branch()`, run it again.
2. **`/verify`** — always, in an `acceptance-tester` subagent (below). On FAIL: fix it here, run the gate, `push_branch()`, run the final validation again (its PASS predates the fix), then `/verify` again.
3. **Proof.** Render the final `/verify` report to markdown with its `report.py` (the verify skill says how), then `submit_proof(report_markdown, files)` with the report and every screenshot it references.
4. **PR description.** `update_pr(body)`: the build's body, plus every check's verdict and how many rounds it took, and every NOT RUN item of the final validation and `/verify`.
5. `stage_done(summary)`: the verdict of every check, NOT RUN items, the report's result line.

The app then marks the PR ready, waits for CI once more, and moves the ticket to In review. NOT RUN items do not block; they are listed.

## Stage: job

The prompt holds the job's instructions, written by the user; follow them once. The checkout is on the project's default branch. Jobs work across the project, so:

- Use `list_pulls()`, `ci_status()`, `get_issue()` and `comment(body)` to find and report work.
- To change a PR, check out its branch (already fetched), make the change, run the gate, commit, and `push_branch()`. Return to the default branch afterwards. Never commit to the default branch; `push_branch()` refuses it.
- A job never submits questions; a decision only the user can make goes into the summary, which lands on the job's card.
- End with `stage_done(summary)`: what you did, per PR or issue, and what you left for the user.

## The gate — after every code change

Run `/implement`'s Validate step as the gate in build, fix, verify and job runs, every time code changed, before dispatching a check or pushing:

- `git status --porcelain` is empty on the branch.
- Every mechanical check passes: the formatter in check mode, linters, type checks, unit tests and the build, found as Validate says. Run each with its output appended to `.ship/<goal>/verification/gate.log` and read its exit code; read the log only for a command that failed, so passing output stays out of the run's context.

On a failure, fix it here and run the gate again. Gate rounds count against the Retries limit of the step that triggered them. No check is dispatched and nothing is pushed while the gate is red.

## Subagents

Dispatch each subagent as its named agent: Claude Code's Agent tool, or pi's `subagent` tool, with the agent's name. Where the environment does not have that agent, use a general-purpose agent given the agent's rules restated in the brief. Every brief names the goal, where the requirement and the decisions file live, the branch and the checkout's path, and where to write the report. If the environment has no subagent capability, run the check in this run, say so in the report, and say in the summary that the check was not independent.

| Subagent         | Agent               | Writes files?               | Stage        |
| ---------------- | ------------------- | --------------------------- | ------------ |
| code review      | `code-reviewer`     | no                          | build        |
| simplify         | `worker`            | yes                         | build        |
| impl check       | `verifier`          | report only                 | build (`critical`) |
| final validation | `verifier`          | report only                 | verify       |
| verify           | `acceptance-tester` | report and screenshots only | verify       |

Subagents never call the app's tools. Only this run submits, pushes, opens PRs and ends the stage.

Every check writes its report to `.ship/<goal>/verification/<n>-<check>.md` (`<check>` is `impl-check` or `final-validation`), where `<n>` is one higher than the highest number already in that directory. Each run of a check gets its own file; earlier files are never edited.

```markdown
# <Check> <n> — <goal>

- **Date:** <date>
- **Verdict:** PASS | FAIL
- **Checked by:** subagent (or: in-session fallback)

## Coverage

| Requirement item | Evidence                             | Result      |
| ---------------- | ------------------------------------ | ----------- |
| <item, verbatim> | <file:line / command and its output> | PASS / FAIL |

## Problems

### 1. <one-sentence summary>

- **Kind:** missing | contradicts | out of scope | broken
- **Detail:** <what is wrong and where>
```

The verdict is FAIL when any row fails or any problem is listed.

### Implementation check — `critical` only

Brief: read the requirement, the decisions file, and the full diff from the base branch. For every item under Requirements, Acceptance criteria and Constraints, point to the code that delivers it. Flag items that are missing or only partly done, behavior that contradicts an item or a recorded decision, and changes that serve no item or touch anything under Out of scope. Read the code; running it is the final validation's job.

### Final validation — always

Brief: prove the change works by running it, not by reading it. On the goal branch, run every acceptance criterion in the requirement as an observable check, plus the project's formatter, linters, type checks, tests and builds. Record each command and the part of its output that proves the result. Anything that cannot be run here (needs hardware, a reboot, a deploy, a credential) is reported as NOT RUN with the reason; it is never assumed to pass.

### Verify — always

Brief: the goal, where the requirement and the decisions file live, the branch, the path to the verify skill (`<skills>/verify/SKILL.md`), and every answer the user has given for this ticket that bears on running or judging the app. It follows the skill in full and writes its report and screenshots under `.ship/<goal>/verify/`, not `verification/`.

The subagent cannot reach the user. When it needs something only the user can answer (how to run the app, a test account, what correct looks like), it stops and returns the questions instead of guessing. Submit them as a `follow_up` round (`questions` skill), then `stage_done`; the verify stage runs again with the answers, and the next brief carries them. Hand a credential to the subagent as an environment variable in the brief, never in a file. Question rounds do not count as retries.

## Retries

Each check, and each fix run, gets at most 2 fix-and-recheck rounds. Still failing after that → submit a `follow_up` round showing the latest report's problems in the `context` and asking how to continue (the options are the user's: a hint to try, accept it as it stands with the failures listed in the PR, or stop the ticket), then `stage_done`. The app also stops starting fix runs after 2 rounds and marks the ticket failed. Never weaken a check, edit a report, or drop a requirement item to get a PASS.

## Never

- Never run unless a stage run's prompt sent you here.
- Never do another stage's work, or end a run without `stage_done`.
- Never merge, try to merge, or ask for a way to merge.
- Never reach GitHub except through the app's tools: no `git push`, no CLI, no API calls, no tokens.
- Never push the default branch, rebase a pushed branch, or force-push.
- Never poll or wait for CI; the app does.
- Never ask the user in the chat; questions go to the card.
- Never skip the final validation or `/verify`.
- Never ask whether to stop after prototypes; the user's pick ships.
- Never dispatch a check or push while the gate is red.
- Never let a checker fix what it checks, see how the work was made, or call the app's tools.
- Never open the PR before the build's checks pass, or mark it ready yourself; the app does after verify.
- Never claim a check passed that did not run.
- Never edit or delete an earlier verification report.
