package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/sozureke/kagegami/internal/secret"
)

// Credential kinds. The database enforces the same list.
var credentialKinds = []string{"password", "hash", "key", "token", "other"}

// Credential is a username and secret found during the engagement.
type Credential struct {
	ID           int64
	Username     string
	Secret       secret.String
	Kind         string
	WorksOn      string
	SourceBlobID *int64
	CreatedAt    time.Time
}

// UpsertCredential stores a credential, or returns the existing one with the same username, secret and kind.
func (r *Repo) UpsertCredential(ctx context.Context, c Credential) (Credential, error) {
	if c.Username == "" && c.Secret.IsEmpty() {
		return Credential{}, invalid("a credential needs a username or a secret")
	}
	if c.Kind == "" {
		c.Kind = "password"
	}
	if !slices.Contains(credentialKinds, c.Kind) {
		return Credential{}, invalid("credential kind %q (expected one of %v)", c.Kind, credentialKinds)
	}

	const query = `
		INSERT INTO credential (username, secret, kind, works_on, source_blob_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(username, secret, kind) DO UPDATE SET
			works_on       = COALESCE(NULLIF(excluded.works_on, ''), credential.works_on),
			source_blob_id = COALESCE(excluded.source_blob_id, credential.source_blob_id)
		RETURNING ` + credentialColumns

	row := r.q.QueryRowContext(ctx, query, c.Username, c.Secret.Reveal(), c.Kind, c.WorksOn, c.SourceBlobID, toMS(r.now()))
	return scanCredential(row)
}

// ListCredentials returns every credential in the order it was found.
func (r *Repo) ListCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT `+credentialColumns+` FROM credential ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const credentialColumns = `id, username, secret, kind, works_on, source_blob_id, created_at` //nolint:gosec // a list of column names, not a credential

func scanCredential(s scanner) (Credential, error) {
	var c Credential
	var plain string
	var blob sql.NullInt64
	var created int64
	err := s.Scan(&c.ID, &c.Username, &plain, &c.Kind, &c.WorksOn, &blob, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, err
	}
	c.Secret = secret.New(plain)
	c.CreatedAt = fromMS(created)
	if blob.Valid {
		c.SourceBlobID = &blob.Int64
	}
	return c, nil
}
