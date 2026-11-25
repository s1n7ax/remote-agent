package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

const accountCols = `id, label, priority, exhausted_till, warning, utilization, created_at`

func scanAccount(r interface{ Scan(...any) error }) (domain.Account, error) {
	var a domain.Account
	var till sql.NullTime
	err := r.Scan(&a.ID, &a.Label, &a.Priority, &till, &a.Warning, &a.Utilization, &a.CreatedAt)
	a.ExhaustedTill = timePtr(till)
	return a, err
}

// CreateAccount stores a Claude account with its token.
func (s *Store) CreateAccount(ctx context.Context, a domain.Account, token string) (domain.Account, error) {
	a.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts(label, token, priority, created_at) VALUES(?,?,?,?)`,
		a.Label, token, a.Priority, a.CreatedAt)
	if err != nil {
		return a, fmt.Errorf("create account: %w", err)
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

// Accounts lists accounts by priority (lowest number first).
func (s *Store) Accounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY priority, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	out := []domain.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Account returns one account.
func (s *Store) Account(ctx context.Context, id int64) (domain.Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = ?`, id))
	if err != nil {
		return a, notFound(err, "account")
	}
	return a, nil
}

// AccountToken returns an account's token.
func (s *Store) AccountToken(ctx context.Context, id int64) (string, error) {
	var tok string
	if err := s.db.QueryRowContext(ctx, `SELECT token FROM accounts WHERE id = ?`, id).Scan(&tok); err != nil {
		return "", notFound(err, "account")
	}
	return tok, nil
}

// UpdateAccount writes an account's label, priority and usage state.
func (s *Store) UpdateAccount(ctx context.Context, a domain.Account) error {
	_, err := s.db.ExecContext(ctx, `UPDATE accounts SET label=?, priority=?, exhausted_till=?, warning=?, utilization=? WHERE id=?`,
		a.Label, a.Priority, nullTime(a.ExhaustedTill), a.Warning, a.Utilization, a.ID)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	return nil
}

// DeleteAccount removes an account.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}
