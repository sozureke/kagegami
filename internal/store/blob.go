package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

// OutputBlob is a command that was run.
type OutputBlob struct {
	ID         int64
	Cmd        string
	ExitCode   *int
	Size       int64
	SHA256     string
	StartedAt  time.Time
	FinishedAt *time.Time
}

const blobColumns = `id, cmd, exit_code, size, sha256, started_at, finished_at`

// CreateBlob records that a command has started.
func (r *Repo) CreateBlob(ctx context.Context, cmd string) (OutputBlob, error) {
	if strings.TrimSpace(cmd) == "" {
		return OutputBlob{}, invalid("command must not be blank")
	}
	row := r.q.QueryRowContext(ctx,
		`INSERT INTO output_blob (cmd, started_at) VALUES (?, ?) RETURNING `+blobColumns, cmd, toMS(r.now()))
	return scanBlob(row)
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FinishBlob records the exit code, size and SHA-256 (lower-case hex) of a command's output. A blob can be finished only once.
func (r *Repo) FinishBlob(ctx context.Context, id int64, exitCode int, size int64, sha256 string) (OutputBlob, error) {
	if size < 0 {
		return OutputBlob{}, invalid("size %d", size)
	}
	if !sha256Hex.MatchString(sha256) {
		return OutputBlob{}, invalid("sha256 %q must be 64 lower-case hex digits", sha256)
	}

	row := r.q.QueryRowContext(ctx, `
UPDATE output_blob SET exit_code = ?, size = ?, sha256 = ?, finished_at = ?
WHERE id = ? AND finished_at IS NULL
RETURNING `+blobColumns, exitCode, size, sha256, toMS(r.now()), id)
	b, err := scanBlob(row)
	if errors.Is(err, ErrNotFound) {
		// Nothing was updated: either there is no such blob, or it is already finished.
		if _, getErr := r.GetBlob(ctx, id); getErr == nil {
			return OutputBlob{}, invalid("blob %d is already finished", id)
		}
	}
	return b, err
}

// GetBlob returns one blob, or ErrNotFound.
func (r *Repo) GetBlob(ctx context.Context, id int64) (OutputBlob, error) {
	return scanBlob(r.q.QueryRowContext(ctx, `SELECT `+blobColumns+` FROM output_blob WHERE id = ?`, id))
}

func scanBlob(s scanner) (OutputBlob, error) {
	var b OutputBlob
	var exit, finished sql.NullInt64
	var started int64
	err := s.Scan(&b.ID, &b.Cmd, &exit, &b.Size, &b.SHA256, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return OutputBlob{}, ErrNotFound
	}
	if err != nil {
		return OutputBlob{}, err
	}
	b.StartedAt = fromMS(started)
	if exit.Valid {
		code := int(exit.Int64)
		b.ExitCode = &code
	}
	if finished.Valid {
		t := fromMS(finished.Int64)
		b.FinishedAt = &t
	}
	return b, nil
}
