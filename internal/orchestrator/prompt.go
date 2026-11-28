package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/questions"
)

// The first message and the goal directory follow skills/ship/STAGES.md; the
// skills key off its headings, so change both together.

// systemPrompt is appended to every run's system prompt.
const systemPrompt = `You are an unattended coding agent run by remote-agent. Nobody watches this session live, and you cannot ask the user anything directly: every question goes through the remote-agent MCP tool submit_questions, and the user answers it later on the ticket.

The run ends when your turn ends. Do every step yourself in this session: never start subagents or background tasks, and never end a turn to wait for something. The app waits for CI and for the user, and starts the next run.

You have no GitHub credentials and no gh CLI. Use the remote-agent MCP tools for every GitHub action. There is no way to merge a pull request, and you must never try: merging is the user's decision.`

// goalName is the ticket's goal: its branch without the ra/<id>- prefix.
func goalName(t domain.Ticket) string {
	b := strings.TrimPrefix(t.Branch, "ra/")
	if i := strings.Index(b, "-"); i >= 0 && strings.HasPrefix(b, fmt.Sprint(t.ID)+"-") {
		b = b[i+1:]
	}
	return strings.ReplaceAll(b, "/", "-")
}

// writeGoalDir refreshes .ship/<goal>/ in the checkout from the database.
// The checkout is agent-writable, so every write goes through an os.Root at
// it and replaces what was there: a planted symlink is removed, not
// followed.
// The ticket's images are copied from artifactPath(id) to images/<n>.<ext>,
// after removing whatever that directory held, so a removed image is gone.
func writeGoalDir(workdir string, tc TicketContext, artifactPath func(id string) string) error {
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return fmt.Errorf("open checkout: %w", err)
	}
	defer root.Close()
	dir := filepath.Join(".ship", goalName(tc.Ticket))
	write := func(rel, content string) error {
		return writeNew(root, filepath.Join(dir, rel), []byte(content))
	}
	if err := writeNew(root, filepath.Join(".ship", ".gitignore"), []byte("*\n")); err != nil {
		return fmt.Errorf("create goal dir: %w", err)
	}
	if tc.Ticket.Requirement != "" {
		if err := write("requirement/requirement.md", tc.Ticket.Requirement); err != nil {
			return err
		}
	}
	if tc.Ticket.Decisions != "" {
		if err := write("decisions/decisions.md", tc.Ticket.Decisions); err != nil {
			return err
		}
	}
	for _, rd := range tc.Rounds {
		if rd.CompletedAt == nil || rd.Purpose == domain.PurposeSpendCap {
			continue
		}
		visible := questions.Visible(rd.Tree, rd.Answers)
		answers := map[string]domain.Answer{}
		for _, q := range visible {
			answers[q.ID] = rd.Answers[q.ID]
		}
		tree := rd.Tree
		tree.Questions = visible
		raw, err := json.MarshalIndent(map[string]any{"tree": tree, "answers": answers}, "", "  ")
		if err != nil {
			return err
		}
		if err := write(fmt.Sprintf("rounds/%d.json", rd.N), string(raw)); err != nil {
			return err
		}
	}
	if err := ensureDir(root, dir); err != nil {
		return fmt.Errorf("create goal dir: %w", err)
	}
	if err := root.RemoveAll(filepath.Join(dir, "images")); err != nil {
		return fmt.Errorf("clear images: %w", err)
	}
	for _, im := range tc.Images {
		if err := copyImage(root, filepath.Join(dir, imageFile(im)), artifactPath(im.ArtifactID)); err != nil {
			return fmt.Errorf("image %d: %w", im.N, err)
		}
	}
	if len(tc.Prototypes) > 0 {
		raw, err := json.MarshalIndent(tc.Prototypes, "", "  ")
		if err != nil {
			return err
		}
		if err := write("prototypes.json", string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// imageFile is an image's path in the goal directory.
func imageFile(im domain.TicketImage) string {
	ext := imageExt[im.MIME]
	if ext == "" {
		ext = "img"
	}
	return fmt.Sprintf("images/%d.%s", im.N, ext)
}

func copyImage(root *os.Root, rel, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return copyNew(root, rel, f)
}

// skillsDirFor is where the agent finds its skills.
func skillsDirFor(kind domain.AgentKind) string {
	if kind == domain.AgentPi {
		return "~/.agents/skills"
	}
	return "~/.claude/skills"
}

// prompt builds a run's first message and system prompt, and refreshes the
// goal directory the message points at.
func (o *Orchestrator) prompt(ctx context.Context, ar *activeRun) (user, system string, err error) {
	tc, err := o.ticketContext(ctx, ar.ticket.ID)
	if err != nil {
		return "", "", err
	}
	t, p := tc.Ticket, tc.Project
	if t.Branch == "" {
		t.Branch = ar.ticket.Branch
		tc.Ticket.Branch = t.Branch
	}
	job := t.Kind == domain.KindJob
	if !job {
		if err := writeGoalDir(ar.workdir, tc, o.ArtifactPath); err != nil {
			return "", "", fmt.Errorf("write goal dir: %w", err)
		}
	}
	goal := goalName(t)
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("You are running the `%s` stage of the /ship flow for one ticket on the remote-agent board.", t.Stage)
	line("Read %s/ship/SKILL.md, then follow its \"Stage: %s\" section in full.", skillsDirFor(ar.run.Agent), t.Stage)
	line("You cannot reach the user except through the app's MCP tools. Report the stage's outcome with its tool, end the run with stage_done, and never end a turn to wait for anything: the run ends with your turn.")
	line("")
	line("## Run")
	line("")
	line("- ticket: %d — %s", t.ID, t.Title)
	line("- stage: %s", t.Stage)
	line("- project: %s", p.Repo)
	line("- base branch: %s", p.DefaultBranch)
	if !job {
		line("- goal: %s", goal)
		line("- goal branch: %s", t.Branch)
		line("- goal directory: .ship/%s/", goal)
		if t.Issue != 0 {
			line("- issue: #%d https://github.com/%s/issues/%d", t.Issue, p.Repo, t.Issue)
		}
		if t.PR != 0 {
			line("- pull request: #%d https://github.com/%s/pull/%d", t.PR, p.Repo, t.PR)
		} else {
			line("- pull request: none")
		}
		line("- next round: %d", len(tc.Rounds)+1)
		if t.Prototypes {
			line("- prototypes: yes, 2–3")
		} else {
			line("- prototypes: no")
		}
		line("- critical: no")
		line("- review effort: medium")
	}
	r, why := o.resumeReason(ctx, t)
	if r != "" {
		line("- resumed: %s", r)
	}
	if why != "" {
		line("")
		line("## Why the last run stopped")
		line("")
		line("Your previous run of this stage stopped before it finished: %s.", why)
		line("Continue the stage from where it stopped; do not redo work the checkout and .ship/%s/ already show.", goal)
	}

	if t.Stage == domain.StageExplore || t.Stage == domain.StageDesign {
		line("")
		line("## Idea")
		line("")
		line("%s", t.Idea)
	}
	if !job && len(tc.Images) > 0 {
		line("")
		line("## Images")
		line("")
		if o.SeesImages(ar.run.Agent, ar.run.Model) {
			line("The ticket has %d image(s), numbered in the order the user added them; the idea and the answers may refer to them by number. Open every one with your file-reading tool before you start, and hand the paths to any subagent that needs them.", len(tc.Images))
		} else {
			line("The ticket has %d image(s), numbered in the order the user added them; the idea and the answers may refer to them by number. This run's model cannot look at images, so do not try to open them: work from the text, and say in your summary that you could not see them.", len(tc.Images))
		}
		line("")
		for _, im := range tc.Images {
			line("- image %d: .ship/%s/%s", im.N, goal, imageFile(im))
		}
	}
	if !job {
		var rounds []domain.Round
		for _, rd := range tc.Rounds {
			if rd.CompletedAt != nil && rd.Purpose != domain.PurposeSpendCap && answersFor(t.Stage, rd) {
				rounds = append(rounds, rd)
			}
		}
		if len(rounds) > 0 {
			line("")
			line("## Answers")
			for _, rd := range rounds {
				line("")
				line("Round %d (%s) — full tree and answers: .ship/%s/rounds/%d.json", rd.N, rd.Purpose, goal, rd.N)
				line("")
				for _, q := range questions.Visible(rd.Tree, rd.Answers) {
					line("- %s — %s: %s", q.ID, q.Title, answerLine(q, rd.Answers[q.ID]))
				}
			}
		}
	}
	if (t.Stage == domain.StageFix || t.Stage == domain.StageVerify) && tc.Ticket.Requirement != "" {
		line("")
		line("## Requirement")
		line("")
		line("%s", tc.Ticket.Requirement)
	}
	if t.Stage == domain.StageFix {
		line("")
		line("## Trigger")
		line("")
		kind := t.FixTrigger
		switch {
		case kind != "":
		case strings.Contains(t.StatusNote, "conflict"):
			kind = fixConflict
		case strings.Contains(t.StatusNote, "verification"):
			kind = fixVerifyFailed
		default:
			kind = fixCIFailed
		}
		line("- kind: %s", kind)
		if t.PR != 0 {
			if pr, err := o.gh.Pull(ctx, p.Repo, t.PR); err == nil {
				line("- head: %s", pr.HeadSHA)
				var failing []string
				for _, c := range pr.Checks {
					if c.Conclusion != "" && c.Conclusion != "success" && c.Conclusion != "skipped" && c.Conclusion != "neutral" {
						failing = append(failing, fmt.Sprintf("%s (%s)", c.Name, c.Conclusion))
					}
				}
				if len(failing) > 0 {
					line("- failing checks: %s", strings.Join(failing, ", "))
				}
			}
		}
		line("- fix round: %d of %d", t.FixRounds, maxFixRounds)
		if kind == fixVerifyFailed && tc.Proof != nil {
			line("")
			line("Last verification report (FAIL):")
			line("")
			line("%s", tc.Proof.Report)
		}
	}
	if job {
		line("")
		line("## Job")
		line("")
		line("%s", strings.SplitN(t.Title, " — ", 2)[0])
		line("")
		line("%s", t.JobPrompt)
	}
	return b.String(), systemPrompt, nil
}

// answersFor reports whether a completed round belongs in a stage's prompt.
func answersFor(stage domain.Stage, rd domain.Round) bool {
	switch stage {
	case domain.StageDesign:
		return rd.Purpose == domain.PurposeRequirement || rd.Purpose == domain.PurposeFollowUp
	case domain.StageBuild:
		return rd.Purpose == domain.PurposeDesign || rd.Purpose == domain.PurposeFollowUp
	default:
		return rd.Purpose == domain.PurposeFollowUp && rd.Stage == stage
	}
}

func answerLine(q domain.Question, a domain.Answer) string {
	var labels []string
	for _, id := range a.OptionIDs {
		label := id
		for _, opt := range q.Options {
			if opt.ID == id {
				label = opt.Label
			}
		}
		if q.Kind == domain.QuestionConfirm && id == "yes" {
			label = "Yes"
		}
		labels = append(labels, label)
	}
	out := strings.Join(labels, ", ")
	if a.Text != "" {
		if out != "" {
			out += " — "
		}
		out += fmt.Sprintf("%q", a.Text)
	}
	return out
}

// resumeReason says why this run repeats a stage ("" for a first run) and,
// when the run before it failed, what stopped it.
func (o *Orchestrator) resumeReason(ctx context.Context, t domain.Ticket) (reason, why string) {
	runs, err := o.st.TicketRuns(ctx, t.ID)
	if err != nil || len(runs) < 2 {
		return "", ""
	}
	prev := runs[1] // runs[0] is this run
	if prev.Stage != t.Stage {
		return "", ""
	}
	switch prev.Status {
	case domain.RunLimited:
		return "account switch", ""
	case domain.RunFailed:
		return "retry after failure", prev.Error
	case domain.RunStopped, domain.RunCapped:
		return "user resume", ""
	case domain.RunSucceeded:
		return "follow_up answered", ""
	}
	return "", ""
}
