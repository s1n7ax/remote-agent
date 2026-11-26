---
name: ship
description: The stage guide for the remote-agent board's /ship flow. Each ticket moves through explore → design → build → (CI, run by the app) → fix → verify, and every stage is a separate agent run started by the app; this skill says exactly what each run does, which app MCP tools report its outcome, and that it never waits on anything. Also covers scheduled job runs. Only use when a stage run's prompt sends you here; never trigger on your own.
disable-model-invocation: true
---

# Ship

This skill is the conductor, split into stages. The app — not you — drives the ticket: it starts one run per stage, waits for the user's answers, waits for CI, moves the ticket between statuses, marks the PR ready, and starts the next run. Each run does one stage, reports its outcome to the app through the app's MCP tools, and ends. Nothing in a run ever waits: the run ends with your turn. The user is pulled in only for decisions that are theirs, and only through question rounds on the ticket's card.

The run's first message names the stage and gives its context (see [STAGES.md](./STAGES.md) for the exact contract). Find your stage below and follow it in full.

Six rules hold the whole skill up. Everything below is detail.

1. **One stage per run, reported by its outcome tool.** Do exactly your stage's work, report it with its outcome tool (below), and end with `stage_done(summary)`. The outcome tool is what finishes the stage; `stage_done` adds the summary. Never start the next stage's work; a new run does it.
2. **The stage skills do the work.** This skill never re-implements a stage. Each stage reads its skill file and follows it in full, except where this skill overrides it.
3. **Never wait, never delegate.** Do every step in this session: no subagents, no background tasks, no ending a turn to wait for something. When your turn ends, the run ends, and whatever you were waiting for is lost. Work that takes time outside the run (CI, the user, the next stage) is the app's to wait for.
4. **The user is reached only through `submit_questions`.** No chat, no waiting for a reply. A run that needs the user submits a round and ends.
5. **GitHub only through the app.** You hold no GitHub credential. Pushing, PRs, CI, issues and comments go through the app's MCP tools; nothing else can reach GitHub, so never try.
6. **Never merge.** There is no merge tool, and there never will be. Only the user merges, from the app, after review. Never try another way.

## The pipeline

```
explore   run      read, build the requirement tree       → round 1 → user answers
design    run      requirement + glossary/ADRs, decisions  → round 2 → user answers
                   ↳ prototypes, only when asked
build     run      implement, validate, review, fix, simplify, push, draft PR
ci        app      waits until every check finished — no agent runs; red → fix, green → ready for testing
fix       run      red CI, a merge conflict or failed testing; fix, review, simplify, push → ci again (max 2)
verify    run      ready for testing: final validation + /verify, verdict → PASS: app marks the PR ready → user merges
                                                                          FAIL: app sends the ticket to fix
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
| `submit_proof(verdict, report_markdown, files)` | verify | attaches the `/verify` report and its screenshots to the ticket              |
| `push_branch()`                      | build, fix, verify, job | pushes the checked-out branch to `origin`; refuses the default branch    |
| `open_pr(title, body, draft)`        | build               | opens the ticket's PR from the goal branch                                   |
| `update_pr(body)`                    | verify              | replaces the PR description                                                  |
| `ci_status()`, `ci_logs(check)`      | fix, job            | reads the checks on the branch's head, and one check's log                   |
| `get_issue()`, `comment(body)`       | explore, any        | reads the ticket's issue; comments on it                                     |
| `list_pulls()`                       | job                 | lists the project's open PRs                                                 |
| `stage_done(summary)`                | every run, last     | the run's summary for the card; the only outcome of explore and job runs      |

Each stage's **outcome tool** reports that the stage is done:

| Stage     | Outcome tool                                                  |
| --------- | ------------------------------------------------------------- |
| explore   | `submit_questions`, or `stage_done` when there is nothing to ask |
| design    | `submit_questions` (round 2)                                  |
| build     | `open_pr` (or `push_branch` when the ticket already has a PR)  |
| fix       | `push_branch`                                                 |
| verify    | `submit_proof` (PASS or FAIL)                                 |
| job       | `stage_done`                                                  |
| any       | `submit_questions` with `purpose: "follow_up"`                |

A run that ends without its outcome is continued once, in the same conversation, with a `## Why the last run stopped` section in its prompt; pick up where it stopped. A second such end fails the ticket. Every run also has a time limit set per project; a run that reaches it is stopped and the ticket fails.

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

1. Follow `/implement`'s build run: apply round 2's answers (requirement confirmation first, then the decisions, ADRs, and the prototype pick), then Implement, Validate, Review, Fix, Simplify — all in this session. Resubmit the requirement and decisions when they changed.
2. Run the gate (below) until green.
3. With `critical: yes` in the prompt: run the implementation check (below) in this session and fix until it passes.
4. `push_branch()`.
5. `open_pr(title, body, draft: true)` — or, when the ticket already has a PR (a resumed build), `update_pr(body)`. The title is the requirement's title. The body carries the requirement's Goal and Acceptance criteria (reviewers cannot open `.ship/`), `Closes #<issue>`, the decisions the user made, and the checks that ran so far with their results. Never put `/verify` results in it; the app posts the proof.
6. `stage_done(summary)`: what was built, review findings fixed vs skipped, what simplify changed, validation results.

A mid-build surprise that only the user can decide: commit what is done (no push, no PR yet), submit a `follow_up` round, and `stage_done`.

## Stage: fix

The prompt says why: a red CI run (with the failing checks), a merge conflict with the base branch, or a failed verify (with its report). Its `## Requirement` section is the original requirement: every fix serves it.

- **Red CI.** Read `ci_status()` and `ci_logs(check)` for each failing check. Reproduce the failure locally with the same command CI runs, fix the cause — never the check — and rerun the command until it passes.
- **Merge conflict.** `git merge origin/<base>` on the goal branch, resolve every conflict keeping both sides' intent (the requirement decides when they clash), and commit the merge. Never rebase or force-push a pushed branch.
- **Failed verify.** Read the report's problems in the prompt, reproduce each one, and fix its cause in the code — never the test.

When the fix changed code, follow `/implement`'s Review, Fix and Simplify steps on the fix's changes, in this session. Then run the gate until green, `push_branch()`, and `stage_done(summary)`: the cause, the fix, the review findings fixed vs skipped, and the commands that now pass. The app waits for CI again. A failure you cannot fix without the user (a missing secret, a flaky external service, a requirement that cannot pass CI): submit a `follow_up` round instead of guessing, and end the run.

## Stage: verify

The ticket is Ready for testing: CI is green on the PR head. This run is the checker. You never saw how the work was done; the requirement in the prompt is the source of truth, and nothing beyond what the documents say. You report; you never fix: never edit, commit or push project code in a verify run, and write only reports, screenshots and throwaway state. Never mark an item PASS without evidence you gathered in this run (for `/verify`, a screenshot you took and looked at).

1. **Final validation** — in this session (below). Write its report.
2. **`/verify`** — in this session: read `<skills>/verify/SKILL.md` and follow it in full. Its report and screenshots go under `.ship/<goal>/verify/`.
3. **Proof.** Render the `/verify` report to markdown with its `report.py` (the verify skill says how), then `submit_proof(verdict, report_markdown, files)` with the report and every screenshot it references. The verdict is FAIL when either check failed; the final validation's problems go into the markdown too. This is the stage's outcome.
4. **PR description.** On PASS only, `update_pr(body)`: the build's body, plus every check's verdict, and every NOT RUN item of the final validation and `/verify`.
5. `stage_done(summary)`: the verdict of every check, NOT RUN items, the report's result line.

The app takes it from there: PASS → it marks the PR ready, waits for CI once more, and moves the ticket to In review. FAIL → it sends the ticket to a fix run with the report. NOT RUN items do not block; they are listed.

## Stage: job

The prompt holds the job's instructions, written by the user; follow them once. The checkout is on the project's default branch. Jobs work across the project, so:

- Use `list_pulls()`, `ci_status()`, `get_issue()` and `comment(body)` to find and report work.
- To change a PR, check out its branch (already fetched), make the change, run the gate, commit, and `push_branch()`. Return to the default branch afterwards. Never commit to the default branch; `push_branch()` refuses it.
- A job never submits questions; a decision only the user can make goes into the summary, which lands on the job's card.
- End with `stage_done(summary)`: what you did, per PR or issue, and what you left for the user.

## The gate — after every code change

Run `/implement`'s Validate step as the gate in build, fix and job runs, every time code changed, before a check or a push:

- `git status --porcelain` is empty on the branch.
- Every mechanical check passes: the formatter in check mode, linters, type checks, unit tests and the build, found as Validate says. Run each with its output appended to `.ship/<goal>/verification/gate.log` and read its exit code; read the log only for a command that failed, so passing output stays out of the run's context.

On a failure, fix it here and run the gate again. Gate rounds count against the Retries limit of the step that triggered them. No check runs and nothing is pushed while the gate is red.

## Checks

Every check runs in this session, as its own step: reread the requirement and the decisions file, then judge the code against them as a stranger would, not against what you meant to build. Never dispatch a subagent or a background task for a check.

| Check            | Stage              | Writes                      |
| ---------------- | ------------------ | --------------------------- |
| impl check       | build (`critical`) | report only                 |
| final validation | verify             | report only                 |
| verify           | verify             | report and screenshots only |

Every check writes its report to `.ship/<goal>/verification/<n>-<check>.md` (`<check>` is `impl-check` or `final-validation`), where `<n>` is one higher than the highest number already in that directory. Each run of a check gets its own file; earlier files are never edited.

```markdown
# <Check> <n> — <goal>

- **Date:** <date>
- **Verdict:** PASS | FAIL
- **Checked by:** <stage> run

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

Read the requirement, the decisions file, and the full diff from the base branch. For every item under Requirements, Acceptance criteria and Constraints, point to the code that delivers it. Flag items that are missing or only partly done, behavior that contradicts an item or a recorded decision, and changes that serve no item or touch anything under Out of scope. Read the code; running it is the final validation's job.

### Final validation — always

Prove the change works by running it, not by reading it. On the goal branch, run every acceptance criterion in the requirement as an observable check, plus the project's formatter, linters, type checks, tests and builds. Record each command and the part of its output that proves the result. Anything that cannot be run here (needs hardware, a reboot, a deploy, a credential) is reported as NOT RUN with the reason; it is never assumed to pass.

### Verify — always

Follow the verify skill in full, with every answer the user has given for this ticket that bears on running or judging the app. Its report and screenshots go under `.ship/<goal>/verify/`, not `verification/`.

When it needs something only the user can answer (how to run the app, a test account, what correct looks like), do not guess: submit the questions as a `follow_up` round (`questions` skill), then `stage_done`; the verify stage runs again with the answers. Question rounds do not count as retries.

## Retries

The impl check gets at most 2 fix-and-recheck rounds in its build run; the app gives a ticket at most 2 fix runs. Still failing after that → submit a `follow_up` round showing the latest report's problems in the `context` and asking how to continue (the options are the user's: a hint to try, accept it as it stands with the failures listed in the PR, or stop the ticket), then `stage_done`. The app also stops starting fix runs after 2 rounds and marks the ticket failed. Never weaken a check, edit a report, or drop a requirement item to get a PASS.

## Never

- Never run unless a stage run's prompt sent you here.
- Never do another stage's work, or end a run without its outcome tool and `stage_done`.
- Never dispatch a subagent or a background task, or end a turn to wait for anything.
- Never merge, try to merge, or ask for a way to merge.
- Never reach GitHub except through the app's tools: no `git push`, no CLI, no API calls, no tokens.
- Never push the default branch, rebase a pushed branch, or force-push.
- Never poll or wait for CI; the app does.
- Never ask the user in the chat; questions go to the card.
- Never skip the final validation or `/verify`.
- Never ask whether to stop after prototypes; the user's pick ships.
- Never run a check or push while the gate is red.
- Never fix code in a verify run; report it and let the app send it to fix.
- Never open the PR before the build's checks pass, or mark it ready yourself; the app does after verify.
- Never claim a check passed that did not run.
- Never edit or delete an earlier verification report.
