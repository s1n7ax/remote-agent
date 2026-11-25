package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

const runCols = `id, ticket_id, stage, agent, model, effort, account_id, session_id, status, summary, error,
	cost_usd, started_at, finished_at, created_at`

func scanRun(r interface{ Scan(...any) error }) (domain.Run, error) {
	var run domain.Run
	var started, finished sql.NullTime
	err := r.Scan(&run.ID, &run.TicketID, &run.Stage, &run.Agent, &run.Model, &run.Effort, &run.AccountID,
		&run.SessionID, &run.Status, &run.Summary, &run.Error, &run.CostUSD, &started, &finished, &run.CreatedAt)
	run.StartedAt = timePtr(started)
	run.FinishedAt = timePtr(finished)
	return run, err
}

// CreateRun inserts run and returns it with its id.
func (s *Store) CreateRun(ctx context.Context, run domain.Run) (domain.Run, error) {
	run.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO runs(ticket_id, stage, agent, model, effort, account_id,
		session_id, status, summary, error, cost_usd, started_at, finished_at, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.TicketID, run.Stage, run.Agent, run.Model, run.Effort, run.AccountID, run.SessionID, run.Status,
		run.Summary, run.Error, run.CostUSD, nullTime(run.StartedAt), nullTime(run.FinishedAt), run.CreatedAt)
	if err != nil {
		return run, fmt.Errorf("create run: %w", err)
	}
	run.ID, _ = res.LastInsertId()
	return run, nil
}

// UpdateRun writes every mutable field of run.
func (s *Store) UpdateRun(ctx context.Context, run domain.Run) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET account_id=?, session_id=?, status=?, summary=?, error=?,
		cost_usd=?, started_at=?, finished_at=? WHERE id=?`,
		run.AccountID, run.SessionID, run.Status, run.Summary, run.Error, run.CostUSD,
		nullTime(run.StartedAt), nullTime(run.FinishedAt), run.ID)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

// Run returns one run.
func (s *Store) Run(ctx context.Context, id int64) (domain.Run, error) {
	run, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if err != nil {
		return run, notFound(err, "run")
	}
	return run, nil
}

// TicketRuns lists a ticket's runs, newest first.
func (s *Store) TicketRuns(ctx context.Context, ticketID int64) ([]domain.Run, error) {
	return s.runs(ctx, `ticket_id = ? ORDER BY id DESC`, ticketID)
}

// RunsByStatus lists runs in a status.
func (s *Store) RunsByStatus(ctx context.Context, st domain.RunStatus) ([]domain.Run, error) {
	return s.runs(ctx, `status = ? ORDER BY id`, st)
}

func (s *Store) runs(ctx context.Context, where string, args ...any) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM runs WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	out := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// AddEvent appends a run log line and returns it with its id.
func (s *Store) AddEvent(ctx context.Context, ev domain.RunEvent) (domain.RunEvent, error) {
	ev.CreatedAt = now()
	var data any
	if len(ev.Data) > 0 {
		data = string(ev.Data)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO run_events(run_id, kind, text, data, created_at) VALUES(?,?,?,?,?)`,
		ev.RunID, ev.Kind, ev.Text, data, ev.CreatedAt)
	if err != nil {
		return ev, fmt.Errorf("add event: %w", err)
	}
	ev.ID, _ = res.LastInsertId()
	return ev, nil
}

// Events returns up to limit events of a run with id greater than after.
func (s *Store) Events(ctx context.Context, runID, after int64, limit int) ([]domain.RunEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, kind, text, data, created_at FROM run_events
		WHERE run_id = ? AND id > ? ORDER BY id LIMIT ?`, runID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := []domain.RunEvent{}
	for rows.Next() {
		var ev domain.RunEvent
		var data sql.NullString
		if err := rows.Scan(&ev.ID, &ev.RunID, &ev.Kind, &ev.Text, &data, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if data.Valid {
			ev.Data = []byte(data.String)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
