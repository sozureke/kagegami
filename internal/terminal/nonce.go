package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sozureke/kagegami/pkg/tmux"
)

// getuid is a variable so that a test can pretend the directory belongs to someone else.
var getuid = os.Getuid

// ErrUnsafeDir is returned when the run directory can be reached by other users, or is not
// really a directory.
var ErrUnsafeDir = errors.New("unsafe run directory")

// NewNonce returns a fresh capture nonce: 128 random bits as 32 lower-case hex digits, the
// only form the shell hook accepts (ADR-0008).
func NewNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("cannot make a capture nonce: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// NonceFileName is the name, inside the run directory, of the file the hook reads for pane:
// for %7 it is 7.nonce. The hook builds the same name from $TMUX_PANE.
func NonceFileName(pane tmux.PaneID) string {
	return strings.TrimPrefix(string(pane), "%") + ".nonce"
}

// NonceFile is the file through which the shell hook learns the nonce of a pane.
type NonceFile struct {
	path string
}

// Path is where the file is.
func (f *NonceFile) Path() string { return f.path }

// WriteNonceFile makes the nonce of pane readable for the hook, in runDir. The directory is
// created with mode 0700 if it is missing, and refused if it is a symbolic link, belongs to
// someone else, or can be entered by group or others. The file is made with mode 0600 and a
// file or link left over from an earlier run is replaced, never followed.
func WriteNonceFile(runDir string, pane tmux.PaneID, nonce string) (*NonceFile, error) {
	if _, err := tmux.ParsePaneID(string(pane)); err != nil {
		return nil, err
	}
	if err := prepareRunDir(runDir); err != nil {
		return nil, err
	}

	path := filepath.Join(runDir, NonceFileName(pane))
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // inside the private run directory checked above
		if err == nil {
			_, werr := f.WriteString(nonce + "\n")
			cerr := f.Close()
			if err := errors.Join(werr, cerr); err != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("write the nonce file: %w", err)
			}
			return &NonceFile{path: path}, nil
		}
		if !errors.Is(err, fs.ErrExist) || attempt > 0 {
			return nil, fmt.Errorf("create the nonce file: %w", err)
		}
		// Left over from a run that did not clean up. Remove it (a link is removed, not
		// followed) and try once more.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove the old nonce file: %w", rerr)
		}
	}
}

// Remove deletes the file, which ends the watch for the hook. It is safe to call twice.
func (f *NonceFile) Remove() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the nonce file: %w", err)
	}
	return nil
}

// prepareRunDir creates the run directory if needed and checks that only the user can reach it.
func prepareRunDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the run directory: %w", err)
	}
	return checkPrivateDir(dir)
}

func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnsafeDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory (a symbolic link is not accepted)", ErrUnsafeDir, dir)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%w: %s has mode %04o, other users could reach it; use chmod 700", ErrUnsafeDir, dir, perm)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != getuid() {
		return fmt.Errorf("%w: %s belongs to another user", ErrUnsafeDir, dir)
	}
	return nil
}
