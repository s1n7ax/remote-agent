package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

func scanRound(r interface{ Scan(...any) error }) (domain.Round, error) {
	var rd domain.Round
	var tree, answers string
	var completed sql.NullTime
	if err := r.Scan(&rd.TicketID, &rd.N, &rd.Purpose, &rd.Stage, &tree, &answers, &completed, &rd.CreatedAt); err != nil {
		return rd, err
	}
	rd.CompletedAt = timePtr(completed)
	if err := json.Unmarshal([]byte(tree), &rd.Tree); err != nil {
		return rd, fmt.Errorf("decode tree: %w", err)
	}
	rd.Answers = map[string]domain.Answer{}
	if err := json.Unmarshal([]byte(answers), &rd.Answers); err != nil {
		return rd, fmt.Errorf("decode answers: %w", err)
	}
	return rd, nil
}

const roundCols = `ticket_id, n, purpose, stage, tree, answers, completed_at, created_at`

// AddRound appends a new round to a ticket and returns it with its number.
func (s *Store) AddRound(ctx context.Context, rd domain.Round) (domain.Round, error) {
	tree, err := json.Marshal(rd.Tree)
	if err != nil {
		return rd, fmt.Errorf("encode tree: %w", err)
	}
	rd.CreatedAt = now()
	rd.Answers = map[string]domain.Answer{}
	err = s.db.QueryRowContext(ctx, `INSERT INTO rounds(ticket_id, n, purpose, stage, tree, created_at)
		VALUES(?, (SELECT COALESCE(MAX(n), 0) + 1 FROM rounds WHERE ticket_id = ?), ?, ?, ?, ?) RETURNING n`,
		rd.TicketID, rd.TicketID, rd.Purpose, rd.Stage, string(tree), rd.CreatedAt).Scan(&rd.N)
	if err != nil {
		return rd, fmt.Errorf("add round: %w", err)
	}
	return rd, nil
}

// Rounds lists a ticket's rounds in order.
func (s *Store) Rounds(ctx context.Context, ticketID int64) ([]domain.Round, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+roundCols+` FROM rounds WHERE ticket_id = ? ORDER BY n`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("list rounds: %w", err)
	}
	defer rows.Close()
	out := []domain.Round{}
	for rows.Next() {
		rd, err := scanRound(rows)
		if err != nil {
			return nil, fmt.Errorf("scan round: %w", err)
		}
		out = append(out, rd)
	}
	return out, rows.Err()
}

// Round returns one round.
func (s *Store) Round(ctx context.Context, ticketID int64, n int) (domain.Round, error) {
	rd, err := scanRound(s.db.QueryRowContext(ctx, `SELECT `+roundCols+` FROM rounds WHERE ticket_id = ? AND n = ?`, ticketID, n))
	if err != nil {
		return rd, notFound(err, "round")
	}
	return rd, nil
}

// SaveRound writes a round's answers and completion time.
func (s *Store) SaveRound(ctx context.Context, rd domain.Round) error {
	answers, err := json.Marshal(rd.Answers)
	if err != nil {
		return fmt.Errorf("encode answers: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE rounds SET answers = ?, completed_at = ? WHERE ticket_id = ? AND n = ?`,
		string(answers), nullTime(rd.CompletedAt), rd.TicketID, rd.N)
	if err != nil {
		return fmt.Errorf("save round: %w", err)
	}
	return nil
}
