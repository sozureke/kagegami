package tmux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrCaptureUnsupported means that on this tmux, pipe-pane does not hand raw output markers through, so command capture cannot work (ADR-0006).
var ErrCaptureUnsupported = errors.New("tmux does not hand raw output markers through pipe-pane")

// CheckOptions configure VerifyCapture.
type CheckOptions struct {
	// Binary is the tmux executable; empty means "tmux" found in PATH.
	Binary string
	// Deadline is how long the first attempt waits for the marker; the retry waits twice as
	// long. Zero means 5 seconds. The wait only costs time when the check fails.
	Deadline time.Duration
}

const (
	defaultCheckDeadline = 5 * time.Second
	checkPollInterval    = 25 * time.Millisecond
	// A unix socket path is limited to about 104 bytes on macOS and 108 on Linux.
	maxSocketPath = 90
)

// VerifyCapture checks the property Kagegami's capture depends on, on the tmux that will
// really be used: that pipe-pane delivers a raw OSC marker, unchanged.
func VerifyCapture(ctx context.Context, o CheckOptions) error {
	deadline := o.Deadline
	if deadline <= 0 {
		deadline = defaultCheckDeadline
	}

	var got string
	wait := deadline
	for range 2 {
		ok, seen, err := checkOnce(ctx, o.Binary, wait)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		got = seen
		wait *= 2
	}

	version, _ := ExecRunner{Binary: o.Binary}.Run(ctx, "-V")
	return fmt.Errorf("%w (%s): the marker did not arrive within %s, nor within %s on a second try; the pipe delivered %s",
		ErrCaptureUnsupported, version, deadline, 2*deadline, describe(got))
}

// describe says what reached the pipe, shortened.
func describe(got string) string {
	if got == "" {
		return "nothing"
	}
	const shown = 80
	if len(got) > shown {
		return fmt.Sprintf("%d bytes starting %q", len(got), got[:shown])
	}
	return fmt.Sprintf("%d bytes: %q", len(got), got)
}

// checkOnce runs the check once on a fresh private server.
func checkOnce(ctx context.Context, binary string, wait time.Duration) (ok bool, got string, err error) {
	dir, err := os.MkdirTemp("", "kgc")
	if err != nil {
		return false, "", fmt.Errorf("create a temporary directory for the capture check: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	sock := filepath.Join(dir, "s")
	if len(sock) > maxSocketPath {
		return false, "", fmt.Errorf("the temporary directory path is too long for a unix socket: %s", sock)
	}
	runner := ExecRunner{Binary: binary, GlobalArgs: []string{"-S", sock, "-f", "/dev/null"}, Env: CleanEnv()}

	// The server goes away whatever happens, also when ctx was cancelled.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = runner.Run(cleanup, "kill-server")
	}()

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return false, "", fmt.Errorf("cannot make a random marker: %w", err)
	}
	token := hex.EncodeToString(raw) // only hex digits, so it is safe inside the command below
	marker := []byte("\x1b]7771;V;" + token + "\x07")
	out := filepath.Join(dir, "out")

	// The pane waits for a key, so the pipe is surely open before it prints the marker.
	paneCmd := `stty -echo; read x; printf '\033]7771;V;` + token + `\007'; sleep 30`
	answer, err := runner.Run(ctx, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "check", "-x", "80", "-y", "24", paneCmd)
	if err != nil {
		return false, "", fmt.Errorf("cannot start a temporary tmux server for the capture check: %w", err)
	}
	pane, err := ParsePaneID(answer)
	if err != nil {
		return false, "", err
	}

	if err := New(runner, func(string) string { return "" }).PipePane(ctx, pane, "cat >> "+ShellQuote(out)); err != nil {
		return false, "", fmt.Errorf("cannot open a pipe in the temporary pane: %w", err)
	}
	// Enter into a throwaway pane of a private server; this is not Client.SendText, which is reserved for the executor.
	if _, err := runner.Run(ctx, "send-keys", "-t", string(pane), "Enter"); err != nil {
		return false, "", fmt.Errorf("cannot start the marker in the temporary pane: %w", err)
	}

	end := time.Now().Add(wait)
	for {
		data, _ := os.ReadFile(out) //nolint:gosec // a file in the temporary directory created above
		if bytes.Contains(data, marker) {
			return true, "", nil
		}
		if time.Now().After(end) {
			return false, string(data), nil
		}
		select {
		case <-ctx.Done():
			return false, "", ctx.Err()
		case <-time.After(checkPollInterval):
		}
	}
}
