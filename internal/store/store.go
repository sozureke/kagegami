package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// FileName is the name of the database file inside a case directory.
const FileName = "kagegami.db"

var ErrSchemaTooNew = errors.New("database schema is newer than this version of kagegami")

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	migrations, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return nil, err
	}

	return open(ctx, path, migrations)
}

func open(ctx context.Context, path string, migrations fs.FS) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path is built from the resolved case directory
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}

	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// no lock contention between our own goroutines. Do not run a query
	// while a transaction is open on another handle, it would wait for the connection.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(ctx, path, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries { // ReadDir returns entries sorted by filename
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v < 1 {
			return nil, fmt.Errorf("migration %q: name must look like 0001_description.sql", e.Name())
		}
		if v != len(out)+1 {
			return nil, fmt.Errorf("migration %q: expected version %d (versions must be consecutive from 1)", e.Name(), len(out)+1)
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	return out, nil
}

func (s *Store) migrate(ctx context.Context, path string, fsys fs.FS) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	latest := len(migrations)
	if current > latest {
		return fmt.Errorf("%w (database is at version %d, this build knows %d)", ErrSchemaTooNew, current, latest)
	}
	if current == latest {
		return nil
	}

	if current > 0 { // an existing database is about to change: keep a copy first
		if err := s.backup(ctx, path, current); err != nil {
			return err
		}
	}
	for _, m := range migrations[current:] {
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// backup writes a consistent copy of the database next to it.
func (s *Store) backup(ctx context.Context, path string, version int) error {
	dst := fmt.Sprintf("%s.bak-v%d", path, version)
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("replace old backup: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("back up database before migrating: %w", err)
	}
	return os.Chmod(dst, 0o600)
}
