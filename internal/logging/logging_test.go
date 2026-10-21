package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sozureke/kagegami/internal/secret"
	"github.com/sozureke/kagegami/internal/store"
)

const canary = "CANARY-s3cr3t-Pa55w0rd-9f8e7d"

// records parses a buffer of JSON lines.
func records(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("not valid JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func logOnce(t *testing.T, level slog.Level, f func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	f(New(&buf, level))
	recs := records(t, buf.Bytes())
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %s", len(recs), buf.String())
	}
	return recs[0]
}

// --- ParseLevel -------------------------------------------------------------------------

func TestParseLevel(t *testing.T) {
	valid := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for name, want := range valid {
		got, err := ParseLevel(name)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
	for _, name := range []string{"", "Info", "DEBUG", "warning", "trace", "fatal", " info", "info ", "0"} {
		if _, err := ParseLevel(name); err == nil {
			t.Errorf("ParseLevel(%q) must fail: the configuration only allows the four exact names", name)
		}
	}
	if _, err := ParseLevel("loud"); err == nil || !strings.Contains(err.Error(), `"loud"`) {
		t.Errorf("the error should name the bad value: %v", err)
	}
}

// --- Format and levels ------------------------------------------------------------------

func TestRecordsAreJSONLines(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	l.Info("first", "n", 1)
	l.Warn("second", "ok", true, "name", "kagegami")

	if lines := strings.Count(buf.String(), "\n"); lines != 2 {
		t.Errorf("%d lines, want 2: each record is one line", lines)
	}
	recs := records(t, buf.Bytes())
	if recs[0]["msg"] != "first" || recs[0]["level"] != "INFO" || recs[0]["n"] != float64(1) {
		t.Errorf("first record = %v", recs[0])
	}
	if recs[1]["msg"] != "second" || recs[1]["level"] != "WARN" || recs[1]["ok"] != true || recs[1]["name"] != "kagegami" {
		t.Errorf("second record = %v", recs[1])
	}
}

func TestLevels(t *testing.T) {
	emit := func(l *slog.Logger) {
		l.Debug("d")
		l.Info("i")
		l.Warn("w")
		l.Error("e")
	}
	tests := []struct {
		level slog.Level
		want  []string
	}{
		{slog.LevelDebug, []string{"d", "i", "w", "e"}},
		{slog.LevelInfo, []string{"i", "w", "e"}},
		{slog.LevelWarn, []string{"w", "e"}},
		{slog.LevelError, []string{"e"}},
	}
	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			var buf bytes.Buffer
			emit(New(&buf, tt.level))
			var got []string
			for _, r := range records(t, buf.Bytes()) {
				got = append(got, r["msg"].(string))
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("level %v logged %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}

func TestTimeIsUTC(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("far-east", 9*3600+1800)
	t.Cleanup(func() { time.Local = old })

	rec := logOnce(t, slog.LevelInfo, func(l *slog.Logger) { l.Info("x") })
	ts, _ := rec["time"].(string)
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("time = %q, want UTC (ending in Z) even when the local zone is +09:30", ts)
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatalf("time %q does not parse: %v", ts, err)
	}
	if d := time.Since(parsed); d < -time.Minute || d > time.Minute {
		t.Errorf("time %q is %v from now: the zone was converted wrongly", ts, d)
	}
}

// --- Secrets ----------------------------------------------------------------------------

func TestSensitiveKeys(t *testing.T) {
	secrets := []string{
		"password", "Password", "PASSWORD", "user_password", "db-password", "passwd", "passphrase",
		"secret", "client_secret", "Secret", "api_key", "apiKey", "API-KEY", "api.key", "apikey",
		"private_key", "privateKey", "authorization", "Authorization", "cookie", "set-cookie", "Cookie",
		"token", "Token", "access_token", "refresh-token", "id.token", "bearerToken", "auth_token",
	}
	for _, key := range secrets {
		if !sensitive(key) {
			t.Errorf("%q must be treated as a secret key", key)
		}
	}
	plain := []string{
		"tokens", "max_tokens", "token_count", "tokenizer", "tokens_used", "input_tokens",
		"user", "host", "ip", "port", "cmd", "command", "sha256", "hash", "size", "id", "case",
		"key", "keys", "name", "path", "level", "msg", "time", "error", "err", "pass", "passport",
	}
	for _, key := range plain {
		if sensitive(key) {
			t.Errorf("%q must not be redacted: it is not a secret", key)
		}
	}
}

func TestSensitiveAttributesAreRedactedWhateverTheirType(t *testing.T) {
	values := map[string]any{
		"string":      canary,
		"empty":       "",
		"int":         123456,
		"bool":        true,
		"float":       3.14159,
		"slice":       []string{canary, "other"},
		"map":         map[string]string{"k": canary},
		"struct":      struct{ P string }{canary},
		"pointer":     &struct{ P string }{canary},
		"error":       errors.New(canary),
		"bytes":       []byte(canary),
		"duration":    90 * time.Second,
		"time":        time.Now(),
		"nil":         nil,
		"stringer":    fmt.Stringer(secretStringer(canary)),
		"typed":       secret.New(canary),
		"nested slog": slog.GroupValue(slog.String("inner", canary)),
	}
	for name, v := range values {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			New(&buf, slog.LevelInfo).Info("m", "password", v)
			if strings.Contains(buf.String(), canary) {
				t.Fatalf("leaked: %s", buf.String())
			}
			rec := records(t, buf.Bytes())[0]
			// A group value cannot be replaced as a whole (the handler writes its members
			// one by one), so there every member must be the marker.
			if group, ok := rec["password"].(map[string]any); ok {
				for k, v := range group {
					if v != secret.Redacted {
						t.Errorf("password.%s = %v, want the marker", k, v)
					}
				}
			} else if rec["password"] != secret.Redacted {
				t.Errorf("password = %v, want the marker", rec["password"])
			}
		})
	}
}

type secretStringer string

func (s secretStringer) String() string { return string(s) }

func TestNonSensitiveAttributesAreKept(t *testing.T) {
	rec := logOnce(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("m", "user", "root", "tokens", 1234, "token_count", 5, "sha256", "abc123", "cmd", "nmap -sV x")
	})
	want := map[string]any{"user": "root", "tokens": float64(1234), "token_count": float64(5), "sha256": "abc123", "cmd": "nmap -sV x"}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %v, want %v", k, rec[k], v)
		}
	}
}

func TestSensitiveGroupsAndNesting(t *testing.T) {
	cases := map[string]func(*slog.Logger){
		"group named like a secret": func(l *slog.Logger) { l.Info("m", slog.Group("password", "value", canary)) },
		"nested under a secret group": func(l *slog.Logger) {
			l.Info("m", slog.Group("password", slog.Group("inner", "deep", canary)))
		},
		"secret key inside a plain group": func(l *slog.Logger) { l.Info("m", slog.Group("auth", "password", canary)) },
		"WithGroup":                       func(l *slog.Logger) { l.WithGroup("secret").Info("m", "anything", canary) },
		"With a secret attribute":         func(l *slog.Logger) { l.With("api_key", canary).Info("m") },
		"With then a group":               func(l *slog.Logger) { l.With(slog.Group("token", "v", canary)).Info("m") },
		"WithGroup then With":             func(l *slog.Logger) { l.WithGroup("cookie").With("k", canary).Info("m") },
		"deeply nested key":               func(l *slog.Logger) { l.Info("m", slog.Group("a", slog.Group("b", slog.Group("c", "secret", canary)))) },
	}
	for name, emit := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			emit(New(&buf, slog.LevelInfo))
			if strings.Contains(buf.String(), canary) {
				t.Errorf("leaked: %s", buf.String())
			}
			if !strings.Contains(buf.String(), secret.Redacted) {
				t.Errorf("no marker in the output: %s", buf.String())
			}
		})
	}
}

func TestPlainGroupsAreKept(t *testing.T) {
	rec := logOnce(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("m", slog.Group("scan", "host", "10.0.0.1", "port", 22))
	})
	scan, _ := rec["scan"].(map[string]any)
	if scan["host"] != "10.0.0.1" || scan["port"] != float64(22) {
		t.Errorf("scan = %v", rec["scan"])
	}
}

func TestSecretStringRedactsItselfUnderAnyKey(t *testing.T) {
	rec := logOnce(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("m", "credential", secret.New(canary), "empty", secret.String{})
	})
	if rec["credential"] != secret.Redacted {
		t.Errorf("credential = %v, want the marker", rec["credential"])
	}
	if rec["empty"] != "" {
		t.Errorf("empty = %v, want an empty secret to stay empty", rec["empty"])
	}
}

// The safety net covers attribute keys, not message text: documented so nobody assumes more.
func TestMessagesAreNotScanned(t *testing.T) {
	rec := logOnce(t, slog.LevelInfo, func(l *slog.Logger) { l.Info("password check failed") })
	if rec["msg"] != "password check failed" {
		t.Errorf("msg = %v: messages are written as given; secrets must never be put in them", rec["msg"])
	}
}

// --- Open -------------------------------------------------------------------------------

func readLog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOpenCreatesAPrivateLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "logs")
	l, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hello", "n", 1)
	l.Debug("hidden")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, FileName): 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	recs := records(t, []byte(readLog(t, dir)))
	if len(recs) != 1 || recs[0]["msg"] != "hello" {
		t.Errorf("records = %v, want only the info line (the debug line is below the level)", recs)
	}
}

func TestOpenAppends(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		l, err := Open(dir, slog.LevelInfo)
		if err != nil {
			t.Fatal(err)
		}
		l.Info("run", "i", i)
		_ = l.Close()
	}
	recs := records(t, []byte(readLog(t, dir)))
	if len(recs) != 3 {
		t.Fatalf("%d records, want 3: reopening must append, not truncate", len(recs))
	}
	for i, r := range recs {
		if r["i"] != float64(i) {
			t.Errorf("record %d = %v", i, r)
		}
	}
}

func TestOpenTightensAnOpenLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil { //nolint:gosec // deliberately too open
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want an old world-readable log tightened to 600", st.Mode().Perm())
	}
	if !strings.HasPrefix(readLog(t, dir), "old\n") {
		t.Error("the existing content must be kept")
	}
}

func bigFile(t *testing.T, path string, size int64, marker string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(marker); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil { // sparse: no real disk use
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRotatesALargeLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	bigFile(t, path, MaxFileBytes+1, "OLD-LOG")

	l, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("fresh")
	_ = l.Close()

	rotated, err := os.Stat(path + ".1")
	if err != nil || rotated.Size() != MaxFileBytes+1 {
		t.Fatalf("the old log was not moved to .1: %v", err)
	}
	if st, _ := os.Stat(path); st.Size() > 1<<10 {
		t.Errorf("the new log is %d bytes: it should start empty", st.Size())
	}
	if strings.Contains(readLog(t, dir), "OLD-LOG") {
		t.Error("the new log still contains the old content")
	}
	if recs := records(t, []byte(readLog(t, dir))); len(recs) != 1 || recs[0]["msg"] != "fresh" {
		t.Errorf("records = %v", recs)
	}
}

func TestOpenRotationBoundary(t *testing.T) {
	for _, tt := range []struct {
		name    string
		size    int64
		rotates bool
	}{
		{"just under the limit", MaxFileBytes - 1, false},
		{"exactly the limit", MaxFileBytes, false},
		{"one byte over", MaxFileBytes + 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bigFile(t, filepath.Join(dir, FileName), tt.size, "x")
			l, err := Open(dir, slog.LevelInfo)
			if err != nil {
				t.Fatal(err)
			}
			_ = l.Close()
			_, statErr := os.Stat(filepath.Join(dir, FileName+".1"))
			if rotated := statErr == nil; rotated != tt.rotates {
				t.Errorf("rotated = %v, want %v", rotated, tt.rotates)
			}
		})
	}
}

func TestRotationReplacesTheOlderArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path+".1", []byte("ANCIENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	bigFile(t, path, MaxFileBytes+1, "RECENT")

	l, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()

	archive, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(archive, []byte("RECENT")) || bytes.Contains(archive, []byte("ANCIENT")) {
		t.Errorf("the archive should be the last log, only one archive is kept (starts %q)", archive[:min(len(archive), 10)])
	}
	if matches, _ := filepath.Glob(path + ".*"); len(matches) != 1 {
		t.Errorf("archives = %v, want exactly one", matches)
	}
}

func TestOpenFailures(t *testing.T) {
	t.Run("the directory cannot be created", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(filepath.Join(blocker, "logs"), slog.LevelInfo); err == nil || !strings.Contains(err.Error(), "log directory") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("rotation fails", func(t *testing.T) {
		dir := t.TempDir()
		bigFile(t, filepath.Join(dir, FileName), MaxFileBytes+1, "x")
		// a non-empty directory where the archive should go makes the rename fail
		archive := filepath.Join(dir, FileName+".1")
		if err := os.MkdirAll(filepath.Join(archive, "keep"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir, slog.LevelInfo); err == nil || !strings.Contains(err.Error(), "rotate") {
			t.Errorf("err = %v, want a rotation error", err)
		}
	})
	t.Run("the log path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir, slog.LevelInfo); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestLoggingAfterCloseDoesNotPanic(t *testing.T) {
	l, err := Open(t.TempDir(), slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l.Info("late") // a logger must never take the program down
}

// --- Concurrency ------------------------------------------------------------------------

func TestConcurrentLoggingProducesWholeLines(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines, each = 20, 200
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				l.Info("line", "g", g, "i", i, "pad", strings.Repeat("x", 100))
			}
		}()
	}
	wg.Wait()
	_ = l.Close()

	recs := records(t, []byte(readLog(t, dir)))
	if len(recs) != goroutines*each {
		t.Errorf("%d records, want %d", len(recs), goroutines*each)
	}
}

// The panel and one-off commands run as separate processes and share the log file.
func TestTwoOpenHandlesShareTheFile(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, l := range []*Log{a, b} {
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 100 {
					l.Info("line", "i", i, "pad", strings.Repeat("y", 200))
				}
			}()
		}
	}
	wg.Wait()
	_ = a.Close()
	_ = b.Close()

	if recs := records(t, []byte(readLog(t, dir))); len(recs) != 2*10*100 {
		t.Errorf("%d records, want %d", len(recs), 2*10*100)
	}
}

// --- End to end: a secret that goes through the store never reaches the log -------------

func TestCanaryNeverReachesTheLog(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), store.FileName)
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	repo := st.Repo()
	stored, err := repo.UpsertCredential(ctx, store.Credential{Username: "root", Secret: secret.New(canary), WorksOn: "ssh"})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	l, err := Open(dir, slog.LevelDebug)
	if err != nil {
		t.Fatal(err)
	}

	// every way a developer might plausibly log a credential
	l.Info("stored", "credential", stored)
	l.Info("stored", "credential", &stored)
	l.Info("listed", "credentials", listed)
	l.Info("secret only", "secret", stored.Secret)
	l.Info("secret only", "value", stored.Secret)
	l.Info("any", slog.Any("cred", stored))
	l.Info("group", slog.Group("cred", "user", stored.Username, "secret", stored.Secret))
	l.Warn("formatted", "text", fmt.Sprintf("%v %+v %#v", stored, stored, stored))
	l.Error("error", "err", fmt.Errorf("login failed for %v", stored))
	l.With("credential", stored).Debug("with")
	l.WithGroup("scan").Info("grouped", "credential", stored)
	// and the mistake the type cannot catch, the one the key-based net exists for
	l.Info("careless", "password", stored.Secret.Reveal())
	l.Info("careless", "api_key", stored.Secret.Reveal(), "access_token", stored.Secret.Reveal())
	l.Info("careless group", slog.Group("password", "value", stored.Secret.Reveal()))
	_ = l.Close()

	content := readLog(t, dir)
	if strings.Contains(content, canary) {
		t.Fatalf("the canary secret reached the log file:\n%s", content)
	}
	if !strings.Contains(content, secret.Redacted) {
		t.Error("no redaction marker in the log: the test did not log what it meant to")
	}
	if got := len(records(t, []byte(content))); got < 14 {
		t.Errorf("only %d records were written, want at least 14", got)
	}
}
