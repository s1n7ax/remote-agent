package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTicketRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreateProject(ctx, domain.Project{Name: "demo", Repo: "o/demo", DefaultBranch: "main", Agent: domain.AgentClaude})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	tk, err := s.CreateTicket(ctx, domain.Ticket{ProjectID: p.ID, Kind: domain.KindTicket, Title: "t", Status: domain.StatusBacklog,
		Stage: domain.StageExplore, Agent: domain.AgentClaude, StartAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	tk.Status = domain.StatusReady
	if err := s.UpdateTicket(ctx, &tk); err != nil {
		t.Fatal(err)
	}
	got, err := s.Ticket(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusReady || got.StartAt == nil || !got.StartAt.Equal(at) {
		t.Fatalf("got %+v", got)
	}
	ready, err := s.TicketsByStatus(ctx, domain.StatusReady, domain.StatusWaiting)
	if err != nil || len(ready) != 1 {
		t.Fatalf("ready = %v, %v", ready, err)
	}
	if _, err := s.Ticket(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRoundsNumberAndAnswers(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, _ := s.CreateProject(ctx, domain.Project{Name: "d", Repo: "o/d"})
	tk, _ := s.CreateTicket(ctx, domain.Ticket{ProjectID: p.ID, Title: "t", Status: domain.StatusBacklog, Stage: domain.StageExplore, Agent: domain.AgentFake})
	tree := domain.QuestionTree{Title: "R", Questions: []domain.Question{{ID: "q1", Kind: domain.QuestionText, Title: "?"}}}
	r1, err := s.AddRound(ctx, domain.Round{TicketID: tk.ID, Purpose: domain.PurposeRequirement, Stage: domain.StageDesign, Tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := s.AddRound(ctx, domain.Round{TicketID: tk.ID, Purpose: domain.PurposeDesign, Stage: domain.StageBuild, Tree: tree})
	if r1.N != 1 || r2.N != 2 {
		t.Fatalf("round numbers %d %d", r1.N, r2.N)
	}
	r1.Answers["q1"] = domain.Answer{Text: "hello"}
	if err := s.SaveRound(ctx, r1); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Round(ctx, tk.ID, 1)
	if got.Answers["q1"].Text != "hello" || got.Tree.Questions[0].ID != "q1" {
		t.Fatalf("got %+v", got)
	}
}

func TestSpendAccumulates(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.AddSpend(ctx, "2026-11-25", 0.5); err != nil {
		t.Fatal(err)
	}
	total, err := s.AddSpend(ctx, "2026-11-25", 0.25)
	if err != nil || total != 0.75 {
		t.Fatalf("total %v %v", total, err)
	}
}

func TestEventsPaging(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, _ := s.CreateProject(ctx, domain.Project{Name: "d", Repo: "o/d"})
	tk, _ := s.CreateTicket(ctx, domain.Ticket{ProjectID: p.ID, Title: "t", Status: domain.StatusWorking, Stage: domain.StageBuild, Agent: domain.AgentFake})
	run, _ := s.CreateRun(ctx, domain.Run{TicketID: tk.ID, Stage: domain.StageBuild, Agent: domain.AgentFake, Status: domain.RunRunning})
	for i := 0; i < 5; i++ {
		if _, err := s.AddEvent(ctx, domain.RunEvent{RunID: run.ID, Kind: domain.EventText, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	page, _ := s.Events(ctx, run.ID, 0, 3)
	rest, _ := s.Events(ctx, run.ID, page[2].ID, 10)
	if len(page) != 3 || len(rest) != 2 {
		t.Fatalf("pages %d %d", len(page), len(rest))
	}
}
