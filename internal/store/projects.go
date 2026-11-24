package store

import (
	"context"
	"fmt"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

const projectCols = `id, name, repo, default_branch, agent, model, effort, created_at`

func scanProject(r interface{ Scan(...any) error }) (domain.Project, error) {
	var p domain.Project
	err := r.Scan(&p.ID, &p.Name, &p.Repo, &p.DefaultBranch, &p.Agent, &p.Model, &p.Effort, &p.CreatedAt)
	return p, err
}

// CreateProject inserts p and returns it with its id.
func (s *Store) CreateProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	p.CreatedAt = now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO projects(name, repo, default_branch, agent, model, effort, created_at) VALUES(?,?,?,?,?,?,?)`,
		p.Name, p.Repo, p.DefaultBranch, p.Agent, p.Model, p.Effort, p.CreatedAt)
	if err != nil {
		return p, fmt.Errorf("create project: %w", err)
	}
	p.ID, _ = res.LastInsertId()
	return p, nil
}

// Project returns one project.
func (s *Store) Project(ctx context.Context, id int64) (domain.Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if err != nil {
		return p, notFound(err, "project")
	}
	return p, nil
}

// Projects lists every project.
func (s *Store) Projects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectCols+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProject writes every mutable field of p.
func (s *Store) UpdateProject(ctx context.Context, p domain.Project) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE projects SET name=?, repo=?, default_branch=?, agent=?, model=?, effort=? WHERE id=?`,
		p.Name, p.Repo, p.DefaultBranch, p.Agent, p.Model, p.Effort, p.ID)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	return nil
}

// DeleteProject removes a project and everything under it.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}
