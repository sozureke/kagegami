package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// single row describing the case.
type Engagement struct {
	Name       string
	Target     string
	Goal       string
	Scope      string
	ROE        string
	Phase      string
	Difficulty string
	CreatedAt  time.Time
}

func (r *Repo) SaveEngagement(ctx context.Context, e Engagement) (Engagement, error) {
	if e.Name == "" {
		return Engagement{}, invalid("engagement name must not be empty")
	}
	const query = `
INSERT INTO engagement (id, name, target, goal, scope, roe, phase, difficulty, created_at)
VALUES (1, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), 'recon'), ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name, target = excluded.target, goal = excluded.goal, scope = excluded.scope,
    roe = excluded.roe, phase = excluded.phase, difficulty = excluded.difficulty
RETURNING name, target, goal, scope, roe, phase, difficulty, created_at`

	row := r.q.QueryRowContext(ctx, query, e.Name, e.Target, e.Goal, e.Scope, e.ROE, e.Phase, e.Difficulty, toMS(r.now()))
	return scanEngagement(row)
}

// returns the engagement, or ErrNotFound before SaveEngagement was called.
func (r *Repo) GetEngagement(ctx context.Context) (Engagement, error) {
	return scanEngagement(r.q.QueryRowContext(ctx,
		`SELECT name, target, goal, scope, roe, phase, difficulty, created_at FROM engagement WHERE id = 1`))
}

func scanEngagement(s scanner) (Engagement, error) {
	var e Engagement
	var created int64
	err := s.Scan(&e.Name, &e.Target, &e.Goal, &e.Scope, &e.ROE, &e.Phase, &e.Difficulty, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Engagement{}, ErrNotFound
	}
	if err != nil {
		return Engagement{}, err
	}
	e.CreatedAt = fromMS(created)
	return e, nil
}
