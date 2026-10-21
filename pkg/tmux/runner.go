package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var (
	ErrNoServer = errors.New("no tmux server is running")
	ErrNotFound = errors.New("tmux target not found")
)

type CommandError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("tmux %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *CommandError) Unwrap() error { return e.Err }

// Runner runs one tmux command and returns what it printed.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout string, err error)
}

// ExecRunner runs the tmux binary. The arguments go to the process as a list and never
// through a shell.
type ExecRunner struct {
	// Binary is the tmux executable; empty means "tmux" found in PATH.
	Binary string
	// GlobalArgs come before every command: "-S", "/path/to/socket" selects a server by
	// its socket, "-f", "/dev/null" starts a server without a configuration file.
	GlobalArgs []string
	// Env is the environment of the process. Nil means the environment of this program.
	// A private server must not inherit TMUX and TMUX_PANE, see CleanEnv.
	Env []string
}

// CleanEnv returns the current environment without TMUX and TMUX_PANE. A program that runs
// inside tmux has both set, and tmux would use them to find (and talk to) the user's
// server instead of the private one that was asked for.
func CleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// Run implements Runner. A missing server and a missing target are reported as
// ErrNoServer and ErrNotFound, wrapped in a *CommandError that keeps tmux's own message.
// Cancelling ctx stops the command and returns the context's error.
func (r ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	binary := r.Binary
	if binary == "" {
		binary = "tmux"
	}
	full := append(append([]string{}, r.GlobalArgs...), args...)

	cmd := exec.CommandContext(ctx, binary, full...) //nolint:gosec // no shell is involved; the binary is chosen by the caller
	cmd.Env = r.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", &CommandError{Args: args, Stderr: stderr.String(), Err: classify(stderr.String(), err)}
	}
	return strings.TrimSuffix(stdout.String(), "\n"), nil
}

// classify maps the messages tmux prints to sentinel errors.
func classify(stderr string, err error) error {
	switch {
	case strings.Contains(stderr, "no server running"),
		strings.Contains(stderr, "error connecting to"),
		strings.Contains(stderr, "failed to connect to server"):
		return fmt.Errorf("%w: %w", ErrNoServer, err)
	case strings.Contains(stderr, "can't find"):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return err
}
