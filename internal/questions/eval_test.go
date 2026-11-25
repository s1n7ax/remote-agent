package questions

import (
	"errors"
	"slices"
	"testing"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

type answers = map[string]domain.Answer

func pick(ids ...string) domain.Answer { return domain.Answer{OptionIDs: ids} }

func ids(qs []domain.Question) []string {
	out := []string{}
	for _, q := range qs {
		out = append(out, q.ID)
	}
	return out
}

func TestVisible(t *testing.T) {
	// a: root; b shows when a=x; c shows when b=p (chain through b);
	// d shows when NOT b=p (hidden while b is hidden);
	// e shows when a=y AND (b answered OR c=yes) — exercises nesting;
	// f shows when ANY(b=p, a=y) — a visible true branch wins over hidden b.
	tr := tree(
		single("a", "x", "y"),
		when(single("b", "p", "q"), leaf("a", "x")),
		when(domain.Question{ID: "c", Kind: domain.QuestionConfirm, Title: "c"}, leaf("b", "p")),
		when(single("d", "o"), &domain.Condition{Not: leaf("b", "p")}),
		when(single("e", "o"), &domain.Condition{All: []*domain.Condition{
			leaf("a", "y"),
			{Any: []*domain.Condition{{Question: "b", Answered: true}, leaf("c", "yes")}},
		}}),
		when(single("f", "o"), &domain.Condition{Any: []*domain.Condition{leaf("b", "p"), leaf("a", "y")}}),
		when(single("g", "o"), &domain.Condition{All: []*domain.Condition{leaf("a", "y"), {Not: leaf("b", "q")}}}),
		when(single("h", "o"), &domain.Condition{Not: &domain.Condition{Not: leaf("a", "x")}}),
	)
	tests := []struct {
		name    string
		answers answers
		want    []string
	}{
		{"nothing answered", answers{}, []string{"a"}},
		{"a=x reveals b and d", answers{"a": pick("x")}, []string{"a", "b", "d", "h"}},
		{"b=p reveals c hides d", answers{"a": pick("x"), "b": pick("p")}, []string{"a", "b", "c", "f", "h"}},
		{"b=q keeps d", answers{"a": pick("x"), "b": pick("q")}, []string{"a", "b", "d", "h"}},
		{"a=y hides b chain; stale b answer ignored", answers{"a": pick("y"), "b": pick("p"), "c": pick("yes")},
			// b hidden → c hidden; d is not(b=p) over hidden b → hidden;
			// e: a=y but any(b answered, c=yes) is unknown → hidden;
			// f: any(unknown, true) → visible; g: all(true, unknown) → hidden.
			[]string{"a", "f"}},
		{"a answered by text only", answers{"a": {Text: "other"}}, []string{"a"}},
		{"multi option match", answers{"a": pick("x", "y")}, []string{"a", "b", "d", "f", "g", "h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ids(Visible(tr, tt.answers))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Visible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisibleAllAnyFalse(t *testing.T) {
	tr := tree(
		single("a", "x", "y"),
		single("b", "p", "q"),
		when(single("all", "o"), &domain.Condition{All: []*domain.Condition{leaf("a", "x"), leaf("b", "p")}}),
		when(single("any", "o"), &domain.Condition{Any: []*domain.Condition{leaf("a", "x"), leaf("b", "p")}}),
		when(single("notall", "o"), &domain.Condition{Not: &domain.Condition{All: []*domain.Condition{leaf("a", "x"), leaf("b", "p")}}}),
	)
	tests := []struct {
		answers answers
		want    []string
	}{
		{answers{}, []string{"a", "b", "notall"}},
		{answers{"a": pick("x")}, []string{"a", "b", "any", "notall"}},
		{answers{"a": pick("x"), "b": pick("p")}, []string{"a", "b", "all", "any"}},
		{answers{"a": pick("y"), "b": pick("q")}, []string{"a", "b", "notall"}},
	}
	for _, tt := range tests {
		if got := ids(Visible(tr, tt.answers)); !slices.Equal(got, tt.want) {
			t.Errorf("Visible(%v) = %v, want %v", tt.answers, got, tt.want)
		}
	}
}

func TestComplete(t *testing.T) {
	tr := tree(
		single("a", "x", "y"),
		when(domain.Question{ID: "t", Kind: domain.QuestionText, Title: "t"}, leaf("a", "x")),
		domain.Question{ID: "c", Kind: domain.QuestionConfirm, Title: "c"},
	)
	tests := []struct {
		name    string
		answers answers
		want    bool
	}{
		{"empty", answers{}, false},
		{"hidden question skipped", answers{"a": pick("y"), "c": pick("yes")}, true},
		{"visible text missing", answers{"a": pick("x"), "c": pick("yes")}, false},
		{"all answered", answers{"a": pick("x"), "t": {Text: "because"}, "c": pick("yes")}, true},
		{"confirm correction", answers{"a": pick("y"), "c": {Text: "no, rather…"}}, true},
		{"invalid option does not count", answers{"a": pick("zz"), "c": pick("yes")}, false},
		{"whitespace text does not count", answers{"a": pick("x"), "t": {Text: "  "}, "c": pick("yes")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Complete(tr, tt.answers); got != tt.want {
				t.Errorf("Complete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckAnswer(t *testing.T) {
	no := false
	closed := single("s", "x", "y")
	closed.AllowOther = &no
	multi := domain.Question{ID: "m", Kind: domain.QuestionMulti, Title: "m", Options: opts("x", "y")}
	text := domain.Question{ID: "t", Kind: domain.QuestionText, Title: "t"}
	confirm := domain.Question{ID: "c", Kind: domain.QuestionConfirm, Title: "c"}
	tests := []struct {
		name string
		q    domain.Question
		a    domain.Answer
		ok   bool
	}{
		{"single one", single("s", "x", "y"), pick("x"), true},
		{"single other", single("s", "x"), domain.Answer{Text: "z"}, true},
		{"single two", single("s", "x", "y"), pick("x", "y"), false},
		{"single unknown", single("s", "x"), pick("q"), false},
		{"single empty", single("s", "x"), domain.Answer{}, false},
		{"single other disallowed", closed, domain.Answer{Text: "z"}, false},
		{"single option with other disallowed", closed, pick("y"), true},
		{"multi two", multi, pick("x", "y"), true},
		{"multi duplicate", multi, pick("x", "x"), false},
		{"multi option and text", multi, domain.Answer{OptionIDs: []string{"x"}, Text: "also z"}, true},
		{"multi empty", multi, domain.Answer{}, false},
		{"text", text, domain.Answer{Text: "hi"}, true},
		{"text with option", text, domain.Answer{OptionIDs: []string{"x"}, Text: "hi"}, false},
		{"text empty", text, domain.Answer{}, false},
		{"confirm yes", confirm, pick("yes"), true},
		{"confirm no", confirm, pick("no"), false},
		{"confirm correction", confirm, domain.Answer{Text: "change X"}, true},
		{"confirm empty", confirm, domain.Answer{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckAnswer(tt.q, tt.a)
			if (err == nil) != tt.ok {
				t.Fatalf("CheckAnswer() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}
