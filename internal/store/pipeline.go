package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PipelineRun is one saved answer of `kagegami pipeline`. Its id is the version a tester can
// compare a later run against.
type PipelineRun struct {
	ID        int64
	CreatedAt time.Time
	Model     string
	Summary   string
	// Steps is the JSON that was shown to the tester (an array of advice.Step); store does not
	// parse it, so it does not depend on the advice package.
	Steps string
}

// SavePipelineRun records a run. Model, Summary and Steps must not be blank.
func (r *Repo) SavePipelineRun(ctx context.Context, model, summary, steps string) (PipelineRun, error) {
	if model == "" || summary == "" || steps == "" {
		return PipelineRun{}, invalid("a pipeline run needs a model, a summary and its steps")
	}
	const query = `
		INSERT INTO pipeline_run (created_at, model, summary, steps) VALUES (?, ?, ?, ?)
		RETURNING id, created_at, model, summary, steps`
	row := r.q.QueryRowContext(ctx, query, toMS(r.now()), model, summary, steps)
	return scanPipelineRun(row)
}

// ListPipelineRuns returns every run, oldest first; the last one is the current version.
func (r *Repo) ListPipelineRuns(ctx context.Context) ([]PipelineRun, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, created_at, model, summary, steps FROM pipeline_run ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PipelineRun
	for rows.Next() {
		run, err := scanPipelineRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// GetPipelineRun returns one run by id, or ErrNotFound.
func (r *Repo) GetPipelineRun(ctx context.Context, id int64) (PipelineRun, error) {
	row := r.q.QueryRowContext(ctx, `SELECT id, created_at, model, summary, steps FROM pipeline_run WHERE id = ?`, id)
	return scanPipelineRun(row)
}

func scanPipelineRun(s scanner) (PipelineRun, error) {
	var run PipelineRun
	var createdAt int64
	err := s.Scan(&run.ID, &createdAt, &run.Model, &run.Summary, &run.Steps)
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineRun{}, ErrNotFound
	}
	if err != nil {
		return PipelineRun{}, err
	}
	run.CreatedAt = fromMS(createdAt)
	return run, nil
}
