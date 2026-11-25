package questions

import (
	"errors"
	"strings"
	"testing"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

func opts(ids ...string) []domain.Option {
	out := make([]domain.Option, len(ids))
	for i, id := range ids {
		out[i] = domain.Option{ID: id, Label: "label " + id}
	}
	return out
}

func single(id string, optionIDs ...string) domain.Question {
	return domain.Question{ID: id, Kind: domain.QuestionSingle, Title: "title " + id, Options: opts(optionIDs...)}
}

func when(q domain.Question, c *domain.Condition) domain.Question {
	q.ShowIf = c
	return q
}

func leaf(q string, anyOf ...string) *domain.Condition {
	return &domain.Condition{Question: q, AnyOf: anyOf}
}

func tree(qs ...domain.Question) domain.QuestionTree {
	return domain.QuestionTree{Title: "Round", Questions: qs}
}

func TestValidate(t *testing.T) {
	text := domain.Question{ID: "t", Kind: domain.QuestionText, Title: "Why?"}
	confirm := domain.Question{ID: "c", Kind: domain.QuestionConfirm, Title: "OK?"}
	tests := []struct {
		name string
		tree domain.QuestionTree
		want string // substring of the error; "" means valid
	}{
		{"valid flat", tree(single("a", "x", "y"), text, confirm), ""},
		{"valid nested", tree(
			single("a", "x", "y"),
			single("b", "p", "q"),
			when(text, &domain.Condition{All: []*domain.Condition{
				leaf("a", "x"),
				{Any: []*domain.Condition{leaf("b", "p"), {Not: leaf("b", "q")}}},
			}}),
			when(confirm, &domain.Condition{Question: "t", Answered: true}),
		), ""},
		{"confirm anyOf yes", tree(confirm, when(single("a", "x"), leaf("c", "yes"))), ""},
		{"no title", domain.QuestionTree{Questions: []domain.Question{text}}, "title is required"},
		{"no questions", tree(), "at least one question"},
		{"missing id", tree(domain.Question{Kind: domain.QuestionText, Title: "x"}), "questions[0]: id is required"},
		{"duplicate id", tree(single("a", "x"), single("a", "y")), `question "a": duplicate id`},
		{"missing question title", tree(domain.Question{ID: "a", Kind: domain.QuestionText}), "title is required"},
		{"unknown kind", tree(domain.Question{ID: "a", Kind: "radio", Title: "x"}), `unknown kind "radio"`},
		{"single without options", tree(single("a")), "need at least one option"},
		{"multi without options", tree(domain.Question{ID: "a", Kind: domain.QuestionMulti, Title: "x"}), "need at least one option"},
		{"duplicate option", tree(single("a", "x", "x")), `option "x": duplicate id`},
		{"empty option id", tree(single("a", "")), "options[0]: id is required"},
		{"empty label", tree(domain.Question{ID: "a", Kind: domain.QuestionSingle, Title: "x", Options: []domain.Option{{ID: "x"}}}), "label is required"},
		{"two recommended", tree(domain.Question{ID: "a", Kind: domain.QuestionSingle, Title: "x", Options: []domain.Option{
			{ID: "x", Label: "x", Recommended: true}, {ID: "y", Label: "y", Recommended: true},
		}}), "at most one option"},
		{"text with options", tree(domain.Question{ID: "a", Kind: domain.QuestionText, Title: "x", Options: opts("x")}), "take no options"},
		{"confirm with options", tree(domain.Question{ID: "a", Kind: domain.QuestionConfirm, Title: "x", Options: opts("no")}), "confirm questions take no options"},
		{"forward reference", tree(when(single("a", "x"), leaf("b", "y")), single("b", "y")), `question "b" is not an earlier question`},
		{"self reference", tree(when(single("a", "x"), leaf("a", "x"))), `question "a" is not an earlier question`},
		{"unknown reference", tree(when(single("a", "x"), leaf("zz", "x"))), "not an earlier question"},
		{"unknown option", tree(single("a", "x"), when(text, leaf("a", "nope"))), `has no option "nope"`},
		{"anyOf on text", tree(text, when(single("a", "x"), leaf("t", "x"))), "cannot reference text question"},
		{"leaf without anyOf or answered", tree(single("a", "x"), when(text, &domain.Condition{Question: "a"})), "anyOf or answered is required"},
		{"leaf with both", tree(single("a", "x"), when(text, &domain.Condition{Question: "a", AnyOf: []string{"x"}, Answered: true})), "not both"},
		{"empty condition", tree(single("a", "x"), when(text, &domain.Condition{})), "exactly one of"},
		{"two forms", tree(single("a", "x"), when(text, &domain.Condition{Question: "a", AnyOf: []string{"x"}, Not: leaf("a", "x")})), "exactly one of"},
		{"nested forward reference", tree(single("a", "x"), when(text, &domain.Condition{Any: []*domain.Condition{
			leaf("a", "x"), {Not: &domain.Condition{All: []*domain.Condition{leaf("c", "yes")}}},
		}}), confirm), `showIf.any[1].not.all[0]: question "c" is not an earlier question`},
		{"null child", tree(single("a", "x"), when(text, &domain.Condition{All: []*domain.Condition{nil}})), "condition is null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.tree)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.want)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v does not wrap ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	err := Validate(tree(single("a"), single("a", "x", "x")))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"need at least one option", "duplicate id", `option "x": duplicate id`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
