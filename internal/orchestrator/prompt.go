package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/questions"
)

// systemPrompt is appended to every run's system prompt.
const systemPrompt = `You are an unattended coding agent run by remote-agent. Nobody watches this session live, and you cannot ask the user anything directly: every question goes through the remote-agent MCP tool submit_questions, and the user answers it later on the ticket.

You have no GitHub credentials and no gh CLI. Use the remote-agent MCP tools for every GitHub action (push_branch, open_pr, update_pr, ci_status, ci_logs, get_ticket, comment, list_pulls). There is no way to merge a pull request, and you must never try: merging is the user's decision.

Your skills are in ~/.claude/skills (Claude Code) or ~/.agents/skills (pi). Follow the ship skill's section for your stage exactly, then call stage_done with a one-paragraph summary and stop.`

// prompt builds a run's first message and system prompt.
func (o *Orchestrator) prompt(ctx context.Context, ar *activeRun) (user, system string, err error) {
	tc, err := o.ticketContext(ctx, ar.ticket.ID)
	if err != nil {
		return "", "", err
	}
	t := tc.Ticket
	var b strings.Builder
	fmt.Fprintf(&b, "Run the `%s` stage of /ship (read the ship skill, section \"Stage: %s\").\n\n", t.Stage, t.Stage)
	fmt.Fprintf(&b, "- Ticket: %d — %s\n- Repository: %s (default branch %s)\n", t.ID, t.Title, tc.Project.Repo, tc.Project.DefaultBranch)
	if t.Kind != domain.KindJob {
		fmt.Fprintf(&b, "- Goal branch: %s (already checked out in your working directory)\n", t.Branch)
	}
	if t.Issue != 0 {
		fmt.Fprintf(&b, "- Issue: #%d\n", t.Issue)
	}
	if t.PR != 0 {
		fmt.Fprintf(&b, "- Pull request: #%d\n", t.PR)
	}
	fmt.Fprintf(&b, "- Prototypes requested: %v\n", t.Prototypes)
	if t.StatusNote != "" && (t.Stage == domain.StageFix || t.Stage == domain.StageVerify) {
		fmt.Fprintf(&b, "- Why this run: %s\n", t.StatusNote)
	}
	if t.Kind == domain.KindJob {
		fmt.Fprintf(&b, "\n## Job\n\n%s\n", t.JobPrompt)
		return b.String(), systemPrompt, nil
	}
	fmt.Fprintf(&b, "\n## Idea\n\n%s\n", t.Idea)
	if t.Requirement != "" {
		fmt.Fprintf(&b, "\n## Requirement\n\n%s\n", t.Requirement)
	}
	if t.Decisions != "" {
		fmt.Fprintf(&b, "\n## Decisions\n\n%s\n", t.Decisions)
	}
	for _, rd := range tc.Rounds {
		if rd.CompletedAt == nil || rd.Purpose == domain.PurposeSpendCap {
			continue
		}
		fmt.Fprintf(&b, "\n## Answers, round %d (%s)\n\n%s\n", rd.N, rd.Purpose, questions.Summary(rd))
	}
	if pick := pickedPrototype(tc.Rounds, tc.Prototypes); pick != nil && t.Stage == domain.StageBuild {
		fmt.Fprintf(&b, "\n## Prototype picked\n\nThe user picked prototype %q (branch %s). Start from it: `git checkout -B %s %s`, then finish it to production standard. Delete the other prototype branches locally.\n",
			pick.Name, pick.Branch, t.Branch, pick.Branch)
	}
	if t.Stage == domain.StageFix && tc.Proof != nil && tc.Proof.Verdict == "FAIL" {
		fmt.Fprintf(&b, "\n## Last verification report (FAIL)\n\n%s\n", tc.Proof.Report)
	}
	return b.String(), systemPrompt, nil
}
