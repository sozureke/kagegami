package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// JournalEntry is one line of the engagement journal, written by a person or by the agent.
type JournalEntry struct {
	ID           int64
	Time         time.Time
	Author       string // "human" or "ai"
	Text         string
	SourceBlobID *int64
}

// AddJournal appends an entry stamped with the current time. Author must be "human" or "ai", and the text must not be blank.
func (r *Repo) AddJournal(ctx context.Context, e JournalEntry) (JournalEntry, error) {
	if e.Author != "human" && e.Author != "ai" {
		return JournalEntry{}, invalid("journal author %q (expected human or ai)", e.Author)
	}
	if strings.TrimSpace(e.Text) == "" {
		return JournalEntry{}, invalid("journal text must not be blank")
	}

	const query = `
		INSERT INTO journal_entry (ts, author, text, source_blob_id)
		VALUES (?, ?, ?, ?)
		RETURNING id, ts, author, text, source_blob_id`

	row := r.q.QueryRowContext(ctx, query, toMS(r.now()), e.Author, e.Text, e.SourceBlobID)
	return scanJournal(row)
}

// ListJournal returns the whole journal, oldest first.
func (r *Repo) ListJournal(ctx context.Context) ([]JournalEntry, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, ts, author, text, source_blob_id FROM journal_entry ORDER BY ts, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []JournalEntry
	for rows.Next() {
		e, err := scanJournal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanJournal(s scanner) (JournalEntry, error) {
	var e JournalEntry
	var ts int64
	var blob sql.NullInt64
	err := s.Scan(&e.ID, &ts, &e.Author, &e.Text, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return JournalEntry{}, ErrNotFound
	}
	if err != nil {
		return JournalEntry{}, err
	}
	e.Time = fromMS(ts)
	if blob.Valid {
		e.SourceBlobID = &blob.Int64
	}
	return e, nil
}
