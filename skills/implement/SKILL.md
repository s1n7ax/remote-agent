---
name: implement
description: Build the requirement documented by /requirement, over two stage runs. The design run analyzes the code, proves unverified claims with throwaway spikes, drafts the decisions, builds prototypes when the ticket asks, and submits the round-2 design questions (approach forks, significant stretches, requirement gaps) bundled with the requirement confirmation. The build run applies the answers, implements, validates, runs /code-review in a subagent, fixes the findings, and runs /simplify in a subagent. Only use when a stage run's prompt or the ship skill sends you here; never trigger on your own.
disable-model-invocation: true
---

# Implement

This skill is execution. It starts from a requirement written by `/requirement`, works out how to build it, settles the decisions that need the user, builds it, reviews it, fixes the review findings, and simplifies the result.

The user is not in the session, so the work is split over two runs of the `/ship` flow:

| Stage run | This skill's part                                                                              | Ends with                                                     |
| --------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `design`  | Analyze, Decide (and Prototypes when asked) — after `/requirement` wrote the requirement        | `submit_decisions` (draft), `submit_questions` (round 2), `stage_done` |
| `build`   | Apply the answers, Implement, Validate, Review, Fix, Simplify                                   | `/ship` pushes and opens the draft PR                         |

Four rules hold the whole skill up. Everything below is detail.

1. **The requirement is the contract.** It is the definition of done. Never expand scope beyond it, however tempting the nearby work.
2. **Decide before you build.** Analyze the code and settle every decision that needs the user before writing a line of it. Code written before a fork is settled may be thrown away; that is why the build waits for round 2.
3. **Code first, questions last.** Ask only the three allowed question types, and only when the code, the docs, a spike, and your tools cannot settle it. Every question passes the `questions` skill and goes into the round-2 tree.
4. **Everything is written down.** Decisions go in the goal's decisions file (and to the card with `submit_decisions`), review findings and their fixes in its `code-review/` directory. Both are updated, never rewritten.

## The design run

### 1. Load the goal

The run's prompt names the goal and its directory, `.ship/<goal>/`. Read the requirement `/requirement` just wrote, `.ship/<goal>/requirement/requirement.md`, and the decisions file if one exists, `.ship/<goal>/decisions/decisions.md`. A decision already recorded is settled; never ask it again.

### 2. Analyze

Read everything the requirement touches and work out the viable ways to build it, and the decisions each one forces, before writing a single question. Every question in round 2 must already be informed.

#### When the code is out of reach

The run sees one project: its own checkout. Never build blind — do not guess another project's code, and do not replace reading it with questions.

- **The requirement needs a change in another repository.** That crosses an ownership boundary: ask it as a Type 2 stretch, recommending that the other project's part moves to **Out of scope** here and gets its own ticket on that project's board.
- **A dependency's source is needed.** Read it from the dependency cache, the vendored copy, or the Nix store; a library's docs through the docs MCP when one is configured.

#### When the code cannot prove it

The build hinges on a technical claim the code and docs cannot prove ("the library's webhook retries at least once"). Do not guess and do not ask — prove it yourself.

1. **Read deeper first.** The library's source, its tests, its issue tracker.
2. **Still unproven → spike it.** Make the smallest change that proves or disproves the claim — in a throwaway project under `$TMPDIR`, or on a `<goal>_spike_<claim>` branch (`<claim>` a short kebab-case name for the claim) in a separate git worktree under `$TMPDIR`, so the goal's checkout stays untouched. A spike is evidence, not implementation: never pushed, never a PR. Keep it local — no shared or production systems, real secrets, or paid resources.
3. **Build on facts.** Record the finding, and where the spike lives, in the decisions file. A disproven claim usually surfaces a Type 1 or Type 2 question — put it in the round.

Spike only claims that decide whether an approach is viable; claims that matter only inside one approach wait until it is chosen.

### 3. Decide

Map every decision the build forces, in dependency order, the big forks and stretches before any detail. Each one is either yours (see the gate test below) — decide it, and record it — or one of the three types below, which goes into the round-2 tree.

#### Type 1 — Fork in the road

Two or more clearly viable approaches exist, and every one of them satisfies the requirement. Ask which one the user prefers.

> **Where should request validation live?**
>
> All three spots satisfy the requirement; they differ in how much code moves and who owns the rule.
>
> 1. **In the existing shared schema package — recommended.** One definition, both projects import it. Simplest change, and the rule cannot drift between projects. Cost: a version bump of the shared package.
> 2. **In each project separately.** No cross-project dependency, but the rule now exists twice and will drift.
> 3. **In a new validation service.** Cleanest separation and scales to future projects, but a new deployable to run and monitor — the most moving parts by far.

A fork whose answer opens further decisions becomes a gate question; the decisions each option opens are its branches, behind `showIf`, pre-written now.

#### Type 2 — Significant stretch

The change costs something big or risky. **Always ask**, even when only one way exists — the real question is "is this stretch acceptable?" The triggers:

- The existing architecture cannot deliver the requirement: a breaking change, a new dependency, a new service or project, a schema migration, removing existing behavior.
- The security or permission surface grows: new secrets, broader permissions, a new exposed endpoint, handling sensitive data for the first time.
- The change crosses an ownership boundary: it touches a project owned by someone else or shared with other efforts.

The user may opt out. Write the opt-out option so it names what moves into **Out of scope** and why (e.g. "full-text search — rejected, new dependency was too big a stretch"); the build run moves it there and builds around what remains.

> **The search requirement needs a real index, and the current in-memory store cannot provide one. The only viable path adds a new dependency (a search engine) and a new service in docker-compose. Is that stretch acceptable?**
>
> 1. **Yes, add it — recommended.** The requirement explicitly demands typo-tolerant search; no in-process trick delivers that.
> 2. **No — cut search back to exact filtering on existing fields.** "Typo-tolerant search" moves to out of scope, with the reason: new dependency rejected.

#### Type 3 — Requirement gap

The requirement is silent on something that forks the build — a decision no code exploration can settle. Ask it, framed as the consequence the user feels; the build run writes the answer into the requirement — under **Requirements** or **Constraints**, in the user's words.

If the gap unravels the requirement itself rather than a corner of it, do not patch it with design questions: submit a `follow_up` round of requirement questions (the `/requirement` explore rules apply) instead of round 2, and end the run. The design stage runs again with the answers.

#### Everything else is yours to decide

> Could a strong engineer, given this requirement and this codebase, decide this without the user's taste or risk appetite?

Yes → decide it yourself, without asking. No → it must fit one of the three types to be asked. If it fits none, decide it yourself. Record the decisions a reviewer would otherwise question.

#### One approach per ticket

A ticket ships one approach on one goal branch. When the user wants to compare directions before choosing, that is what prototypes are for (below). An answer that keeps several options ("both") is read in the build run as "you pick": take the recommendation and say so.

#### The decisions file

The decisions file lives next to the requirement: `.ship/<goal>/decisions/decisions.md`. Submit the same markdown with `submit_decisions(markdown)` so the card shows it; submitting again replaces it.

```markdown
# Decisions — <goal>

Implements: <path to the requirement>

- **<decision>** — <why>. Decided by: user | agent. Rejected: <alternatives> (<why rejected>).

## Pending — round 2

- **<question id>** — <what the answer decides>. Recommended: <option>.

## Spikes

- **<claim>** — proven | disproven. <what showed it>. Lives at: <path or branch>.

## Prototypes

Only when the ticket asked for them; drop the section otherwise.

- **<name>** — branch `<goal>_prototype_<name>`. <what sets it apart, in one line>.
```

In the design run it is a draft: your own decisions are final, the user's are under **Pending — round 2**. The build run moves each pending entry up as `Decided by: user` and drops the section.

### 4. Prototypes — only when the ticket asks

"Prototype" always means: show me a few, I pick the best, and the pick is what ships. It is never a request to build prototypes and stop; never ask whether to stop after them.

Run this after Analyze and Decide. Do not ask the Type 1 forks the prototypes differ on; the pick settles them. Ask every other question as usual.

1. **Build 2–3 prototypes**, or as many as the ticket named, never more than 3. Each one takes a genuinely different direction: a different approach at a fork, or a different design for UI work. Pick the directions yourself from the analysis.
2. **Each prototype runs.** It is a working, unpolished build of the requirement's core, enough to judge the direction — not the final code. Build each on its own `<goal>_prototype_<name>` branch (`<name>` short kebab-case), created from the goal branch, in its own worktree at `.ship/<goal>/prototypes/<name>` (`git worktree add -b <goal>_prototype_<name> .ship/<goal>/prototypes/<name>`), committed. The worktrees outlive the run, so the app can serve any of them as the preview while the user decides. Prototypes skip review, simplify and the checks, and are never pushed. The checkout itself stays on the goal branch.
3. **Capture the evidence.** For each: screenshots of anything visual (Playwright MCP against the prototype running locally), a command and its output for a CLI or service, the key part of the diff otherwise. Upload each screenshot with `upload_artifact(path)`; the returned id goes into the option's `preview.image`.
4. **Start one live preview.** The app serves one preview at a time. Start the recommended prototype with `start_preview(command, port)` — a command, run from the checkout, that serves it from its worktree (`cd .ship/<goal>/prototypes/<name> && <serve command>`), and the port it listens on — so the user can try it on their phone. Check the preview answers before submitting.
5. **Submit them** with `submit_prototypes(list)`: per prototype its name, branch, one-line description, artifact ids, and the start command and port, so the app can switch the preview to another one on request.
6. **Ask which one ships** in round 2: one `single` question, one option per prototype with its screenshot as `preview`, recommended first. This is the only question prototypes add. A pick that combines prototypes ("A, with B's layout") is a valid `allowOther` answer.

### 5. Submit round 2

Build one tree, following the `questions` skill (`purpose: "design"`, the round number from the prompt):

1. **First: the requirement confirmation.** A `confirm` question with the full requirement as its `context`, as `/requirement` wrote it. Nothing in it is summarized away.
2. **Then: the prototype pick**, when there are prototypes.
3. **Then: the Type 1, 2 and 3 questions**, gate questions before their branches, in dependency order. Each must still score 0.

The `intro` lists the decisions you made yourself, so the user can object to one. Submit the decisions draft (`submit_decisions`), then the tree (`submit_questions`), then end with `stage_done`.

With nothing to ask, the tree is the confirmation alone: the requirement is always confirmed before the build.

## The build run

### 1. Load the answers

Read the requirement, the decisions draft, and round 2's questions and answers from the prompt (plus any `follow_up` rounds since).

1. **Requirement confirmation.** "Yes" → it is confirmed. A correction → apply it to `requirement.md` and `submit_requirement` again; update the glossary and ADRs if it changes a term or a recorded decision (as `/requirement` says). Then check every design answer and every decision of yours against the corrected requirement. If the correction reopens a fork only the user can settle, submit a `follow_up` round and end the run; nothing gets built on a requirement in flux.
2. **Design answers.** Move each pending decision up as `Decided by: user`. A Type 2 opt-out moves its part into **Out of scope** with the reason; a Type 3 answer goes into **Requirements** or **Constraints**, in the user's words. Resubmit both documents.
3. **ADRs.** A user decision that is hard to reverse, surprising, and a real trade-off gets an ADR, as `/requirement` says; commit it as a `docs:` commit.
4. **Prototype pick.** Record it under `## Prototypes` with the user's reason if they gave one. Then `git checkout -B <goal-branch> <goal>_prototype_<pick>`, remove every prototype worktree (`git worktree remove .ship/<goal>/prototypes/<name>`), and delete the prototype branches. A combined pick starts from the closest prototype.

### 2. Implement

Build the requirement the way the decisions file says, on the goal branch the run is already checked out on. Commit per logical change, following the `git-commit` skill. Stage files by path.

When the code surprises you mid-build with a decision that fits one of the three question types, stop that line of work: commit what is done, submit a `follow_up` round with the question, and end the run; the build runs again with the answer. Commits already made stay. Anything else you decide yourself and record.

### 3. Validate

Before review, prove the implementation works:

- Prove every acceptance criterion in the requirement with an observable check — a command, a test, an output you can show.
- Run the project's formatter, linters, type checks, tests and builds — the same commands CI and the pre-commit hooks run. Find them in the CI config, pre-commit config, task runner (`Makefile`, `justfile`, `package.json` scripts, flake `checks`/`formatter`) and contributing docs; never guess a subset.
- Run the formatter in its check mode (`--check`, `--fail-on-change`) after applying it, and commit what it changed. The working tree must be clean when Validate ends.

Fix failures and rerun until green. Never move to review with failing checks, and never claim a check passed that was not run. Report each command with its exit code.

### 4. Review

Run the review as a **subagent**: a fresh context does not defend the code it wrote. Dispatch the `code-reviewer` agent (Claude Code's Agent tool; pi's `subagent` tool) with the goal branch's changes from the base branch as the target, at `medium` effort unless the ticket asks for another level. The subagent reviews; it never fixes. If the environment has no subagent capability, run the review in this session and say so, per the code-review skill's own fallback rule.

Write the findings to `.ship/<goal>/code-review/<n>-review.md`, where `<n>` is one higher than the highest existing review number (`1-review.md` for the first). Each review round gets its own file; earlier files are never edited by later rounds.

```markdown
# Review <n> — <goal>

- **Date:** <date>
- **Scope:** <branch or diff reviewed>
- **Effort:** <level>
- **Reviewer:** code-reviewer subagent (or: in-session fallback)

## Findings

### 1. <one-sentence summary>

- **Location:** `path/to/file:line`
- **Status:** CONFIRMED | PLAUSIBLE
- **Failure scenario:** <concrete input or state -> wrong behavior>
- **Fix:** <filled in during the fix pass>

### 2. ...
```

### 5. Fix

Fix every finding in this same run, most severe first. For each one, apply the fix and update its entry in the review file — never in a separate document:

- **Fixed:** what changed and where (`- **Fix:** Fixed — <what changed, file/symbol>`).
- **Skipped:** only with a reason — the finding is false, the fix would change required behavior, or a recorded decision forbids it (`- **Fix:** Skipped — <reason>`).

After all fixes, rerun Validate. A fix that breaks a check is not a fix.

### 6. Simplify

Run `/simplify` as a **subagent**, for the same reason as the review. Simplify edits files, so dispatch the `worker` agent (or a general-purpose agent where the environment has no `worker`), telling it to read the simplify skill (`~/.claude/skills/simplify/SKILL.md`, or `~/.agents/skills/simplify/SKILL.md` under pi) and follow it on this goal's changes, naming the goal so it finds the requirement and decisions. If the environment has no subagent capability, run it in this session and say so.

Review the subagent's changes, then rerun Validate. Simplify is quality-only; its changes need no entries in the review file.

Stop here: `/ship`'s build section runs the remaining checks, pushes the branch and opens the draft PR.

## Never

- Never run unless a stage run's prompt or `/ship` sent you here.
- Never start without the requirement.
- Never ask a question before analyzing the code.
- Never ask a question outside the three allowed types, except a `follow_up` that keeps the flow moving (a requirement that unravelled, a mid-build surprise).
- Never break a `questions` skill rule — the dumbness score, the option cap, the tree rules.
- Never write production code in the design run; only spikes and prototypes.
- Never build on a requirement whose round-2 confirmation has not come back "yes" or been applied.
- Never ask again what the decisions file already records.
- Never build more than 3 prototypes, or push one.
- Never skip the review, the fix pass, or the simplify pass.
- Never mark a finding fixed without the fix applied and validation rerun.
- Never edit or delete an earlier review file.
- Never claim tests, checks, or reviews ran that did not run.
- Never expand scope beyond the requirement, however tempting the nearby work.
