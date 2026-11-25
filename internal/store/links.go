package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// LinkRow is a stored link: From depends on To, or the two are related.
type LinkRow struct {
	ID      int64
	From    int64
	To      int64
	Kind    domain.LinkKind // depends_on | related
	Deleted bool
}

// AddLink stores a link from from to to. needsUpdate marks a dependency
// added after from's branch was cut.
func (s *Store) AddLink(ctx context.Context, from, to int64, kind domain.LinkKind, needsUpdate bool) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_links(from_id, to_id, kind, needs_update, created_at) VALUES(?,?,?,?,?)`,
		from, to, kind, needsUpdate, now())
	if err != nil {
		return 0, fmt.Errorf("add link: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// Link returns one stored link.
func (s *Store) Link(ctx context.Context, id int64) (LinkRow, error) {
	var l LinkRow
	err := s.db.QueryRowContext(ctx, `SELECT id, from_id, to_id, kind, deleted FROM ticket_links WHERE id = ?`, id).
		Scan(&l.ID, &l.From, &l.To, &l.Kind, &l.Deleted)
	if err != nil {
		return l, notFound(err, "link")
	}
	return l, nil
}

// DeleteLink removes a link.
func (s *Store) DeleteLink(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ticket_links WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	return nil
}

// TicketLinks returns every link of a ticket, as seen from it: a dependency
// of another ticket on it reads as blocks.
func (s *Store) TicketLinks(ctx context.Context, id int64) ([]domain.TicketLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.from_id, l.to_id, l.kind, l.deleted, l.to_title, l.to_issue,
		l.needs_update, t.title, t.issue, t.status, t.status_note
		FROM ticket_links l
		LEFT JOIN tickets t ON l.deleted = 0 AND t.id = CASE WHEN l.from_id = ? THEN l.to_id ELSE l.from_id END
		WHERE l.from_id = ? OR (l.to_id = ? AND l.deleted = 0)
		ORDER BY l.id`, id, id, id)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()
	out := []domain.TicketLink{}
	for rows.Next() {
		var (
			l           domain.TicketLink
			from, to    int64
			snapTitle   string
			snapIssue   int
			title, note sql.NullString
			issue       sql.NullInt64
			status      sql.NullString
		)
		if err := rows.Scan(&l.ID, &from, &to, &l.Kind, &l.Deleted, &snapTitle, &snapIssue, &l.NeedsUpdate,
			&title, &issue, &status, &note); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		l.TicketID = to
		if from != id {
			l.TicketID = from
			if l.Kind == domain.LinkDependsOn {
				l.Kind = domain.LinkBlocks
			}
		}
		if l.Deleted || !title.Valid {
			l.Deleted = true
			l.Title, l.Issue = snapTitle, snapIssue
		} else {
			l.Title, l.Issue = title.String, int(issue.Int64)
			l.Status, l.StatusNote = domain.Status(status.String), note.String
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Dependencies maps every ticket of a project to the tickets it depends on
// (links to deleted tickets excluded).
func (s *Store) Dependencies(ctx context.Context, projectID int64) (map[int64][]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.from_id, l.to_id FROM ticket_links l
		JOIN tickets t ON t.id = l.from_id
		WHERE l.kind = ? AND l.deleted = 0 AND t.project_id = ?`, domain.LinkDependsOn, projectID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var from, to int64
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("scan dependency: %w", err)
		}
		out[from] = append(out[from], to)
	}
	return out, rows.Err()
}

// ClearLinkUpdates clears NeedsUpdate on the given links.
func (s *Store) ClearLinkUpdates(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE ticket_links SET needs_update = 0 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("clear link update: %w", err)
		}
	}
	return nil
}
