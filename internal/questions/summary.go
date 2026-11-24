package questions

import (
	"fmt"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Summary renders a round's visible questions and their answers as markdown,
// for agents' prompts and for the issue comment.
func Summary(rd domain.Round) string {
	var b strings.Builder
	if rd.Tree.Title != "" {
		fmt.Fprintf(&b, "**%s**\n\n", rd.Tree.Title)
	}
	for _, q := range Visible(rd.Tree, rd.Answers) {
		a := rd.Answers[q.ID]
		fmt.Fprintf(&b, "- **%s** — %s\n", q.Title, answerText(q, a))
	}
	return b.String()
}

func answerText(q domain.Question, a domain.Answer) string {
	if a.Empty() {
		return "_unanswered_"
	}
	var parts []string
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
		parts = append(parts, label)
	}
	if a.Text != "" {
		parts = append(parts, fmt.Sprintf("%q", a.Text))
	}
	return strings.Join(parts, "; ")
}
