package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

const ticketCols = `id, project_id, kind, title, idea, status, stage, agent, model, effort, prototypes,
	issue, branch, pr, verified_sha, requirement, decisions, status_note, start_at, wait_until, fix_rounds,
	fix_trigger, spend_usd, spend_cap_usd, job_prompt, created_at, updated_at`

func scanTicket(r interface{ Scan(...any) error }) (domain.Ticket, error) {
	var t domain.Ticket
	var startAt, waitUntil sql.NullTime
	err := r.Scan(&t.ID, &t.ProjectID, &t.Kind, &t.Title, &t.Idea, &t.Status, &t.Stage, &t.Agent,
		&t.Model, &t.Effort, &t.Prototypes, &t.Issue, &t.Branch, &t.PR, &t.VerifiedSHA, &t.Requirement, &t.Decisions,
		&t.StatusNote, &startAt, &waitUntil, &t.FixRounds, &t.FixTrigger, &t.SpendUSD, &t.SpendCapUSD, &t.JobPrompt,
		&t.CreatedAt, &t.UpdatedAt)
	t.StartAt = timePtr(startAt)
	t.WaitUntil = timePtr(waitUntil)
	return t, err
}

// CreateTicket inserts t and returns it with its id.
func (s *Store) CreateTicket(ctx context.Context, t domain.Ticket) (domain.Ticket, error) {
	t.CreatedAt = now()
	t.UpdatedAt = t.CreatedAt
	res, err := s.db.ExecContext(ctx, `INSERT INTO tickets(project_id, kind, title, idea, status, stage,
		agent, model, effort, prototypes, issue, branch, pr, verified_sha, requirement, decisions, status_note, start_at,
		wait_until, fix_rounds, fix_trigger, spend_usd, spend_cap_usd, job_prompt, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ProjectID, t.Kind, t.Title, t.Idea, t.Status, t.Stage, t.Agent, t.Model, t.Effort, t.Prototypes,
		t.Issue, t.Branch, t.PR, t.VerifiedSHA, t.Requirement, t.Decisions, t.StatusNote, nullTime(t.StartAt),
		nullTime(t.WaitUntil), t.FixRounds, t.FixTrigger, t.SpendUSD, t.SpendCapUSD, t.JobPrompt, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return t, fmt.Errorf("create ticket: %w", err)
	}
	t.ID, _ = res.LastInsertId()
	return t, nil
}

// Ticket returns one ticket.
func (s *Store) Ticket(ctx context.Context, id int64) (domain.Ticket, error) {
	t, err := scanTicket(s.db.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets WHERE id = ?`, id))
	if err != nil {
		return t, notFound(err, "ticket")
	}
	return t, nil
}

func (s *Store) tickets(ctx context.Context, where string, args ...any) ([]domain.Ticket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ticketCols+` FROM tickets WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()
	out := []domain.Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ticket: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ProjectTickets lists a project's tickets, most recently updated first.
func (s *Store) ProjectTickets(ctx context.Context, projectID int64) ([]domain.Ticket, error) {
	return s.tickets(ctx, `project_id = ? ORDER BY updated_at DESC`, projectID)
}

// TicketsByStatus lists tickets in any of the given statuses, oldest update first.
func (s *Store) TicketsByStatus(ctx context.Context, statuses ...domain.Status) ([]domain.Ticket, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	where := `status IN (?`
	args := []any{statuses[0]}
	for _, st := range statuses[1:] {
		where += `,?`
		args = append(args, st)
	}
	return s.tickets(ctx, where+`) ORDER BY updated_at`, args...)
}

// UpdateTicket writes every mutable field of t.
func (s *Store) UpdateTicket(ctx context.Context, t *domain.Ticket) error {
	t.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE tickets SET title=?, idea=?, status=?, stage=?, agent=?, model=?,
		effort=?, prototypes=?, issue=?, branch=?, pr=?, verified_sha=?, requirement=?, decisions=?, status_note=?, start_at=?,
		wait_until=?, fix_rounds=?, fix_trigger=?, spend_usd=?, spend_cap_usd=?, job_prompt=?, updated_at=? WHERE id=?`,
		t.Title, t.Idea, t.Status, t.Stage, t.Agent, t.Model, t.Effort, t.Prototypes, t.Issue, t.Branch,
		t.PR, t.VerifiedSHA, t.Requirement, t.Decisions, t.StatusNote, nullTime(t.StartAt), nullTime(t.WaitUntil),
		t.FixRounds, t.FixTrigger, t.SpendUSD, t.SpendCapUSD, t.JobPrompt, t.UpdatedAt, t.ID)
	if err != nil {
		return fmt.Errorf("update ticket: %w", err)
	}
	return nil
}

// DeleteTicket removes a ticket.
func (s *Store) DeleteTicket(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tickets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete ticket: %w", err)
	}
	return nil
}

// Prototypes returns the prototypes a ticket's design stage submitted.
func (s *Store) Prototypes(ctx context.Context, ticketID int64) ([]domain.Prototype, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT prototypes_json FROM tickets WHERE id = ?`, ticketID).Scan(&raw); err != nil {
		return nil, notFound(err, "ticket")
	}
	out := []domain.Prototype{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode prototypes: %w", err)
	}
	return out, nil
}

// SetPrototypes stores a ticket's prototypes.
func (s *Store) SetPrototypes(ctx context.Context, ticketID int64, ps []domain.Prototype) error {
	raw, err := json.Marshal(ps)
	if err != nil {
		return fmt.Errorf("encode prototypes: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tickets SET prototypes_json = ? WHERE id = ?`, string(raw), ticketID); err != nil {
		return fmt.Errorf("store prototypes: %w", err)
	}
	return nil
}
