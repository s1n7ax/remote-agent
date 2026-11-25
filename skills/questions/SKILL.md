---
name: questions
description: Decide whether a question is worth the user's attention before it is asked, and ask the ones that are — as one branching question tree, built up front and submitted to the board with the app's `submit_questions` tool. Use whenever you are about to ask the user anything — in any skill or stage run — or when a skill tells you to ask. Runs each candidate through the dumbness score, blocks questions you can answer yourself, and defines the shape of one question and of the whole tree.
---

# Questions

The user's attention is the scarcest resource in the run, and every question spends it — whether or not the answer was ever needed. This skill decides which questions reach the user, kills the rest, and defines how the survivors are asked.

The user is not in the session. Questions go onto the ticket's card as one form, the user answers whenever they like, and a **new** run picks up the answers. So there is no back-and-forth: you get one shot per round, and the round must hold every question that is the user's to answer now, with every branch already written.

Four rules hold the whole skill up. Everything below is detail.

1. **Ask only what is the user's to decide.** Facts you can fetch, defaults any competent engineer would pick, and choices that do not change the work are yours — decide them and move on.
2. **Score before you ask.** Run every candidate through the dumbness score. A high score means the question does not go to the user; it comes back to you to resolve.
3. **One decision per question.** Never bundle decisions; a topic that holds several becomes a gate question and its branches.
4. **One tree per round, fully pre-written.** Work out the whole decision tree before submitting anything: every branch the user might open is already in it, hidden behind `showIf` until an earlier answer makes it relevant.

## The dumbness score

Score every candidate before it reaches the user. Add the points for each flag it trips:

**Blockers — any one of these means the question does not reach the user:**

| Flag                   | Points | Meaning                                                                                      |
| ---------------------- | ------ | -------------------------------------------------------------------------------------------- |
| **Self-answerable**    | +3     | The code, the docs, a tool, or a spike can answer it.                                        |
| **Obvious default**    | +3     | One option is what any competent person would pick; the others are absurd or strictly worse. |
| **Already answered**   | +3     | The user said it, or it follows directly from what they said.                                |
| **No consequence**     | +3     | The answer changes nothing about what gets built or done.                                    |
| **Fact, not decision** | +3     | You are asking the user to supply something you should look up.                              |

**Softeners — cheap to fix, so sharpen before asking:**

| Flag                   | Points | Meaning                                                                             |
| ---------------------- | ------ | ----------------------------------------------------------------------------------- |
| **False dilemma**      | +1     | The options miss the real alternatives, or smuggle extra decisions into the choice. |
| **Speculative**        | +1     | The decision is not needed yet, or is outside the current scope.                    |
| **Cheaply reversible** | +1     | You can change the decision later at little cost.                                   |
| **Delegable**          | +1     | A subagent, a second read, or a short spike could settle it.                        |

**0 — ask it.** A real decision only the user can make.

**1–2 — sharpen first.** Fix the options, split the bundle, read the code, or spike it, then re-score. Ask only what reaches 0.

**3 or more — do not ask.** The question is not worth the user's attention. Resolve it yourself — decide with the obvious default, or go get the missing fact — then record what you chose and why, and carry on. Escalate only if you genuinely cannot proceed without the user, and then say plainly what blocks you.

The score gates the question, not the topic. A question can be weighty and still score 0; a question can sound reasonable and still score 3 because one option is nonsense. Score the question.

A branch question is scored as if its gate were already answered the way that shows it. Every branch must survive the score on its own.

### Ask yourself first

For any question that scores 3 or more, answer these to yourself, in order, before giving up on it:

1. **What would I do if the user said nothing?** If you have an answer, do that.
2. **Is there a default any competent person would pick?** Then that is the answer.
3. **Does the answer change a single line of the work?** If not, do not ask.
4. **Can I get the answer from the code, the docs, or a tool?** Then get it.
5. **Am I asking because I am unsure, or because it is genuinely theirs?** Being unsure is not a reason to ask.

Only a question that survives all five is the user's to answer.

### What a dumb question looks like

> The voice should run as a background service so it is always available, or only while Neovim is open?

Nobody would tie a home-automation voice to an open editor. The second option is absurd, so the "choice" is not a choice: it scores +3 for an obvious default and never reaches the user. The agent picks the background service, notes the assumption, and moves on.

## Whose decision is it?

Each workflow has its own territory, but this skill's rules apply to every question asked in any of them. One test decides whether a question is in bounds:

> If we threw away every line of code and rebuilt it with different tools, would this answer still matter?

Yes → the user's world. It may be asked, if it survives the score.
No → implementation. Yours to decide; never ask.

## The shape of one question

1. **One decision, answerable in a word.** The options are direct answers to that one question. A topic holding several decisions becomes a gate question with branches (see Building the tree).
2. **Explain first.** `title` is one plain sentence for the question itself. `context` holds enough to see why it matters and what depends on the answer — markdown, as long as it needs to be.
3. **At most 3 options, best first.** Think of every reasonable answer, then present only the top 3. For each: what it means in practice, why you would pick it, what it costs. The recommendation is first, carries `"recommended": true`, and its `description` gives the one-line reason.
4. **Keep the escape hatch.** `allowOther` (on by default for `single` and `multi`) lets the user answer in their own words. Never author a "None of these" or "Something else" option on top of it, and never use it to smuggle in a second decision.

### Kinds

| Kind      | Use it when                                                                        | Answer                                     |
| --------- | ---------------------------------------------------------------------------------- | ------------------------------------------ |
| `single`  | One of 2–3 options is the answer. The default.                                     | `optionIds: [<one id>]`, or `text`         |
| `multi`   | Several options can be true at once (which platforms, which roles). Never otherwise. | `optionIds: [<ids>]`, or `text`          |
| `text`    | The answer is the user's own words and no short list covers it (a name, a limit).  | `text`                                     |
| `confirm` | A yes-or-correct check of something you wrote: a summary, a restatement.           | `optionIds: ["yes"]`, or `text` correction |

A `confirm` has no `options`; the card supplies "Yes" and a correction box. Put the thing to confirm in its `context`, in full.

### Previews

Add `preview` to an option only when the options are artifacts to compare — a layout, a schema, a config, a prototype — never for a plain preference. `preview.markdown` holds a snippet; `preview.image` holds an artifact id from `upload_artifact(path)` (a screenshot).

## Building the tree

Map the decisions as a tree before writing any JSON: every decision branches into the decisions that hang off it. Then cut it with the dumbness score, and only then write it out.

1. **Gate first, branches after.** A question that decides whether others matter comes first. Each branch question carries a `showIf` on the gate answer that makes it relevant. Order the array so every question comes after the questions its `showIf` names.
2. **Pre-write every branch.** For each option of a gate, write the questions that option opens, now. The user never waits for a run to discover the next question an option leads to.
3. **Never ask what a branch makes moot.** If one answer to the gate makes a question pointless, that question's `showIf` lists only the answers that keep it relevant. A question visible on every path must matter on every path.
4. **One decision per question, still.** Branching is not a licence to bundle; a branch is one decision, like any other.
5. **Breadth before depth.** Cover the whole space at the top level before nesting deep in one corner. Two levels of nesting is usually enough; three is the limit.
6. **Keep the visible form short.** Whatever path the user takes, aim for at most about 10 visible questions. Hidden branches cost nothing; visible ones cost attention.
7. **Defer what you cannot pre-write.** A question whose options depend on the user's own words (a `text` answer, or an `allowOther` answer to a gate) cannot be written yet. Leave it for the next run, which may post a `follow_up` round if it still scores 0.
8. **Stable, meaningful ids.** Question ids and option ids are short kebab-case names (`export-trigger`, `scheduled`), not `q1`/`a`. The next run reads the answers by id.

### `showIf`

```jsonc
{ "question": "<id>", "anyOf": ["<option id>", ...] }   // true when that question's answer includes any of these options
{ "all": [<cond>, ...] }                                // every condition holds
{ "any": [<cond>, ...] }                                // at least one holds
```

The app ignores the answers of hidden questions, so a condition on a hidden question is false. Chaining on the direct parent is therefore enough: a grandchild disappears when its grandparent hides its parent. Use `all` only when a question depends on two separate answers.

A gate answered in the user's own words (`text`, no option) satisfies no `anyOf`, so every branch keyed on that gate stays hidden. That is intended: the next run reads the text and decides what, if anything, still needs asking.

## Submitting the round

Submit the whole tree once, with `submit_questions(tree)`, then end the run with `stage_done(summary)`. Never submit twice in one run, and never keep working on what the answers will decide.

```jsonc
{
  "round": <n>,                       // the round number the run's prompt gives you
  "purpose": "requirement",           // requirement | design | follow_up
  "title": "<short card heading>",
  "intro": "<markdown: what you understood, what you decided yourself and why, what this round settles>",
  "questions": [ ... ]
}
```

- **`purpose`.** `requirement` for a requirement round, `design` for a design round, `follow_up` for anything else (an answer opened something new, a stage is blocked, a skill needs something only the user knows). After a `follow_up` round the same stage runs again with the answers; after the others the ticket moves to the next stage.
- **`intro`.** List the assumptions you made instead of asking — the decisions the score sent back to you — so the user can see them and correct one in an `allowOther` answer or a later round.
- **Nothing survives the score?** Submit nothing. Record your decisions where the calling skill says and carry on.

### Example — a requirement round with nested branches

Idea: "let me export a board".

```json
{
  "round": 1,
  "purpose": "requirement",
  "title": "Board export",
  "intro": "I read this as: someone on the board can get its tickets out as a file.\n\nDecided without asking: the export holds every column (nothing on the board is private), and it is CSV-compatible so it opens in a spreadsheet.",
  "questions": [
    {
      "id": "export-who",
      "kind": "single",
      "title": "Who needs to export a board?",
      "context": "Decides where the button lives and whether exports must hide anything.",
      "options": [
        { "id": "owner", "label": "Only you, the owner", "description": "One button in the board menu; nothing to hide. Recommended: the app is single-user.", "recommended": true },
        { "id": "anyone", "label": "Anyone you share the board with", "description": "Needs sharing first, which the app does not have." }
      ]
    },
    {
      "id": "export-trigger",
      "kind": "single",
      "title": "Does an export happen only when you ask for it, or also on a schedule?",
      "options": [
        { "id": "on-demand", "label": "Only when I tap Export", "description": "Simplest; nothing runs in the background. Recommended unless you need regular snapshots.", "recommended": true },
        { "id": "scheduled", "label": "Also on a schedule", "description": "A snapshot lands somewhere every day or week without you doing anything." }
      ]
    },
    {
      "id": "export-destination",
      "kind": "single",
      "title": "Where should scheduled exports go?",
      "showIf": { "question": "export-trigger", "anyOf": ["scheduled"] },
      "options": [
        { "id": "repo", "label": "Committed into the project's repo", "description": "History for free, no new service. Recommended: everything else already lives there.", "recommended": true },
        { "id": "email", "label": "Emailed", "description": "Arrives where you already look; needs a mail service." }
      ]
    },
    {
      "id": "export-email-to",
      "kind": "text",
      "title": "Which address should scheduled exports be sent to?",
      "showIf": { "question": "export-destination", "anyOf": ["email"] }
    },
    {
      "id": "export-done-tickets",
      "kind": "single",
      "title": "Should tickets in Done be in the export?",
      "options": [
        { "id": "include", "label": "Yes, everything on the board", "description": "A complete record. Recommended: the file is for looking back.", "recommended": true },
        { "id": "exclude", "label": "No, only unfinished tickets", "description": "A smaller file focused on open work." }
      ]
    }
  ]
}
```

`export-email-to` is two levels deep: it shows only after `scheduled` and then `email`. Answering `on-demand` hides both branches, and nobody is asked where scheduled exports go.

### Example — a design round: confirmation, a prototype pick, and dependent forks

```json
{
  "round": 2,
  "purpose": "design",
  "title": "Confirm the requirement and pick a design",
  "intro": "The requirement and the decisions I made are on the card. Two things need you: which prototype ships, and one stretch.",
  "questions": [
    {
      "id": "requirement-ok",
      "kind": "confirm",
      "title": "Is this requirement right?",
      "context": "# Board export\n\n## Goal\n\n...the full requirement, every section, as it will be built..."
    },
    {
      "id": "prototype",
      "kind": "single",
      "title": "Which export screen ships?",
      "context": "Both run on the live preview; screenshots below.",
      "options": [
        { "id": "menu", "label": "A: Export in the board menu", "description": "One tap, no new screen. Recommended: matches how the other board actions work.", "recommended": true, "preview": { "image": "art_7f3a", "markdown": "Branch `board-export_prototype_menu`" } },
        { "id": "sheet", "label": "B: Export sheet with options", "description": "A bottom sheet to pick columns first; one more tap, more control.", "preview": { "image": "art_91c2", "markdown": "Branch `board-export_prototype_sheet`" } }
      ]
    },
    {
      "id": "sheet-remember",
      "kind": "single",
      "title": "Should the sheet remember the columns you picked last time?",
      "showIf": { "question": "prototype", "anyOf": ["sheet"] },
      "options": [
        { "id": "remember", "label": "Yes, per board", "description": "Repeat exports are one tap again. Recommended.", "recommended": true },
        { "id": "fresh", "label": "No, start from all columns each time", "description": "Never surprises you with a hidden column." }
      ]
    },
    {
      "id": "mail-service",
      "kind": "single",
      "title": "Emailed exports need an outgoing mail account on the server. Is that stretch acceptable?",
      "context": "Adds a new secret (SMTP login) to the server config and a new failure mode (mail bounces).",
      "showIf": { "all": [
        { "question": "requirement-ok", "anyOf": ["yes"] },
        { "question": "prototype", "anyOf": ["menu", "sheet"] }
      ] },
      "options": [
        { "id": "yes", "label": "Yes, add it", "description": "The requirement asks for email; nothing else delivers it. Recommended.", "recommended": true },
        { "id": "no", "label": "No, commit to the repo instead", "description": "Emailed exports move to out of scope." }
      ]
    }
  ]
}
```

`mail-service` waits for both a confirmed requirement and a picked prototype: a correction to the requirement may make email moot, so the stretch is asked only on the confirmed path.

## After the answers

The next run receives the round's answers (`{ "optionIds": [...], "text": "..." }` per visible question). Handle each:

- **A listed option** — record it and move on.
- **Their own words** — record their words; if they reshape earlier answers or decisions, say so in the run's summary and adjust.
- **A correction to a `confirm`** — apply it to the thing confirmed before building on it.
- **"You decide", or a question back at you** — answer it in the summary and take the recommendation.
- **An answer that opens a new decision only the user can make** — score it; if it reaches 0, submit a `follow_up` round. Never re-ask what was answered.

## Never

- Never ask a question that scores 3 or more on the dumbness score.
- Never ask for a fact you can look up.
- Never ask a question whose answer does not change the work.
- Never bundle several decisions into one question.
- Never present more than 3 options.
- Never ask the user in the session's chat, or wait for a reply there; questions go to the card through `submit_questions`.
- Never submit more than one tree per run, or keep building on what the round will decide.
- Never leave a branch for a later run that you could have pre-written now.
- Never show a question on a path where an earlier answer made it moot.
- Never reference a question in `showIf` that comes later in the array.
- Never author a "None of these" option; `allowOther` already supplies it.
- Never present an option you already know is absurd; if it is absurd, drop it and decide.
