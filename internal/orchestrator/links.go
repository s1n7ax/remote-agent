package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// NewLink is a link the user adds from a ticket: Kind is how the other
// ticket To relates to it (depends_on: this ticket depends on To; blocks: To
// depends on this ticket; related).
type NewLink struct {
	To   int64           `json:"to"`
	Kind domain.LinkKind `json:"kind"`
}

// linkMarker marks the issue comment that lists a ticket's links.
const linkMarker = "<!-- remote-agent:links -->"

// Links returns a ticket's links, as seen from it.
func (o *Orchestrator) Links(ctx context.Context, id int64) ([]domain.TicketLink, error) {
	return o.st.TicketLinks(ctx, id)
}

// AddLink links ticket id to another ticket of its project. Only the user
// reaches this; agents have no tool for it.
func (o *Orchestrator) AddLink(ctx context.Context, id int64, in NewLink) ([]domain.TicketLink, error) {
	if err := o.addLink(ctx, id, in); err != nil {
		return nil, err
	}
	o.linksChanged(ctx, id, in.To)
	return o.st.TicketLinks(ctx, id)
}

func (o *Orchestrator) addLink(ctx context.Context, id int64, in NewLink) error {
	// Serialised so two concurrent additions cannot close a loop together.
	o.linkMu.Lock()
	defer o.linkMu.Unlock()
	t, err := o.st.Ticket(ctx, id)
	if err != nil {
		return err
	}
	other, err := o.linkTarget(ctx, t, in)
	if err != nil {
		return err
	}
	dependent, dependency := t, other
	if in.Kind == domain.LinkBlocks {
		dependent, dependency = other, t
	}
	if in.Kind != domain.LinkRelated {
		if err := o.checkLoop(ctx, dependent, dependency); err != nil {
			return err
		}
	}
	existing, err := o.st.TicketLinks(ctx, id)
	if err != nil {
		return err
	}
	for _, l := range existing {
		if !l.Deleted && l.TicketID == other.ID && l.Kind == in.Kind {
			return invalid("%s is already linked to this ticket", ref(other))
		}
	}
	if in.Kind == domain.LinkRelated {
		_, err := o.st.AddLink(ctx, t.ID, other.ID, domain.LinkRelated, false)
		return err
	}
	// A dependent whose branch is already cut misses the dependency's code:
	// its first run after the dependency merges brings the branch up to date.
	_, err = o.st.AddLink(ctx, dependent.ID, dependency.ID, domain.LinkDependsOn, dependent.Branch != "")
	return err
}

// linkTarget loads and checks the other ticket of a new link from t.
func (o *Orchestrator) linkTarget(ctx context.Context, t domain.Ticket, in NewLink) (domain.Ticket, error) {
	switch in.Kind {
	case domain.LinkDependsOn, domain.LinkBlocks, domain.LinkRelated:
	default:
		return domain.Ticket{}, invalid("unknown link kind %q; use depends_on, blocks or related", in.Kind)
	}
	if in.To == t.ID {
		if in.Kind == domain.LinkRelated {
			return domain.Ticket{}, invalid("a ticket cannot be related to itself")
		}
		return domain.Ticket{}, invalid("a ticket cannot depend on itself")
	}
	other, err := o.st.Ticket(ctx, in.To)
	if errors.Is(err, store.ErrNotFound) {
		return other, invalid("ticket %d does not exist", in.To)
	}
	if err != nil {
		return other, err
	}
	if other.ProjectID != t.ProjectID {
		return other, invalid("links are only between tickets of the same project")
	}
	if t.Kind == domain.KindJob || other.Kind == domain.KindJob {
		return other, invalid("jobs (scheduled runs) cannot have links")
	}
	return other, nil
}

// checkLoop refuses "dependent depends on dependency" when dependency
// already depends on dependent, directly or through other tickets.
func (o *Orchestrator) checkLoop(ctx context.Context, dependent, dependency domain.Ticket) error {
	deps, err := o.st.Dependencies(ctx, dependent.ProjectID)
	if err != nil {
		return err
	}
	// Breadth-first from dependency; prev records the path back.
	prev := map[int64]int64{dependency.ID: 0}
	queue := []int64{dependency.ID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == dependent.ID {
			path := []int64{}
			for n := cur; n != 0; n = prev[n] {
				path = append([]int64{n}, path...)
			}
			return invalid("that would make a dependency loop: %s", o.loopText(ctx, path))
		}
		for _, next := range deps[cur] {
			if _, seen := prev[next]; !seen {
				prev[next] = cur
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// loopText renders a dependency path as "#A depends on #B, which depends on #C".
func (o *Orchestrator) loopText(ctx context.Context, path []int64) string {
	names := make([]string, len(path))
	for i, id := range path {
		names[i] = fmt.Sprintf("ticket %d", id)
		if t, err := o.st.Ticket(ctx, id); err == nil {
			names[i] = ref(t)
		}
	}
	var b strings.Builder
	b.WriteString(names[0] + " already depends on " + names[1])
	for _, n := range names[2:] {
		b.WriteString(", which depends on " + n)
	}
	return b.String()
}

// ref names a ticket by its issue number, or its id when it has no issue.
func ref(t domain.Ticket) string {
	return domain.TicketLink{TicketID: t.ID, Issue: t.Issue}.Ref()
}

// RemoveLink removes one of a ticket's links.
func (o *Orchestrator) RemoveLink(ctx context.Context, id, linkID int64) ([]domain.TicketLink, error) {
	l, err := o.st.Link(ctx, linkID)
	if err != nil {
		return nil, err
	}
	if l.From != id && (l.To != id || l.Deleted) {
		return nil, fmt.Errorf("link %d of ticket %d: %w", linkID, id, store.ErrNotFound)
	}
	if err := o.st.DeleteLink(ctx, linkID); err != nil {
		return nil, err
	}
	other := l.To
	if l.From != id {
		other = l.From
	}
	if l.Deleted {
		other = 0
	}
	o.linksChanged(ctx, id, other)
	return o.st.TicketLinks(ctx, id)
}

// linksChanged tells the UI, mirrors the links on both issues and lets a
// released ticket start.
func (o *Orchestrator) linksChanged(ctx context.Context, ids ...int64) {
	for _, id := range ids {
		if id == 0 {
			continue
		}
		t, err := o.st.Ticket(ctx, id)
		if err != nil {
			continue
		}
		o.bus.Publish(Message{Type: "links", ProjectID: t.ProjectID, TicketID: t.ID})
		o.syncLinkComment(ctx, t)
	}
	o.Kick()
}

// syncLinkComment writes the ticket's links into one comment on its issue,
// which GitHub turns into cross-references on the linked issues.
func (o *Orchestrator) syncLinkComment(ctx context.Context, t domain.Ticket) {
	if t.Issue == 0 {
		return
	}
	links, err := o.st.TicketLinks(ctx, t.ID)
	if err != nil {
		o.log.Warn("read links", "ticket", t.ID, "err", err)
		return
	}
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		return
	}
	if err := o.gh.UpsertComment(ctx, p.Repo, t.Issue, linkMarker, linkComment(links)); err != nil {
		o.log.Warn("post links comment", "ticket", t.ID, "err", err)
	}
}

// linkComment is the issue comment listing a ticket's links.
func linkComment(links []domain.TicketLink) string {
	var b strings.Builder
	b.WriteString(linkMarker + "\n**Linked tickets** (kept up to date by remote-agent)\n\n")
	if len(links) == 0 {
		b.WriteString("No linked tickets.\n")
		return b.String()
	}
	verb := map[domain.LinkKind]string{
		domain.LinkDependsOn: "Depends on", domain.LinkBlocks: "Blocks", domain.LinkRelated: "Related to",
	}
	for _, kind := range []domain.LinkKind{domain.LinkDependsOn, domain.LinkBlocks, domain.LinkRelated} {
		for _, l := range links {
			if l.Kind != kind {
				continue
			}
			fmt.Fprintf(&b, "- %s %s — %s", verb[kind], l.Ref(), l.Title)
			if l.Deleted {
				b.WriteString(" (deleted ticket)")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// holdNote says why a ticket may not run yet: it names every dependency that
// is not done (merged). "" means the ticket may run.
func holdNote(links []domain.TicketLink) string {
	var waits []string
	for _, l := range links {
		if l.Kind != domain.LinkDependsOn || l.Finished() {
			continue
		}
		w := l.Ref()
		switch {
		case l.Deleted:
			w += " — it was deleted"
		case l.Status == domain.StatusFailed && strings.Contains(l.StatusNote, "closed without merging"):
			w += " — its PR was closed without merging"
		case l.Status == domain.StatusFailed:
			w += " — it failed"
		default:
			w += " (" + l.Title + ")"
		}
		waits = append(waits, w)
	}
	if len(waits) == 0 {
		return ""
	}
	return "waiting on " + strings.Join(waits, "; ")
}

// held reports whether a ready ticket must wait for its dependencies, and
// shows why on its card. A held ticket keeps its status and takes no run
// slot; the first scheduler pass after its last dependency is done starts
// it.
func (o *Orchestrator) held(ctx context.Context, t domain.Ticket) (bool, error) {
	if t.Kind == domain.KindJob {
		return false, nil
	}
	links, err := o.st.TicketLinks(ctx, t.ID)
	if err != nil {
		return false, err
	}
	note := holdNote(links)
	if note == "" {
		return false, nil
	}
	if note != t.StatusNote {
		_, err := o.update(ctx, t.ID, func(c *domain.Ticket) error {
			if c.Status != domain.StatusReady || c.StatusNote == note {
				return errUnchanged
			}
			c.StatusNote = note
			return nil
		})
		if err != nil && !errors.Is(err, errUnchanged) {
			return true, err
		}
	}
	return true, nil
}
