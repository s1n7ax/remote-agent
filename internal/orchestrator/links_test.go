package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// lastLinkComment returns the latest links comment on an issue.
func (h *harness) lastLinkComment(issue int) string {
	h.t.Helper()
	var body string
	for _, c := range h.gh.commentsOn(issue) {
		if c.Marker == linkMarker {
			body = c.Body
		}
	}
	return body
}

// setStatus moves a ticket the way the app would (merge, failure).
func (h *harness) setStatus(id int64, st domain.Status, note string) {
	h.t.Helper()
	if _, err := h.o.mutate(h.ctx, id, func(t *domain.Ticket) { t.Status, t.StatusNote = st, note }); err != nil {
		h.t.Fatal(err)
	}
	h.o.Kick()
}

func (h *harness) create(idea string, in NewTicket) domain.Ticket {
	h.t.Helper()
	in.Idea, in.Agent = idea, domain.AgentFake
	tk, err := h.o.CreateTicket(h.ctx, h.project.ID, in)
	if err != nil {
		h.t.Fatal(err)
	}
	return tk
}

func TestDependencyHoldsTicketUntilMerged(t *testing.T) {
	h := newHarness(t)
	a := h.create("Add an export API", NewTicket{})
	if _, err := h.o.mutate(h.ctx, a.ID, func(t *domain.Ticket) { t.Requirement = "# Export API\n\nREQ-A-TEXT" }); err != nil {
		t.Fatal(err)
	}
	c := h.create("Polish the export page", NewTicket{})
	b := h.create("Export button", NewTicket{DependsOn: []int64{a.ID}, Related: []int64{c.ID}, Start: true})

	// Accepted, but no stage runs; the card says what it waits on.
	h.waitTicket(b.ID, "held", func(t domain.Ticket) bool {
		return t.Status == domain.StatusReady && strings.HasPrefix(t.StatusNote, "waiting on #1 (Add an export API)")
	})
	h.noRun(150 * time.Millisecond)
	if runs, _ := h.st.TicketRuns(h.ctx, b.ID); len(runs) != 0 {
		t.Fatalf("held ticket has runs: %+v", runs)
	}

	// Both ends of every link are mirrored on GitHub.
	if got := h.lastLinkComment(b.Issue); !strings.Contains(got, "Depends on #1 — Add an export API") || !strings.Contains(got, "Related to #2 — Polish the export page") {
		t.Fatalf("B's links comment = %q", got)
	}
	if got := h.lastLinkComment(a.Issue); !strings.Contains(got, "Blocks #3 — Export button") {
		t.Fatalf("A's links comment = %q", got)
	}
	if got := h.lastLinkComment(c.Issue); !strings.Contains(got, "Related to #3 — Export button") {
		t.Fatalf("C's links comment = %q", got)
	}
	links, err := h.o.Links(h.ctx, a.ID)
	if err != nil || len(links) != 1 || links[0].Kind != domain.LinkBlocks || links[0].TicketID != b.ID || links[0].Status != domain.StatusReady {
		t.Fatalf("A's links = %+v, %v", links, err)
	}

	// Merging A releases B without a click.
	h.setStatus(a.ID, domain.StatusDone, "merged")
	r := h.nextRun()
	if r.Info.TicketID != b.ID {
		t.Fatalf("run started for ticket %d, want %d", r.Info.TicketID, b.ID)
	}
	for _, want := range []string{"## Linked tickets", "- depends on #1 — Add an export API (done)", "- related to #2 — Polish the export page (backlog)"} {
		if !strings.Contains(r.Spec.Prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, r.Spec.Prompt)
		}
	}
	if strings.Contains(r.Spec.Prompt, "update from base") {
		t.Fatalf("a dependency set before the first run needs no update:\n%s", r.Spec.Prompt)
	}
	goal := goalName(h.ticket(b.ID))
	doc, err := os.ReadFile(filepath.Join(r.Spec.Workdir, ".ship", goal, "links", itoa(a.ID)+".md"))
	if err != nil || !strings.Contains(string(doc), "REQ-A-TEXT") {
		t.Fatalf("A's link file = %q, %v", doc, err)
	}
	doc, err = os.ReadFile(filepath.Join(r.Spec.Workdir, ".ship", goal, "links", itoa(c.ID)+".md"))
	if err != nil || !strings.Contains(string(doc), "Polish the export page") {
		t.Fatalf("C's link file = %q, %v", doc, err)
	}
	r.Sess.Finish(agent.Event{})
	h.waitInactive(b.ID)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestFailedClosedOrDeletedDependencyKeepsWaiting(t *testing.T) {
	h := newHarness(t)
	a := h.create("Dependency", NewTicket{})
	b := h.create("Dependent", NewTicket{DependsOn: []int64{a.ID}, Start: true})
	h.waitTicket(b.ID, "held", func(t domain.Ticket) bool { return strings.HasPrefix(t.StatusNote, "waiting on #1") })

	h.setStatus(a.ID, domain.StatusFailed, "the PR was closed without merging")
	h.waitTicket(b.ID, "closed note", func(t domain.Ticket) bool {
		return t.StatusNote == "waiting on #1 — its PR was closed without merging"
	})
	h.setStatus(a.ID, domain.StatusFailed, "run crashed")
	h.waitTicket(b.ID, "failed note", func(t domain.Ticket) bool { return t.StatusNote == "waiting on #1 — it failed" })

	if err := h.o.DeleteTicket(h.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	h.waitTicket(b.ID, "deleted note", func(t domain.Ticket) bool { return t.StatusNote == "waiting on #1 — it was deleted" })
	h.noRun(100 * time.Millisecond)
	links, err := h.o.Links(h.ctx, b.ID)
	if err != nil || len(links) != 1 || !links[0].Deleted || links[0].Title != "Dependency" || links[0].Issue != 1 {
		t.Fatalf("links after delete = %+v, %v", links, err)
	}
	if got := h.lastLinkComment(b.Issue); !strings.Contains(got, "Depends on #1 — Dependency (deleted ticket)") {
		t.Fatalf("B's links comment = %q", got)
	}
	// A new ticket reusing A's id must not satisfy or inherit the link.
	reused := h.newTicket(domain.Ticket{Title: "Unrelated", Status: domain.StatusBacklog})
	if l, _ := h.o.Links(h.ctx, reused.ID); len(l) != 0 {
		t.Fatalf("a new ticket inherited links: %+v", l)
	}

	// Removing the link lets B start.
	if _, err := h.o.RemoveLink(h.ctx, b.ID, links[0].ID); err != nil {
		t.Fatal(err)
	}
	r := h.nextRun()
	if r.Info.TicketID != b.ID {
		t.Fatalf("run for %d, want %d", r.Info.TicketID, b.ID)
	}
	if got := h.lastLinkComment(b.Issue); !strings.Contains(got, "No linked tickets.") {
		t.Fatalf("B's links comment after removal = %q", got)
	}
	r.Sess.Finish(agent.Event{})
	h.waitInactive(b.ID)
}

func TestRelatedLinkNeverHolds(t *testing.T) {
	h := newHarness(t)
	a := h.create("Other work", NewTicket{})
	b := h.create("This work", NewTicket{Related: []int64{a.ID}, Start: true})
	r := h.nextRun()
	if r.Info.TicketID != b.ID {
		t.Fatalf("run for %d, want %d", r.Info.TicketID, b.ID)
	}
	r.Sess.Finish(agent.Event{})
	h.waitInactive(b.ID)
	// A related link reads the same from both ends.
	if l, _ := h.o.Links(h.ctx, a.ID); len(l) != 1 || l[0].Kind != domain.LinkRelated || l[0].TicketID != b.ID {
		t.Fatalf("A's links = %+v", l)
	}
}

func TestLinkRefusals(t *testing.T) {
	h := newHarness(t)
	a := h.create("A", NewTicket{})
	b := h.create("B", NewTicket{DependsOn: []int64{a.ID}})
	c := h.create("C", NewTicket{DependsOn: []int64{b.ID}})
	job := h.newTicket(domain.Ticket{Kind: domain.KindJob, Stage: domain.StageJob, Status: domain.StatusDone})
	other, err := h.st.CreateProject(h.ctx, domain.Project{Name: "other", Repo: "acme/other", DefaultBranch: "main", Agent: domain.AgentFake})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := h.o.CreateTicket(h.ctx, other.ID, NewTicket{Idea: "elsewhere", Agent: domain.AgentFake})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		id   int64
		in   NewLink
		want string
	}{
		{"self", a.ID, NewLink{To: a.ID, Kind: domain.LinkDependsOn}, "cannot depend on itself"},
		{"direct loop", a.ID, NewLink{To: b.ID, Kind: domain.LinkDependsOn}, "dependency loop: #2 already depends on #1"},
		{"loop through others", a.ID, NewLink{To: c.ID, Kind: domain.LinkDependsOn}, "#3 already depends on #2, which depends on #1"},
		{"loop as blocks", c.ID, NewLink{To: a.ID, Kind: domain.LinkBlocks}, "dependency loop"},
		{"duplicate", b.ID, NewLink{To: a.ID, Kind: domain.LinkDependsOn}, "already linked"},
		{"duplicate as blocks", a.ID, NewLink{To: b.ID, Kind: domain.LinkBlocks}, "already linked"},
		{"other project", a.ID, NewLink{To: foreign.ID, Kind: domain.LinkRelated}, "same project"},
		{"job", a.ID, NewLink{To: job.ID, Kind: domain.LinkRelated}, "jobs"},
		{"missing", a.ID, NewLink{To: 9999, Kind: domain.LinkRelated}, "does not exist"},
		{"bad kind", a.ID, NewLink{To: b.ID, Kind: "parent"}, "unknown link kind"},
	}
	for _, tc := range cases {
		_, err := h.o.AddLink(h.ctx, tc.id, tc.in)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want ErrInvalid containing %q", tc.name, err, tc.want)
		}
	}
	if _, err := h.o.CreateTicket(h.ctx, h.project.ID, NewTicket{Idea: "x", DependsOn: []int64{foreign.ID}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("create with a foreign dependency: err = %v", err)
	}
	// A related link the other way round is a duplicate; a related link
	// next to a dependency is not.
	if _, err := h.o.AddLink(h.ctx, a.ID, NewLink{To: c.ID, Kind: domain.LinkRelated}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.AddLink(h.ctx, c.ID, NewLink{To: a.ID, Kind: domain.LinkRelated}); !errors.Is(err, ErrInvalid) {
		t.Errorf("reverse related duplicate: err = %v", err)
	}
	if _, err := h.o.AddLink(h.ctx, b.ID, NewLink{To: a.ID, Kind: domain.LinkRelated}); err != nil {
		t.Errorf("related next to a dependency: %v", err)
	}
	// A link of another ticket cannot be removed through this one.
	bl, _ := h.o.Links(h.ctx, b.ID)
	if _, err := h.o.RemoveLink(h.ctx, foreign.ID, bl[0].ID); err == nil {
		t.Error("removed another ticket's link")
	}
}

func TestLateDependencyBringsBranchUpToDate(t *testing.T) {
	h := newHarness(t)
	a := h.create("Base work", NewTicket{})
	b := h.create("Builds on it", NewTicket{Start: true})
	stop := func() {
		t.Helper()
		if err := h.o.Stop(h.ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		h.waitInactive(b.ID)
		h.waitTicket(b.ID, "stopped", func(t domain.Ticket) bool { return t.Status == domain.StatusBacklog })
	}
	h.nextRun()
	stop()
	if h.ticket(b.ID).Branch == "" {
		t.Fatal("first run cut no branch")
	}

	if _, err := h.o.AddLink(h.ctx, b.ID, NewLink{To: a.ID, Kind: domain.LinkDependsOn}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.o.StartTicket(h.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	h.waitTicket(b.ID, "held", func(t domain.Ticket) bool { return strings.HasPrefix(t.StatusNote, "waiting on #1") })
	h.noRun(100 * time.Millisecond)

	h.setStatus(a.ID, domain.StatusDone, "merged")
	r := h.nextRun()
	if !strings.Contains(r.Spec.Prompt, "- update from base: yes — #1 merged after this branch was cut; merge origin/main into the goal branch first") {
		t.Fatalf("prompt lacks the update line:\n%s", r.Spec.Prompt)
	}
	stop()

	if _, err := h.o.StartTicket(h.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	r = h.nextRun()
	if strings.Contains(r.Spec.Prompt, "update from base") {
		t.Fatalf("the update is asked for once only:\n%s", r.Spec.Prompt)
	}
	stop()
}

func TestHeldNoteWhileSlotsBusy(t *testing.T) {
	h := newHarness(t) // two run slots
	a := h.create("Dependency", NewTicket{})
	busy := []*testRun{}
	for range h.cfg.MaxParallelRuns {
		h.create("Busy", NewTicket{Start: true})
		busy = append(busy, h.nextRun())
	}
	b := h.create("Dependent", NewTicket{DependsOn: []int64{a.ID}, Start: true})
	h.waitTicket(b.ID, "held", func(t domain.Ticket) bool { return strings.HasPrefix(t.StatusNote, "waiting on #1") })
	for _, r := range busy {
		if err := h.o.Stop(h.ctx, r.Info.TicketID); err != nil {
			t.Fatal(err)
		}
		h.waitInactive(r.Info.TicketID)
	}
}
