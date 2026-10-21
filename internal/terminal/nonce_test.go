package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/sozureke/kagegami/pkg/tmux"
)

func TestNewNonce(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		n, err := NewNonce()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(n) {
			t.Fatalf("nonce %q is not 32 lower-case hex digits, the only form the hook accepts", n)
		}
		if seen[n] {
			t.Fatalf("nonce %q was made twice", n)
		}
		seen[n] = true
	}
}

func TestNonceFileName(t *testing.T) {
	for pane, want := range map[tmux.PaneID]string{"%0": "0.nonce", "%7": "7.nonce", "%123": "123.nonce"} {
		if got := NonceFileName(pane); got != want {
			t.Errorf("NonceFileName(%q) = %q, want %q", pane, got, want)
		}
	}
}

func TestWriteNonceFile(t *testing.T) {
	old := umask(0)
	defer umask(old)

	run := filepath.Join(t.TempDir(), "a", "run")
	f, err := WriteNonceFile(run, "%7", testNonce)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path() != filepath.Join(run, "7.nonce") {
		t.Errorf("path = %q", f.Path())
	}
	data, _ := os.ReadFile(f.Path())
	if string(data) != testNonce+"\n" {
		t.Errorf("content = %q", data)
	}
	if info, _ := os.Stat(f.Path()); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(run); info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700 even with umask 0", info.Mode().Perm())
	}

	if err := f.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the file is still there")
	}
	if err := f.Remove(); err != nil {
		t.Errorf("a second Remove failed: %v", err)
	}
}

const testNonce = "0123456789abcdef0123456789abcdef"

func TestWriteNonceFileReplacesWhatIsLeftOver(t *testing.T) {
	run := t.TempDir()
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(run, "3.nonce")

	t.Run("an old file", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // a deliberately loose old file
			t.Fatal(err)
		}
		if _, err := WriteNonceFile(run, "%3", testNonce); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		info, _ := os.Stat(path)
		if string(data) != testNonce+"\n" || info.Mode().Perm() != 0o600 {
			t.Errorf("content %q mode %v", data, info.Mode().Perm())
		}
	})
	t.Run("a link is replaced, its target is left alone", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "precious")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(path)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteNonceFile(run, "%3", testNonce); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(target); string(data) != "keep" {
			t.Errorf("the link was followed: the target now holds %q", data)
		}
		if info, _ := os.Lstat(path); info.Mode()&os.ModeSymlink != 0 {
			t.Error("the link is still there")
		}
	})
}

func TestWriteNonceFileRefusesAnUnsafeDirectory(t *testing.T) {
	t.Run("open to others", func(t *testing.T) {
		run := t.TempDir()
		if err := os.Chmod(run, 0o755); err != nil { //nolint:gosec // the point of the test
			t.Fatal(err)
		}
		if _, err := WriteNonceFile(run, "%1", testNonce); !errors.Is(err, ErrUnsafeDir) {
			t.Errorf("err = %v, want ErrUnsafeDir", err)
		}
		if _, err := os.Stat(filepath.Join(run, "1.nonce")); err == nil {
			t.Error("the nonce was written into a directory others can read")
		}
	})
	t.Run("group readable", func(t *testing.T) {
		run := t.TempDir()
		if err := os.Chmod(run, 0o750); err != nil { //nolint:gosec // the point of the test
			t.Fatal(err)
		}
		if _, err := WriteNonceFile(run, "%1", testNonce); !errors.Is(err, ErrUnsafeDir) {
			t.Errorf("err = %v, want ErrUnsafeDir", err)
		}
	})
	t.Run("a symbolic link to a directory", func(t *testing.T) {
		target := t.TempDir()
		if err := os.Chmod(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "run")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteNonceFile(link, "%1", testNonce); !errors.Is(err, ErrUnsafeDir) {
			t.Errorf("err = %v, want ErrUnsafeDir", err)
		}
	})
	t.Run("a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "run")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteNonceFile(file, "%1", testNonce); err == nil {
			t.Error("a file was accepted as the run directory")
		}
	})
}

func TestWriteNonceFileRefusesAnInvalidPane(t *testing.T) {
	run := t.TempDir()
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, pane := range []tmux.PaneID{"", "7", "%", "%a", "%1;x", "../x", "%-1"} {
		_, err := WriteNonceFile(run, pane, testNonce)
		if err == nil || errors.Is(err, ErrUnsafeDir) {
			t.Errorf("pane %q: err = %v, want an error about the pane", pane, err)
		}
	}
	if entries, _ := os.ReadDir(run); len(entries) != 0 {
		t.Errorf("files were created for invalid panes: %v", entries)
	}
}

func TestWriteNonceFileRefusesADirectoryOfAnotherUser(t *testing.T) {
	run := t.TempDir()
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	old := getuid
	getuid = func() int { return os.Getuid() + 1 }
	defer func() { getuid = old }()
	if _, err := WriteNonceFile(run, "%1", testNonce); !errors.Is(err, ErrUnsafeDir) {
		t.Errorf("err = %v, want ErrUnsafeDir", err)
	}
}
