package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// ErrTooManyImages is returned when a ticket already has
// domain.MaxTicketImages images.
var ErrTooManyImages = fmt.Errorf("a ticket can have at most %d images", domain.MaxTicketImages)

// Upload is an image stored before its ticket exists.
type Upload struct {
	ID        string
	ProjectID int64
	Name      string
	MIME      string
	Size      int64
	CreatedAt time.Time
}

// AddUpload records an upload's metadata (the file is stored by the
// caller). CreatedAt defaults to now.
func (s *Store) AddUpload(ctx context.Context, u Upload) (Upload, error) {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now()
	}
	u.CreatedAt = u.CreatedAt.UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO uploads(id, project_id, name, mime, size, created_at) VALUES(?,?,?,?,?,?)`,
		u.ID, u.ProjectID, u.Name, u.MIME, u.Size, u.CreatedAt)
	if err != nil {
		return u, fmt.Errorf("add upload: %w", err)
	}
	return u, nil
}

// DeleteUploadsBefore removes uploads never attached to a ticket and created
// before t, and returns their ids so the caller can remove the files.
func (s *Store) DeleteUploadsBefore(ctx context.Context, t time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `DELETE FROM uploads WHERE created_at < ? RETURNING id`, t.UTC())
	if err != nil {
		return nil, fmt.Errorf("delete old uploads: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CreateTicketWithImages inserts t and attaches the given uploads (of t's
// project) as its images 1..n, in one transaction: the scheduler never sees
// the ticket without its images.
func (s *Store) CreateTicketWithImages(ctx context.Context, t domain.Ticket, uploads []string) (domain.Ticket, error) {
	if len(uploads) > domain.MaxTicketImages {
		return t, ErrTooManyImages
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return t, fmt.Errorf("create ticket: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err = insertTicket(ctx, tx, t)
	if err != nil {
		return t, err
	}
	for i, id := range uploads {
		var u Upload
		err := tx.QueryRowContext(ctx, `DELETE FROM uploads WHERE id = ? AND project_id = ? RETURNING name, mime, size`, id, t.ProjectID).
			Scan(&u.Name, &u.MIME, &u.Size)
		if err != nil {
			return t, notFound(err, "upload "+id)
		}
		a := domain.Artifact{ID: id, TicketID: t.ID, Name: u.Name, MIME: u.MIME, Size: u.Size}
		if _, err := attachImage(ctx, tx, a, i+1); err != nil {
			return t, err
		}
	}
	if err := tx.Commit(); err != nil {
		return t, fmt.Errorf("create ticket: %w", err)
	}
	return t, nil
}

// AddTicketImage records a stored file as the ticket's next image.
func (s *Store) AddTicketImage(ctx context.Context, a domain.Artifact) (domain.TicketImage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TicketImage{}, fmt.Errorf("add image: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var active, last int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE removed_at IS NULL), COALESCE(MAX(n), 0)
		FROM ticket_images WHERE ticket_id = ?`, a.TicketID).Scan(&active, &last)
	if err != nil {
		return domain.TicketImage{}, fmt.Errorf("count images: %w", err)
	}
	if active >= domain.MaxTicketImages {
		return domain.TicketImage{}, ErrTooManyImages
	}
	at, err := attachImage(ctx, tx, a, last+1)
	if err != nil {
		return domain.TicketImage{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.TicketImage{}, fmt.Errorf("add image: %w", err)
	}
	return domain.TicketImage{N: last + 1, ArtifactID: a.ID, Name: a.Name, MIME: a.MIME, Size: a.Size, CreatedAt: at}, nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func attachImage(ctx context.Context, tx execer, a domain.Artifact, n int) (time.Time, error) {
	at := now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts(id, ticket_id, run_id, name, mime, size, created_at) VALUES(?,?,0,?,?,?,?)`,
		a.ID, a.TicketID, a.Name, a.MIME, a.Size, at); err != nil {
		return at, fmt.Errorf("add image artifact: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_images(ticket_id, n, artifact_id, created_at) VALUES(?,?,?,?)`,
		a.TicketID, n, a.ID, at); err != nil {
		return at, fmt.Errorf("add image: %w", err)
	}
	return at, nil
}

// TicketImages lists a ticket's current images by number.
func (s *Store) TicketImages(ctx context.Context, ticketID int64) ([]domain.TicketImage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.n, i.artifact_id, a.name, a.mime, a.size, i.created_at
		FROM ticket_images i JOIN artifacts a ON a.id = i.artifact_id
		WHERE i.ticket_id = ? AND i.removed_at IS NULL ORDER BY i.n`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()
	out := []domain.TicketImage{}
	for rows.Next() {
		var im domain.TicketImage
		if err := rows.Scan(&im.N, &im.ArtifactID, &im.Name, &im.MIME, &im.Size, &im.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan image: %w", err)
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

// RemoveTicketImage removes image n from a ticket (its number stays used)
// and deletes its artifact row; it returns the artifact id so the caller can
// remove the file.
func (s *Store) RemoveTicketImage(ctx context.Context, ticketID int64, n int) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("remove image: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRowContext(ctx, `UPDATE ticket_images SET removed_at = ? WHERE ticket_id = ? AND n = ? AND removed_at IS NULL
		RETURNING artifact_id`, now(), ticketID, n).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("image %d: %w", n, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("remove image: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM artifacts WHERE id = ?`, id); err != nil {
		return "", fmt.Errorf("remove image artifact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("remove image: %w", err)
	}
	return id, nil
}

// ProjectFiles lists the artifact and upload ids of a project's tickets, so
// the caller can remove their files when the project is deleted.
func (s *Store) ProjectFiles(ctx context.Context, projectID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM artifacts a JOIN tickets t ON t.id = a.ticket_id WHERE t.project_id = ?
		UNION SELECT id FROM uploads WHERE project_id = ?`, projectID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project files: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan project file: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
