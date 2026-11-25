package questions

import (
	"fmt"
	"slices"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// truth is a three-valued condition result: a condition that depends on a
// hidden question is unknown.
type truth int

const (
	unknown truth = iota
	falsy
	truthy
)

// Visible returns the questions of tree whose showIf holds against answers,
// in tree order. Answers to hidden questions are ignored.
//
// Conditions use three-valued logic: a leaf that references a hidden question
// is unknown, `not` keeps unknown unknown, `all` is false if any child is
// false, `any` is true if any child is true, and otherwise an unknown child
// makes them unknown. A question whose condition ends up unknown is hidden,
// so `not` over a hidden question never reveals anything.
func Visible(tree domain.QuestionTree, answers map[string]domain.Answer) []domain.Question {
	visible := map[string]bool{}
	var out []domain.Question
	for _, q := range tree.Questions {
		if q.ShowIf == nil || eval(q.ShowIf, visible, answers) == truthy {
			visible[q.ID] = true
			out = append(out, q)
		}
	}
	return out
}

func eval(c *domain.Condition, visible map[string]bool, answers map[string]domain.Answer) truth {
	switch {
	case c == nil:
		return unknown
	case c.Question != "":
		if !visible[c.Question] {
			return unknown
		}
		a := answers[c.Question]
		if c.Answered {
			return boolTruth(!a.Empty())
		}
		for _, id := range a.OptionIDs {
			if slices.Contains(c.AnyOf, id) {
				return truthy
			}
		}
		return falsy
	case len(c.All) > 0:
		result := truthy
		for _, child := range c.All {
			switch eval(child, visible, answers) {
			case falsy:
				return falsy
			case unknown:
				result = unknown
			}
		}
		return result
	case len(c.Any) > 0:
		result := falsy
		for _, child := range c.Any {
			switch eval(child, visible, answers) {
			case truthy:
				return truthy
			case unknown:
				result = unknown
			}
		}
		return result
	case c.Not != nil:
		switch eval(c.Not, visible, answers) {
		case truthy:
			return falsy
		case falsy:
			return truthy
		}
		return unknown
	}
	return unknown
}

func boolTruth(b bool) truth {
	if b {
		return truthy
	}
	return falsy
}

// Complete reports whether every visible question of tree has an acceptable
// answer (see CheckAnswer).
func Complete(tree domain.QuestionTree, answers map[string]domain.Answer) bool {
	for _, q := range Visible(tree, answers) {
		a, ok := answers[q.ID]
		if !ok || CheckAnswer(q, a) != nil {
			return false
		}
	}
	return true
}

// CheckAnswer reports why a is not an acceptable answer to q, wrapping
// ErrInvalid, or nil when it is.
//
// single takes exactly one known option, or free text when other answers are
// allowed; multi takes one or more distinct known options and/or free text
// when allowed; text takes non-empty text; confirm takes optionIds ["yes"] or
// a free-text correction.
func CheckAnswer(q domain.Question, a domain.Answer) error {
	text := strings.TrimSpace(a.Text) != ""
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: question %q: %s", ErrInvalid, q.ID, fmt.Sprintf(format, args...))
	}
	for i, id := range a.OptionIDs {
		if !hasOption(q, id) {
			return fail("unknown option %q", id)
		}
		if slices.Contains(a.OptionIDs[:i], id) {
			return fail("option %q given twice", id)
		}
	}
	switch q.Kind {
	case domain.QuestionSingle:
		if len(a.OptionIDs) > 1 {
			return fail("pick one option")
		}
		if text && !q.OtherAllowed() {
			return fail("free-text answers are not allowed")
		}
		if len(a.OptionIDs) == 0 && !text {
			return fail("no answer")
		}
	case domain.QuestionMulti:
		if text && !q.OtherAllowed() {
			return fail("free-text answers are not allowed")
		}
		if len(a.OptionIDs) == 0 && !text {
			return fail("no answer")
		}
	case domain.QuestionText:
		if len(a.OptionIDs) > 0 {
			return fail("text questions take no options")
		}
		if !text {
			return fail("no answer")
		}
	case domain.QuestionConfirm:
		if len(a.OptionIDs) == 0 && !text {
			return fail("confirm with %q or give a correction", ConfirmYes)
		}
	default:
		return fail("unknown kind %q", q.Kind)
	}
	return nil
}
