---
name: requirement
description: Turn a fuzzy, half-formed idea into a sharp, scoped requirement document over two stage runs — the explore run prepares the whole requirement question tree and submits it to the card, the design run writes the requirement from the answers and updates the repo's glossary and ADRs. Only use when a stage run's prompt or the ship skill sends you here; never trigger on your own.
disable-model-invocation: true
---

# Requirement

This skill is requirement analysis. The user arrives with a fuzzy idea — half a sentence, a feeling, a "we should probably…" — and the skill turns it into a sharp requirement with a clear scope, then documents it. It also keeps the project's language straight: terms the requirement settles go into the repo's `GLOSSARY.md`, and decisions a future reader would trip over go into `docs/adr/`.

The user is not in the session. The work is split over two runs of the `/ship` flow:

| Stage run | This skill's part                                                                                  | Ends with                                   |
| --------- | -------------------------------------------------------------------------------------------------- | ------------------------------------------- |
| `explore` | Read the repo, the idea and the project's language; build and submit the requirement question tree | `submit_questions` (round 1), `stage_done`  |
| `design`  | From the answers: write the requirement, update the glossary and ADRs on the goal branch, commit   | `submit_requirement`, then `/implement`'s design part |

Four rules hold the whole skill up. Everything below is detail.

1. **Ask about the requirement, never about implementation.** The user decides what it must do. How to build it is decided later, by `/implement`.
2. **Every question passes the `questions` skill.** It owns the dumbness score, the shape of one question, and the shape of the tree. This skill adds only what is requirement-specific.
3. **One round, the whole tree.** The explore run sweeps the whole requirement at once and submits it as one branching tree. Nothing is asked one at a time.
4. **Speak the project's language.** Read `GLOSSARY.md` and `docs/adr/` before writing a question; use their terms, and treat a clash with them as something to resolve, not to paper over.

## Requirement or implementation?

The test for every question:

> Would this answer still matter if we threw away every line of code and rebuilt it with different tools?

Yes → requirement, ask it. No → implementation, out of bounds for this skill.

| Question                                                                   | Verdict        |
| -------------------------------------------------------------------------- | -------------- |
| "How does the user start a recording — hotkey, command, or tray icon?"     | Requirement    |
| "OBS or ffmpeg?"                                                           | Implementation |
| "If the app crashes mid-recording, should the half-finished file survive?" | Requirement    |
| "SQLite or a JSON file for settings?"                                      | Implementation |
| "Where do recordings get saved, and can the user change it?"               | Requirement    |

When a tool choice leaks into the user's world, rewrite it as the consequence they feel. Not "ffmpeg or OBS?" but "Is it OK if the user has to install another program first, or must this work on its own?"

## The explore run

### 1. Read before asking

The first question must already be informed.

- **The idea** from the run's prompt, and the ticket's issue (`get_issue()`): title, body, comments.
- **The repo**: README, the docs, the code the idea touches. A question the code answers is not a question.
- **The project's language**: `GLOSSARY-MAP.md` (several contexts) or `GLOSSARY.md` (one), and every ADR under `docs/adr/`. Note each term the idea uses and whether the glossary defines it the same way, and each ADR the idea runs into.

When the issue body already follows the template below (it has an `## Acceptance criteria` section), it is the requirement draft: ask only what it leaves open that is the user's to decide. With nothing left, submit no questions and end the run (step 4).

### 2. Build the tree

Restate the idea in one or two sentences; that restatement opens the round's `intro`. When the idea is too fuzzy to restate — it has two genuinely different readings — the gate question of the whole tree is "Which of these do you mean?", one option per reading, and each reading's questions hang off it.

Then sweep **breadth-first**. Depth on a corner that later gets cut is wasted. Cover at least, in this order:

| Area             | The question it settles                                                          |
| ---------------- | -------------------------------------------------------------------------------- |
| **Who**          | Who uses it, and in which situation.                                             |
| **Done**         | What "done" looks like, and how anyone would observe it.                         |
| **Must**         | What it must do.                                                                 |
| **Must never**   | What it must never do.                                                           |
| **Failure**      | What happens when it fails, half-way or completely.                              |
| **Out of scope** | What is explicitly not part of it.                                               |

Each area yields zero or more candidate questions. Score every candidate; most will not survive, and the decision you make instead goes into the `intro` as an assumption the user can correct. Then shape the survivors into a tree per the `questions` skill: gate questions first, branches behind `showIf`, every branch pre-written.

### 3. Grill the language

Domain modeling happens in the same round, as questions, because the user is not there to be challenged live.

- **A clash with the glossary.** The idea uses a term the glossary defines differently: "Your glossary defines *cancellation* as the whole order; do you mean that here, or cancelling one line?" One question, the glossary's meaning first.
- **A fuzzy or overloaded term.** Propose the precise canonical term as the recommended option: "By *account*, do you mean the Customer or the User?"
- **An edge-case scenario.** Where the boundary between two concepts decides behavior, invent the concrete scenario that forces it ("an order is half shipped and the customer cancels — what happens to the shipped half?").
- **A clash with the code or an ADR.** The idea contradicts what the code does or an ADR decided: name both and ask which holds.

Each of these is still a question: it passes the score, or you decide it and note the assumption.

### 4. Submit and stop

Submit the tree with `submit_questions` (`purpose: "requirement"`, the round number from the prompt), then `stage_done` with a summary: the restatement, the number of questions, and the assumptions you made. Write nothing into the repo in this run.

With nothing that only the user can answer, submit no questions; `stage_done` says so, and the ticket moves straight to `design`.

## The design run — writing it down

The run's prompt carries round 1's questions and answers (and any `follow_up` rounds). Read them with the `questions` skill's After the answers rules.

### 1. Write the requirement

Write it in full, from the idea, the answers, and the assumptions the user did not correct, in the user's own words wherever they gave any. Save it as `.ship/<goal>/requirement/requirement.md` (the goal's directory, in the checkout; `.ship/` is never committed — create `.ship/.gitignore` containing `*` if it is missing), then submit the same markdown with `submit_requirement(markdown)`. Submitting again later replaces it.

```markdown
# <Title>

## Goal

<What "done" looks like, in one or two lines.>

## Requirements

- <requirement in the user's own words, one per line>

## Acceptance criteria

- <observable outcome that proves a requirement is met — something a person or a check can see or run, not an intention. At least one per requirement.>

## Out of scope

- <thing the user ruled out> — <why>

## Constraints

<non-negotiables the user stated: platforms, compatibility, deadlines. "None" if none.>

## Open questions

<only unknowns that depend on something outside this flow — a third party, a future event, a decision the user explicitly deferred. "None" if none.>
```

The user confirms it in round 2: `/implement`'s design part puts the full requirement at the top of that round as a `confirm` question. Until it is confirmed, it is a draft; nothing is built on it yet.

When an answer unravels the idea itself (the user's own words describe something different from every option), do not write a requirement on top of it: submit a `follow_up` round with the questions the new reading opens, and end the run.

### 2. Update the glossary

For every term the round resolved, update the glossary on the goal branch, following [GLOSSARY-FORMAT.md](./GLOSSARY-FORMAT.md):

- One `GLOSSARY.md` at the repo root, unless a `GLOSSARY-MAP.md` names several contexts; then the context's own `GLOSSARY.md`.
- Create it lazily: only when the first term is resolved.
- Opinionated and tight: one canonical term, one or two sentences of what it **is**, the losing synonyms under `_Avoid_`.
- Only terms specific to this project's domain. No implementation detail, no requirement text, no decisions; it is a glossary and nothing else.

### 3. Record ADRs — sparingly

Write an ADR at `docs/adr/NNNN-slug.md`, following [ADR-FORMAT.md](./ADR-FORMAT.md), only for a decision the round settled that is all three:

1. **Hard to reverse** — changing your mind later costs something real.
2. **Surprising without context** — a future reader would wonder "why on earth this way?"
3. **The result of a real trade-off** — there were genuine alternatives, and one was picked for a reason.

At the requirement level these are mostly scope and boundary rulings ("exports never include other projects' tickets") and constraints not visible in the code. If any of the three is missing, skip it. `NNNN` is one higher than the highest number in `docs/adr/`; create the directory lazily. Never rewrite an accepted ADR: supersede it with a new one and set the old one's status.

### 4. Commit

Commit the glossary and ADR changes on the goal branch, staged by path, as one `docs:` commit following the `git-commit` skill (e.g. `docs: define board export in the glossary`). Nothing else goes in that commit. Do not push; the build run pushes the branch.

Then carry on with `/implement`'s design part in the same run.

## Never

- Never run unless a stage run's prompt or `/ship` sent you here.
- Never ask an implementation question.
- Never ask a question that scores 3 or more on the `questions` skill's dumbness score.
- Never ask one question at a time, or ask in the chat; the whole tree goes to the card in one `submit_questions`.
- Never bundle multiple decisions into one question's options — each option must answer the question as asked, not add extra scope rulings on top.
- Never break a `questions` skill rule — the option cap, the escape hatch, the tree rules.
- Never write the requirement, the glossary or an ADR in the explore run.
- Never treat the requirement as confirmed before round 2's `confirm` comes back "yes".
- Never put implementation details, requirement text or decisions into `GLOSSARY.md`.
- Never write an ADR that is not hard to reverse, surprising, and a real trade-off.
- Never leave an open question in the document that could have been asked in the round.
