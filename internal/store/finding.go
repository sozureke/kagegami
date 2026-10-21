package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

var severities = []string{"info", "low", "medium", "high", "critical"}

type Finding struct {
	ID           int64
	Severity     string
	Title        string
	Description  string
	SourceBlobID *int64
	CreatedAt    time.Time
}

// AddFinding records a finding. Severity must be one of info, low, medium, high or critical
func (r *Repo) AddFinding(ctx context.Context, f Finding) (Finding, error) {
	if !slices.Contains(severities, f.Severity) {
		return Finding{}, invalid("severity %q (expected one of %v)", f.Severity, severities)
	}
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		return Finding{}, invalid("finding title must not be blank")
	}

	const query = `
		INSERT INTO finding (severity, title, description, source_blob_id, created_at)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id, severity, title, description, source_blob_id, created_at`

	row := r.q.QueryRowContext(ctx, query, f.Severity, f.Title, f.Description, f.SourceBlobID, toMS(r.now()))
	return scanFinding(row)
}

// ListFindings returns every finding in the order it was recorded.
func (r *Repo) ListFindings(ctx context.Context) ([]Finding, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, severity, title, description, source_blob_id, created_at FROM finding ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Finding
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanFinding(s scanner) (Finding, error) {
	var f Finding
	var blob sql.NullInt64
	var created int64
	err := s.Scan(&f.ID, &f.Severity, &f.Title, &f.Description, &blob, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Finding{}, ErrNotFound
	}
	if err != nil {
		return Finding{}, err
	}
	f.CreatedAt = fromMS(created)
	if blob.Valid {
		f.SourceBlobID = &blob.Int64
	}
	return f, nil
}
