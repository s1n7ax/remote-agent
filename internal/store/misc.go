package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

const scheduleCols = `id, project_id, name, cron, prompt, agent, model, effort, enabled, last_run_at, next_run_at, created_at`

func scanSchedule(r interface{ Scan(...any) error }) (domain.Schedule, error) {
	var sc domain.Schedule
	var last, next sql.NullTime
	err := r.Scan(&sc.ID, &sc.ProjectID, &sc.Name, &sc.Cron, &sc.Prompt, &sc.Agent, &sc.Model, &sc.Effort,
		&sc.Enabled, &last, &next, &sc.CreatedAt)
	sc.LastRunAt = timePtr(last)
	sc.NextRunAt = timePtr(next)
	return sc, err
}

// CreateSchedule inserts sc and returns it with its id.
func (s *Store) CreateSchedule(ctx context.Context, sc domain.Schedule) (domain.Schedule, error) {
	sc.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO schedules(project_id, name, cron, prompt, agent, model, effort,
		enabled, last_run_at, next_run_at, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		sc.ProjectID, sc.Name, sc.Cron, sc.Prompt, sc.Agent, sc.Model, sc.Effort, sc.Enabled,
		nullTime(sc.LastRunAt), nullTime(sc.NextRunAt), sc.CreatedAt)
	if err != nil {
		return sc, fmt.Errorf("create schedule: %w", err)
	}
	sc.ID, _ = res.LastInsertId()
	return sc, nil
}

// Schedules lists schedules; projectID 0 lists all.
func (s *Store) Schedules(ctx context.Context, projectID int64) ([]domain.Schedule, error) {
	q := `SELECT ` + scheduleCols + ` FROM schedules`
	var args []any
	if projectID != 0 {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	out := []domain.Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Schedule returns one schedule.
func (s *Store) Schedule(ctx context.Context, id int64) (domain.Schedule, error) {
	sc, err := scanSchedule(s.db.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = ?`, id))
	if err != nil {
		return sc, notFound(err, "schedule")
	}
	return sc, nil
}

// UpdateSchedule writes every mutable field of sc.
func (s *Store) UpdateSchedule(ctx context.Context, sc domain.Schedule) error {
	_, err := s.db.ExecContext(ctx, `UPDATE schedules SET name=?, cron=?, prompt=?, agent=?, model=?, effort=?,
		enabled=?, last_run_at=?, next_run_at=? WHERE id=?`,
		sc.Name, sc.Cron, sc.Prompt, sc.Agent, sc.Model, sc.Effort, sc.Enabled,
		nullTime(sc.LastRunAt), nullTime(sc.NextRunAt), sc.ID)
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	return nil
}

// DeleteSchedule removes a schedule.
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

// AddArtifact records an artifact's metadata (the file is stored by the caller).
func (s *Store) AddArtifact(ctx context.Context, a domain.Artifact) (domain.Artifact, error) {
	a.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(id, ticket_id, run_id, name, mime, size, created_at) VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.TicketID, a.RunID, a.Name, a.MIME, a.Size, a.CreatedAt)
	if err != nil {
		return a, fmt.Errorf("add artifact: %w", err)
	}
	return a, nil
}

// Artifact returns one artifact's metadata.
func (s *Store) Artifact(ctx context.Context, id string) (domain.Artifact, error) {
	var a domain.Artifact
	err := s.db.QueryRowContext(ctx, `SELECT id, ticket_id, run_id, name, mime, size, created_at FROM artifacts WHERE id = ?`, id).
		Scan(&a.ID, &a.TicketID, &a.RunID, &a.Name, &a.MIME, &a.Size, &a.CreatedAt)
	if err != nil {
		return a, notFound(err, "artifact")
	}
	return a, nil
}

// AddProof stores a ticket's test proof.
func (s *Store) AddProof(ctx context.Context, p domain.Proof) (domain.Proof, error) {
	p.CreatedAt = now()
	files, err := json.Marshal(p.Files)
	if err != nil {
		return p, fmt.Errorf("encode proof files: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO proofs(ticket_id, run_id, verdict, report, files, created_at) VALUES(?,?,?,?,?,?)`,
		p.TicketID, p.RunID, p.Verdict, p.Report, string(files), p.CreatedAt)
	if err != nil {
		return p, fmt.Errorf("add proof: %w", err)
	}
	return p, nil
}

// LatestProof returns the newest proof of a ticket; ok is false when none.
func (s *Store) LatestProof(ctx context.Context, ticketID int64) (p domain.Proof, ok bool, err error) {
	var files string
	err = s.db.QueryRowContext(ctx, `SELECT ticket_id, run_id, verdict, report, files, created_at FROM proofs
		WHERE ticket_id = ? ORDER BY id DESC LIMIT 1`, ticketID).
		Scan(&p.TicketID, &p.RunID, &p.Verdict, &p.Report, &files, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("read proof: %w", err)
	}
	if err := json.Unmarshal([]byte(files), &p.Files); err != nil {
		return p, false, fmt.Errorf("decode proof files: %w", err)
	}
	return p, true, nil
}

// AddPushSubscription stores (or replaces) a Web Push subscription.
func (s *Store) AddPushSubscription(ctx context.Context, endpoint, subscription string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO push_subscriptions(endpoint, subscription, created_at) VALUES(?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET subscription = excluded.subscription`, endpoint, subscription, now())
	if err != nil {
		return fmt.Errorf("add push subscription: %w", err)
	}
	return nil
}

// PushSubscriptions lists every stored subscription JSON.
func (s *Store) PushSubscriptions(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT endpoint, subscription FROM push_subscriptions`)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ep, sub string
		if err := rows.Scan(&ep, &sub); err != nil {
			return nil, fmt.Errorf("scan push subscription: %w", err)
		}
		out[ep] = sub
	}
	return out, rows.Err()
}

// DeletePushSubscription removes a subscription that the push service rejected.
func (s *Store) DeletePushSubscription(ctx context.Context, endpoint string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint); err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	return nil
}
