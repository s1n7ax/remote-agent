// Package questions validates question trees and evaluates which questions
// are visible and whether a round is complete. It does no I/O.
package questions

import (
	"errors"
	"fmt"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// ConfirmYes is the option id that accepts a confirm question.
const ConfirmYes = "yes"

// ErrInvalid is wrapped by every error Validate and CheckAnswer return.
var ErrInvalid = errors.New("invalid question tree")

// Validate reports every problem in tree, joined into one error that wraps
// ErrInvalid, or nil when the tree is well formed.
//
// A valid tree has at least one question; question ids are non-empty and
// unique; kinds are known; single and multi questions have at least one
// option with unique non-empty ids and labels; text questions have no
// options; a single question recommends at most one option; and every showIf
// condition has exactly one form and references only earlier questions (and,
// for anyOf, only option ids those questions offer).
func Validate(tree domain.QuestionTree) error {
	v := validator{seen: map[string]domain.Question{}}
	if strings.TrimSpace(tree.Title) == "" {
		v.addf("tree: title is required")
	}
	if len(tree.Questions) == 0 {
		v.addf("tree: at least one question is required")
	}
	for i, q := range tree.Questions {
		v.question(i, q)
	}
	if len(v.errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(v.errs...))
}

type validator struct {
	seen map[string]domain.Question // earlier questions by id
	errs []error
}

func (v *validator) addf(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

func (v *validator) question(i int, q domain.Question) {
	where := fmt.Sprintf("questions[%d]", i)
	if q.ID != "" {
		where = fmt.Sprintf("question %q", q.ID)
	}
	switch {
	case q.ID == "":
		v.addf("%s: id is required", where)
	case v.seen[q.ID].ID != "":
		v.addf("%s: duplicate id", where)
	}
	if strings.TrimSpace(q.Title) == "" {
		v.addf("%s: title is required", where)
	}
	switch q.Kind {
	case domain.QuestionSingle, domain.QuestionMulti:
		v.options(where, q)
	case domain.QuestionText:
		if len(q.Options) > 0 {
			v.addf("%s: text questions take no options", where)
		}
	case domain.QuestionConfirm:
		for _, o := range q.Options {
			if o.ID != ConfirmYes {
				v.addf("%s: confirm questions take no options other than %q", where, ConfirmYes)
				break
			}
		}
	default:
		v.addf("%s: unknown kind %q (want single, multi, text or confirm)", where, q.Kind)
	}
	if q.ShowIf != nil {
		v.condition(where+": showIf", q.ShowIf)
	}
	// Register after the condition so a question cannot depend on itself.
	if q.ID != "" && v.seen[q.ID].ID == "" {
		v.seen[q.ID] = q
	}
}

func (v *validator) options(where string, q domain.Question) {
	if len(q.Options) == 0 {
		v.addf("%s: %s questions need at least one option", where, q.Kind)
	}
	ids := map[string]bool{}
	recommended := 0
	for j, o := range q.Options {
		switch {
		case o.ID == "":
			v.addf("%s: options[%d]: id is required", where, j)
		case ids[o.ID]:
			v.addf("%s: option %q: duplicate id", where, o.ID)
		}
		ids[o.ID] = true
		if strings.TrimSpace(o.Label) == "" {
			v.addf("%s: options[%d]: label is required", where, j)
		}
		if o.Recommended {
			recommended++
		}
	}
	if q.Kind == domain.QuestionSingle && recommended > 1 {
		v.addf("%s: a single question recommends at most one option", where)
	}
}

func (v *validator) condition(where string, c *domain.Condition) {
	forms := 0
	if c.Question != "" {
		forms++
	}
	if len(c.All) > 0 {
		forms++
	}
	if len(c.Any) > 0 {
		forms++
	}
	if c.Not != nil {
		forms++
	}
	if forms != 1 {
		v.addf("%s: exactly one of question, all, any or not must be set", where)
		return
	}
	switch {
	case c.Question != "":
		v.leaf(where, c)
	case len(c.All) > 0:
		v.children(where+".all", c.All)
	case len(c.Any) > 0:
		v.children(where+".any", c.Any)
	default:
		v.condition(where+".not", c.Not)
	}
}

func (v *validator) children(where string, cs []*domain.Condition) {
	for i, c := range cs {
		if c == nil {
			v.addf("%s[%d]: condition is null", where, i)
			continue
		}
		v.condition(fmt.Sprintf("%s[%d]", where, i), c)
	}
}

func (v *validator) leaf(where string, c *domain.Condition) {
	ref, ok := v.seen[c.Question]
	if !ok {
		v.addf("%s: question %q is not an earlier question", where, c.Question)
		return
	}
	switch {
	case len(c.AnyOf) > 0 && c.Answered:
		v.addf("%s: set either anyOf or answered, not both", where)
	case len(c.AnyOf) == 0 && !c.Answered:
		v.addf("%s: anyOf or answered is required with question", where)
	case len(c.AnyOf) > 0:
		if ref.Kind == domain.QuestionText {
			v.addf("%s: anyOf cannot reference text question %q (use answered)", where, ref.ID)
			return
		}
		for _, id := range c.AnyOf {
			if !hasOption(ref, id) {
				v.addf("%s: question %q has no option %q", where, ref.ID, id)
			}
		}
	}
}

// hasOption reports whether q offers option id; a confirm question always
// offers ConfirmYes.
func hasOption(q domain.Question, id string) bool {
	if q.Kind == domain.QuestionConfirm && id == ConfirmYes {
		return true
	}
	for _, o := range q.Options {
		if o.ID == id {
			return true
		}
	}
	return false
}
