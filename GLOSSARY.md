# remote-agent

A self-hosted board where coding agents work tickets through the /ship flow while the user answers their questions and reviews their pull requests from a phone.

## Work

**Project**:
A GitHub repository registered on the board, with its own kanban, checkout and agent home.
_Avoid_: repo (when meaning the board entry), workspace

**Ticket**:
One unit of work on a project's board, anchored to a GitHub issue, carrying its idea, requirement, decisions, rounds, runs and proof.
_Avoid_: card, task, job (see Job)

**Job**:
A ticket created by a schedule that runs one prompt once, without the /ship stages.
_Avoid_: cron, scheduled task

**Schedule**:
A cron expression and prompt that creates a job each time it fires.

**Stage**:
The /ship step a ticket is in: explore, design, build, ci, fix, verify, merged or job.
_Avoid_: phase, step

**Status**:
Where a ticket sits on the board: backlog, needs input, ready, working, waiting, in review, done or failed.
_Avoid_: column, state

## Questions

**Round**:
One question tree posted on a ticket at once, with the user's answers; it completes, and the ticket advances, only when the user submits it with every visible question answered.
_Avoid_: question batch, survey

**Question tree**:
The questions of a round, where a question can be shown only when an earlier answer matches.
_Avoid_: form, questionnaire

**Purpose**:
What a round is for: requirement, design, follow-up or spend cap.

## Agents

**Run**:
One agent process working one stage of one ticket.
_Avoid_: session, execution

**Account**:
A Claude Pro/Max subscription login the app rotates between when one hits its usage limit.
_Avoid_: profile, credential

**Usage limit**:
The point where a Claude account's subscription quota is exhausted until its reset time.
_Avoid_: rate limit (for the subscription quota), throttle

**Spend cap**:
The daily or per-ticket USD limit on OpenRouter cost for Pi runs.
_Avoid_: budget

**Sandbox**:
The bubblewrap jail one run executes in, seeing only its project directory.
_Avoid_: container, VM

## Review

**Proof**:
A run's test evidence (verdict, report and screenshots) shown on the ticket and the PR.
_Avoid_: test report, evidence bundle

**Prototype**:
One of 2–3 alternative designs built on its own branch for the user to pick from.
_Avoid_: mockup, variant

**Preview**:
A dev server for a ticket or prototype, proxied to the user over the tailnet.
_Avoid_: demo, staging

**Verified SHA**:
The PR head commit that passed final validation; only that commit may reach in review.
