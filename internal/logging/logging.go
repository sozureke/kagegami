package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sozureke/kagegami/internal/secret"
)

const (
	FileName = "kagegami.log"

	// MaxFileBytes is the size above which the log is rotated when it is opened.
	MaxFileBytes int64 = 10 << 20

	dirMode  = 0o700
	fileMode = 0o600
)

// ParseLevel converts the level names allowed in the configuration.
func ParseLevel(name string) (slog.Level, error) {
	switch name {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (expected debug, info, warn or error)", name)
}

// New returns a logger that writes JSON lines to w at or above level.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	}))
}

// Log is a logger that owns its file.
type Log struct {
	*slog.Logger
	f *os.File
}

// Open opens / creating it if needed the log file in dir for appending. An existing log
// larger than MaxFileBytes is first moved to <name>.1, replacing an older one, so the log
// cannot grow without bound. Directory and file are private to the user.
func Open(dir string, level slog.Level) (*Log, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(dir, FileName)

	if st, err := os.Stat(path); err == nil && st.Size() > MaxFileBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("rotate log: %w", err)
		}
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, fileMode) //nolint:gosec // built from the resolved data directory
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	// A log created earlier with looser permissions is tightened.
	if err := f.Chmod(fileMode); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("protect log: %w", err)
	}
	return &Log{Logger: New(f, level), f: f}, nil
}

// Close closes the log file.
func (l *Log) Close() error { return l.f.Close() }

// replaceAttr redacts secrets by name and writes times in UTC.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.TimeValue(t.UTC())
		}
		return a
	}
	if sensitive(a.Key) || slices.ContainsFunc(groups, sensitive) {
		return slog.String(a.Key, secret.Redacted)
	}
	return a
}

// sensitive reports whether an attribute key names a secret. Case, underscores, dashes
// and dots are ignored, so api_key, API-Key and apiKey are the same. "token" counts only
// at the end of a name: access_token is a secret, but "tokens" (a count of model tokens) and "token_count" are not.
func sensitive(key string) bool {
	k := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	for _, part := range []string{"password", "passwd", "passphrase", "secret", "apikey", "privatekey", "authorization", "cookie"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return strings.HasSuffix(k, "token")
}
