package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// bg is the context used by test helpers that talk to the database.
var bg = context.Background()

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mem(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRowContext(bg, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func tableNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(bg, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

func TestOpenCreatesSchema(t *testing.T) {
	s, _ := openTemp(t)

	want := []string{"credential", "engagement", "finding", "host", "journal_entry", "model_usage", "output_blob", "pipeline_run", "service", "web_path"}
	if got := tableNames(t, s); !slices.Equal(got, want) {
		t.Errorf("tables = %v, want %v", got, want)
	}

	embedded, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations(embedded)
	if err != nil {
		t.Fatalf("embedded migrations are invalid: %v", err)
	}
	if got := userVersion(t, s); got != len(migrations) {
		t.Errorf("user_version = %d, want %d (the number of embedded migrations)", got, len(migrations))
	}
}

func TestOpenPragmas(t *testing.T) {
	s, _ := openTemp(t)

	tests := []struct {
		pragma string
		want   string
	}{
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
		{"busy_timeout", "5000"},
	}
	for _, tt := range tests {
		var got string
		if err := s.db.QueryRowContext(bg, "PRAGMA "+tt.pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

// The database holds credentials: the file and its WAL companions must never be readable by others.
func TestDatabaseFilesArePrivate(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.db.ExecContext(bg, "INSERT INTO engagement (name, created_at) VALUES ('x', 1)"); err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		st, err := os.Stat(f)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
			continue
		}
		if got := st.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(f), got)
		}
	}
}

func TestOpenKeepsExistingFilePermissionsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after reopen = %o, want 600", got)
	}
}

func TestReopenIsANoOpAndKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(bg, "INSERT INTO engagement (name, created_at) VALUES ('box', 1)"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	var name string
	if err := s.db.QueryRowContext(bg, "SELECT name FROM engagement").Scan(&name); err != nil || name != "box" {
		t.Errorf("engagement after reopen = %q, %v; want box", name, err)
	}
	if matches, _ := filepath.Glob(path + ".bak-v*"); len(matches) != 0 {
		t.Errorf("a backup was created although the schema was already current: %v", matches)
	}
}

func TestFreshDatabaseHasNoBackup(t *testing.T) {
	_, path := openTemp(t)
	if matches, _ := filepath.Glob(path + ".bak-v*"); len(matches) != 0 {
		t.Errorf("fresh database produced backups: %v", matches)
	}
}

func TestOpenFailsWhenDirectoryIsMissing(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "nope", FileName))
	if err == nil || !strings.Contains(err.Error(), "create database") {
		t.Fatalf("err = %v, want a create database error", err)
	}
}

func TestOpenHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Open(ctx, filepath.Join(t.TempDir(), FileName))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestUpgradeBacksUpThenMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	v1 := map[string]string{"0001_init.sql": "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT) STRICT;"}
	v2 := map[string]string{
		"0001_init.sql":  v1["0001_init.sql"],
		"0002_extra.sql": "ALTER TABLE t ADD COLUMN extra TEXT NOT NULL DEFAULT '';",
	}

	s, err := open(context.Background(), path, mem(v1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(bg, "INSERT INTO t (v) VALUES ('keep me')"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// A stale file from an earlier upgrade must be replaced, not trusted.
	backup := path + ".bak-v1"
	if err := os.WriteFile(backup, []byte("stale junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err = open(context.Background(), path, mem(v2))
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer func() { _ = s.Close() }()

	if got := userVersion(t, s); got != 2 {
		t.Errorf("user_version = %d, want 2", got)
	}
	var v, extra string
	if err := s.db.QueryRowContext(bg, "SELECT v, extra FROM t").Scan(&v, &extra); err != nil || v != "keep me" {
		t.Errorf("data after upgrade = %q, %v; want the original row and the new column", v, err)
	}

	st, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("backup mode = %o, want 600", got)
	}
	b, err := sql.Open("sqlite", "file:"+backup+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	var n int
	if err := b.QueryRowContext(bg, "SELECT count(*) FROM t WHERE v = 'keep me'").Scan(&n); err != nil || n != 1 {
		t.Errorf("backup does not contain the version 1 data: n=%d err=%v", n, err)
	}
	// The backup is the old schema: it has no 'extra' column.
	if err := b.QueryRowContext(bg, "SELECT extra FROM t").Scan(&extra); err == nil {
		t.Error("backup already has the new column; it should be a copy of version 1")
	}
}

func TestFailedMigrationRollsBackCompletely(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	good := map[string]string{"0001_init.sql": "CREATE TABLE a (id INTEGER);"}
	bad := map[string]string{
		"0001_init.sql": good["0001_init.sql"],
		// Creates b, then fails on the duplicate a: b must not survive.
		"0002_bad.sql": "CREATE TABLE b (id INTEGER); CREATE TABLE a (id INTEGER);",
	}

	s, err := open(context.Background(), path, mem(good))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err = open(context.Background(), path, mem(bad))
	if err == nil || !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Fatalf("err = %v, want it to name the failing migration", err)
	}

	s, err = open(context.Background(), path, mem(good))
	if err != nil {
		t.Fatalf("reopen after failed upgrade: %v", err)
	}
	defer func() { _ = s.Close() }()
	if got := userVersion(t, s); got != 1 {
		t.Errorf("user_version = %d, want 1 (unchanged by the failed migration)", got)
	}
	if got := tableNames(t, s); !slices.Equal(got, []string{"a"}) {
		t.Errorf("tables = %v, want only [a]: the failed migration left partial changes", got)
	}
}

func TestDatabaseNewerThanBinaryIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	v2 := map[string]string{
		"0001_init.sql":  "CREATE TABLE a (id INTEGER);",
		"0002_extra.sql": "CREATE TABLE b (id INTEGER);",
	}
	s, err := open(context.Background(), path, mem(v2))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err = open(context.Background(), path, mem(map[string]string{"0001_init.sql": v2["0001_init.sql"]}))
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v, want ErrSchemaTooNew", err)
	}
	if !strings.Contains(err.Error(), "version 2") {
		t.Errorf("error %q does not say which version the database is at", err)
	}
	if matches, _ := filepath.Glob(path + ".bak-v*"); len(matches) != 0 {
		t.Errorf("a rejected open must not touch the database or make backups: %v", matches)
	}
}

func TestLoadMigrations(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantN   int
		wantErr string
	}{
		{"empty", map[string]string{}, 0, ""},
		{"one", map[string]string{"0001_init.sql": "x"}, 1, ""},
		{"consecutive", map[string]string{"0001_a.sql": "x", "0002_b.sql": "y", "0003_c.sql": "z"}, 3, ""},
		{"non-sql files are ignored", map[string]string{"0001_a.sql": "x", "README.md": "docs"}, 1, ""},
		{"gap", map[string]string{"0001_a.sql": "x", "0003_c.sql": "z"}, 0, "expected version 2"},
		{"does not start at one", map[string]string{"0002_b.sql": "y"}, 0, "expected version 1"},
		{"duplicate version", map[string]string{"0001_a.sql": "x", "0001_b.sql": "y"}, 0, "expected version 2"},
		{"no number", map[string]string{"init.sql": "x"}, 0, "must look like"},
		{"zero version", map[string]string{"0000_init.sql": "x"}, 0, "must look like"},
		{"missing description", map[string]string{"0001.sql": "x"}, 0, "must look like"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadMigrations(mem(tt.files))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.wantN {
				t.Errorf("got %d migrations, want %d", len(got), tt.wantN)
			}
			for i, m := range got {
				if m.version != i+1 {
					t.Errorf("migration %d has version %d", i, m.version)
				}
			}
		})
	}
}

// --- Schema behaviour -------------------------------------------------------------------

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(bg, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(bg, query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func seedHost(t *testing.T, s *Store) {
	t.Helper()
	mustExec(t, s, "INSERT INTO host (id, ip, first_seen, last_seen) VALUES (1, '10.0.0.1', 1, 1)")
	mustExec(t, s, "INSERT INTO service (id, host_id, port, proto) VALUES (1, 1, 80, 'tcp')")
	mustExec(t, s, "INSERT INTO web_path (service_id, path) VALUES (1, '/admin')")
}

func TestSchemaRejectsInvalidData(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"service with unknown host", "INSERT INTO service (host_id, port, proto) VALUES (99, 80, 'tcp')"},
		{"web path with unknown service", "INSERT INTO web_path (service_id, path) VALUES (99, '/')"},
		{"port zero", "INSERT INTO service (host_id, port, proto) VALUES (1, 0, 'tcp')"},
		{"port too large", "INSERT INTO service (host_id, port, proto) VALUES (1, 65536, 'tcp')"},
		{"unknown protocol", "INSERT INTO service (host_id, port, proto) VALUES (1, 80, 'icmp')"},
		{"duplicate service", "INSERT INTO service (host_id, port, proto) VALUES (1, 80, 'tcp')"},
		{"duplicate host", "INSERT INTO host (ip, first_seen, last_seen) VALUES ('10.0.0.1', 1, 1)"},
		{"duplicate web path", "INSERT INTO web_path (service_id, path) VALUES (1, '/admin')"},
		{"text in an integer column", "INSERT INTO host (ip, first_seen, last_seen) VALUES ('10.0.0.2', 'yesterday', 1)"},
		{"unknown severity", "INSERT INTO finding (severity, title, created_at) VALUES ('urgent', 't', 1)"},
		{"unknown journal author", "INSERT INTO journal_entry (ts, author, text) VALUES (1, 'bot', 't')"},
		{"second engagement row", "INSERT INTO engagement (id, name, created_at) VALUES (2, 'second', 1)"},
		{"negative blob size", "INSERT INTO output_blob (cmd, size, started_at) VALUES ('c', -1, 1)"},
		{"host without ip", "INSERT INTO host (first_seen, last_seen) VALUES (1, 1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			seedHost(t, s)
			if _, err := s.db.ExecContext(bg, tt.query); err == nil {
				t.Errorf("expected a constraint error for: %s", tt.query)
			}
		})
	}
}

func TestSchemaAcceptsValidData(t *testing.T) {
	s, _ := openTemp(t)
	seedHost(t, s)

	valid := []string{
		"INSERT INTO service (host_id, port, proto) VALUES (1, 80, 'udp')", // same port, other protocol
		"INSERT INTO service (host_id, port, proto) VALUES (1, 8080, 'tcp')",
		"INSERT INTO web_path (service_id, path) VALUES ((SELECT id FROM service WHERE port = 8080), '/admin')", // same path, other service
		"INSERT INTO finding (severity, title, created_at) VALUES ('critical', 't', 1)",
		"INSERT INTO journal_entry (ts, author, text) VALUES (1, 'ai', 't')",
		"INSERT INTO journal_entry (ts, author, text) VALUES (2, 'human', 't')",
		"INSERT INTO engagement (id, name, created_at) VALUES (1, 'box', 1)",
		"INSERT INTO credential (username, secret, created_at) VALUES ('root', 'toor', 1)",
	}
	for _, q := range valid {
		if _, err := s.db.ExecContext(bg, q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestDeletingAHostCascades(t *testing.T) {
	s, _ := openTemp(t)
	seedHost(t, s)

	mustExec(t, s, "DELETE FROM host WHERE id = 1")
	if n := count(t, s, "SELECT count(*) FROM service"); n != 0 {
		t.Errorf("services left after deleting the host: %d", n)
	}
	if n := count(t, s, "SELECT count(*) FROM web_path"); n != 0 {
		t.Errorf("web paths left after deleting the host: %d", n)
	}
}

func TestDeletingABlobKeepsEntitiesAndClearsTheLink(t *testing.T) {
	s, _ := openTemp(t)
	mustExec(t, s, "INSERT INTO output_blob (id, cmd, started_at) VALUES (1, 'nmap x', 1)")
	mustExec(t, s, "INSERT INTO host (id, ip, first_seen, last_seen, source_blob_id) VALUES (1, '10.0.0.1', 1, 1, 1)")
	mustExec(t, s, "INSERT INTO service (id, host_id, port, proto, source_blob_id) VALUES (1, 1, 80, 'tcp', 1)")
	mustExec(t, s, "INSERT INTO web_path (service_id, path, source_blob_id) VALUES (1, '/admin', 1)")
	mustExec(t, s, "INSERT INTO credential (username, created_at, source_blob_id) VALUES ('root', 1, 1)")
	mustExec(t, s, "INSERT INTO finding (severity, title, created_at, source_blob_id) VALUES ('low', 't', 1, 1)")
	mustExec(t, s, "INSERT INTO journal_entry (ts, author, text, source_blob_id) VALUES (1, 'ai', 't', 1)")

	mustExec(t, s, "DELETE FROM output_blob WHERE id = 1")

	// Evidence is gone, but nothing that pointed at it may disappear with it.
	for _, table := range []string{"host", "service", "web_path", "credential", "finding", "journal_entry"} {
		if n := count(t, s, "SELECT count(*) FROM "+table); n != 1 {
			t.Errorf("%s: %d rows after deleting the blob, want 1 (the row must survive)", table, n)
		}
		if n := count(t, s, "SELECT count(*) FROM "+table+" WHERE source_blob_id IS NULL"); n != 1 {
			t.Errorf("%s: source_blob_id was not cleared", table)
		}
	}
}

func TestUpsertByNaturalKey(t *testing.T) {
	s, _ := openTemp(t)
	for _, lastSeen := range []int{1, 2, 3} {
		mustExec(t, s, `INSERT INTO host (ip, os, first_seen, last_seen) VALUES ('10.0.0.1', 'Linux', ?, ?)
			ON CONFLICT(ip) DO UPDATE SET last_seen = excluded.last_seen`, lastSeen, lastSeen)
	}
	if n := count(t, s, "SELECT count(*) FROM host"); n != 1 {
		t.Errorf("rows = %d, want 1: a repeated scan must update, not duplicate", n)
	}
	if got := count(t, s, "SELECT last_seen FROM host"); got != 3 {
		t.Errorf("last_seen = %d, want 3", got)
	}
	if got := count(t, s, "SELECT first_seen FROM host"); got != 1 {
		t.Errorf("first_seen = %d, want 1 (unchanged by the upsert)", got)
	}
}
