package mcpserver

// Tool descriptions and input schemas, written for the agent that calls them.

const descSubmitQuestions = `Post one round of questions to the user as a branching form on the ticket card.
The user is NOT watching live: they answer later, on their phone. Ask every question
you need in ONE tree (including follow-ups that only matter for some answers, using
showIf), then call stage_done and end your turn; a new run continues with the answers.

tree = {title, intro?, questions[]}. Each question:
- id: unique short id ("q1", "db", ...). Conditions may only reference EARLIER ids.
- kind: "single" (pick one option), "multi" (pick one or more), "text" (free text),
  "confirm" (user accepts with option id "yes" or types a correction; give no options).
- title: the question itself, one sentence. context: markdown on why it matters.
- options (single/multi only, at least one): {id, label, description?, recommended?,
  preview?: {markdown?, image? (artifact id from upload_artifact), url?}}. Mark at most
  one option recommended in a single question; put the recommended option first.
- allowOther: free-text answer allowed besides options (default true).
- showIf (optional): the question is shown only while the condition holds. Forms
  (exactly one per condition object):
    {"question": "q1", "anyOf": ["a", "b"]}   q1's answer includes option a or b
    {"question": "q1", "answered": true}      q1 has any answer (use for text questions)
    {"all": [cond, ...]}  {"any": [cond, ...]}  {"not": cond}
  A condition on a hidden question never holds (not even under "not"), so whole
  branches collapse when their parent is hidden.

Example: {"title":"Requirement","questions":[
 {"id":"db","kind":"single","title":"Which database?","options":[
   {"id":"pg","label":"Postgres","recommended":true},{"id":"sqlite","label":"SQLite"}]},
 {"id":"pool","kind":"text","title":"Max connections?","showIf":{"question":"db","anyOf":["pg"]}},
 {"id":"ok","kind":"confirm","title":"Ship behind a feature flag?"}]}

purpose: "requirement" (round 1), "design" (round 2: requirement confirmation + design),
"follow_up" (an extra round, the default) or "verify". Invalid trees are rejected with
every problem listed; fix them and call again.`

const schemaSubmitQuestions = `{
  "type": "object",
  "required": ["tree"],
  "properties": {
    "purpose": {"type": "string", "enum": ["requirement", "design", "follow_up", "verify"],
      "description": "What the answers feed. Defaults to follow_up."},
    "tree": {
      "type": "object",
      "required": ["title", "questions"],
      "properties": {
        "title": {"type": "string", "description": "Round heading shown on the card."},
        "intro": {"type": "string", "description": "Markdown shown above the questions."},
        "questions": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/question"}}
      }
    }
  },
  "$defs": {
    "question": {
      "type": "object",
      "required": ["id", "kind", "title"],
      "properties": {
        "id": {"type": "string", "minLength": 1, "description": "Unique id; showIf may reference only earlier ids."},
        "kind": {"type": "string", "enum": ["single", "multi", "text", "confirm"]},
        "title": {"type": "string", "minLength": 1},
        "context": {"type": "string", "description": "Markdown: why this matters, trade-offs."},
        "options": {"type": "array", "items": {"$ref": "#/$defs/option"},
          "description": "Required (at least one) for single and multi; omit for text and confirm."},
        "allowOther": {"type": "boolean", "description": "Allow a free-text answer (default true)."},
        "showIf": {"$ref": "#/$defs/condition"}
      }
    },
    "option": {
      "type": "object",
      "required": ["id", "label"],
      "properties": {
        "id": {"type": "string", "minLength": 1},
        "label": {"type": "string", "minLength": 1},
        "description": {"type": "string"},
        "recommended": {"type": "boolean"},
        "preview": {
          "type": "object",
          "properties": {
            "markdown": {"type": "string"},
            "image": {"type": "string", "description": "Artifact id from upload_artifact."},
            "url": {"type": "string", "description": "Live preview URL from start_preview."}
          }
        }
      }
    },
    "condition": {
      "type": "object",
      "description": "Exactly one form: {question, anyOf} | {question, answered: true} | {all} | {any} | {not}.",
      "properties": {
        "question": {"type": "string", "description": "Id of an earlier question."},
        "anyOf": {"type": "array", "items": {"type": "string"}, "description": "Option ids of that question."},
        "answered": {"type": "boolean", "description": "True: holds when that question has any answer."},
        "all": {"type": "array", "items": {"$ref": "#/$defs/condition"}},
        "any": {"type": "array", "items": {"$ref": "#/$defs/condition"}},
        "not": {"$ref": "#/$defs/condition"}
      }
    }
  }
}`

const descSubmitRequirement = `Save the ticket's requirement as markdown (goal, behaviour, constraints,
acceptance criteria, out of scope). It becomes the GitHub issue body and the contract
later stages build and verify against. Call again to replace it.`

const descSubmitDecisions = `Save the ticket's design decisions as markdown (each decision, who decided,
rejected alternatives). Later stages read it. Call again to replace it.`

const schemaMarkdown = `{
  "type": "object",
  "required": ["markdown"],
  "properties": {"markdown": {"type": "string", "minLength": 1}}
}`

const descSubmitPrototypes = `Record 2–3 prototype options built on separate local branches, so the user
can pick one. Upload screenshots first with upload_artifact and pass their ids as
artifacts; give preview.command and preview.port so the app can serve the prototype
live (the command runs from the checkout root, e.g. "cd .ship/<goal>/prototypes/<name>
&& npm run dev -- --port 5173"). Then ask the user to pick with submit_questions (one
single question, one option per prototype, option ids equal to the prototype names).`

const schemaSubmitPrototypes = `{
  "type": "object",
  "required": ["prototypes"],
  "properties": {
    "prototypes": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["name", "branch", "description"],
        "properties": {
          "name": {"type": "string", "description": "Short unique kebab-case name, e.g. \"cards\"."},
          "branch": {"type": "string", "description": "Local branch holding this prototype."},
          "description": {"type": "string", "description": "One line: what sets it apart."},
          "artifacts": {"type": "array", "items": {"type": "string"}, "description": "Screenshot artifact ids from upload_artifact."},
          "preview": {
            "type": "object",
            "required": ["command", "port"],
            "properties": {
              "command": {"type": "string", "description": "Shell command, run from the checkout root, that serves this prototype."},
              "port": {"type": "integer", "minimum": 1, "maximum": 65535}
            }
          }
        }
      }
    }
  }
}`

const descStartPreview = `Start (or restart) a dev server in the working directory so the user can try
it from their phone. command must start a server listening on port (on localhost); the
app proxies it and returns the public URL to put in question previews or reports.
branch optionally names the prototype branch being previewed.`

const schemaStartPreview = `{
  "type": "object",
  "required": ["command", "port"],
  "properties": {
    "branch": {"type": "string", "description": "Optional branch the preview shows."},
    "command": {"type": "string", "minLength": 1, "description": "Shell command run in the working directory."},
    "port": {"type": "integer", "minimum": 1, "maximum": 65535}
  }
}`

const descSubmitProof = `Attach the final test evidence to the ticket card: verdict PASS or FAIL, a
markdown report of what was tested and how (commands, observed results), and files
(screenshots, logs) as paths inside the working directory, which are uploaded for you.`

const schemaSubmitProof = `{
  "type": "object",
  "required": ["verdict", "report_markdown"],
  "properties": {
    "verdict": {"type": "string", "enum": ["PASS", "FAIL"]},
    "report_markdown": {"type": "string", "minLength": 1},
    "files": {"type": "array", "items": {"type": "string"},
      "description": "Paths (relative to, or inside, the working directory)."}
  }
}`

const descUploadArtifact = `Upload one file from the working directory (e.g. a screenshot) and get its
artifact id, for question option previews, prototype screenshots or reports.`

const schemaPath = `{
  "type": "object",
  "required": ["path"],
  "properties": {"path": {"type": "string", "minLength": 1,
    "description": "Path relative to, or inside, the working directory."}}
}`

const descPushBranch = `Push the ticket's goal branch (commit first) to GitHub. Never force-pushes, so
do not rewrite already-pushed history; the default branch is never pushed. A ticket run
pushes only its own goal branch; a job run any branch except the default one and the
tickets' ra/<id>-* goal branches. Returns the branch and pushed commit.`

const schemaNone = `{"type": "object", "properties": {}}`

const descOpenPR = `Open the ticket's pull request from its goal branch into the default branch
(push_branch first). draft defaults to true; the app marks it ready after verification.
You cannot merge: only the user merges, after review.`

const schemaOpenPR = `{
  "type": "object",
  "required": ["title", "body"],
  "properties": {
    "title": {"type": "string", "minLength": 1, "description": "Conventional-commit style title."},
    "body": {"type": "string", "minLength": 1, "description": "Markdown description."},
    "draft": {"type": "boolean", "description": "Open as draft (default true)."}
  }
}`

const descUpdatePR = `Replace the description (body) of the ticket's pull request.`

const schemaBody = `{
  "type": "object",
  "required": ["body"],
  "properties": {
    "body": {"type": "string", "minLength": 1, "description": "Markdown."},
    "number": {"type": "integer", "minimum": 1, "description": "Scheduled jobs only: the PR or issue number to act on. Ticket runs omit it."}
  }
}`

const schemaNumber = `{
  "type": "object",
  "properties": {"number": {"type": "integer", "minimum": 1, "description": "Scheduled jobs only: the PR or issue number to act on. Ticket runs omit it."}}
}`

const descGetTicket = `The ticket this run works on: title, idea, requirement, decisions, every question
round with the user's answers, prototypes and the latest proof.`

const descCreateIssue = `Open a GitHub issue in the project (e.g. for a TODO you leave in the code).
Returns its number and URL.`

const schemaCreateIssue = `{
  "type": "object",
  "required": ["title", "body"],
  "properties": {
    "title": {"type": "string", "minLength": 1},
    "body": {"type": "string", "minLength": 1, "description": "Markdown."}
  }
}`

const descCIStatus = `CI on the head commit of the ticket's pull request: overall state
(pending | success | failure) and every check with name, status, conclusion and URL.
Do not poll in a loop: the app waits for CI itself and starts a fix run when it fails.`

const descCILogs = `The failing part (up to 200 lines leading to the error) of one CI check's log,
by check name as listed by ci_status.`

const schemaCILogs = `{
  "type": "object",
  "required": ["check"],
  "properties": {
    "check": {"type": "string", "minLength": 1, "description": "Check name from ci_status."},
    "number": {"type": "integer", "minimum": 1, "description": "Scheduled jobs only: the PR or issue number to act on. Ticket runs omit it."}
  }
}`

const descGetIssue = `The ticket's GitHub issue: number, title, body (the requirement), state, labels.`

const descComment = `Post a markdown comment on the ticket's pull request (or its issue while there
is no pull request). Use sparingly for things reviewers should see on GitHub.`

const descListPulls = `List the project's open pull requests (number, title, branch, head sha, draft).`

const descStageDone = `Finish this run's stage. summary (markdown) tells the user and the next stage
what was done, what is left, and anything surprising. After calling it, end your turn.`

const schemaStageDone = `{
  "type": "object",
  "required": ["summary"],
  "properties": {"summary": {"type": "string", "minLength": 1}}
}`
