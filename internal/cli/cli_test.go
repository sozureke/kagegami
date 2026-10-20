package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

var testInfo = BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-01-02T03:04:05Z"}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut, testInfo)
	return code, out.String(), errOut.String()
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring
		wantStderr string // substring
	}{
		{"no args prints help", nil, 0, "Available Commands", ""},
		{"help flag", []string{"--help"}, 0, "watch", ""},
		{"unknown command", []string{"foo"}, 2, "", "unknown command"},
		{"unknown flag", []string{"watch", "--bogus"}, 2, "", "unknown flag"},
		{"extra argument", []string{"version", "extra"}, 2, "", "unknown command"},
		{"stub is not implemented", []string{"watch"}, 1, "", "not implemented yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestStubWritesNothingToStdout(t *testing.T) {
	_, stdout, _ := runCLI(t, "watch")
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestVersion(t *testing.T) {
	code, stdout, stderr := runCLI(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	want := "kagegami 1.2.3 (commit abc1234, built 2026-01-02T03:04:05Z)\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestUsageErrorUnwrap(t *testing.T) {
	base := errors.New("boom")
	// Wrapped twice, the way a caller higher up the stack might add context.
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", &usageError{err: base}))

	var ue *usageError
	if !errors.As(wrapped, &ue) {
		t.Fatal("errors.As did not find *usageError in a wrapped chain")
	}
	if !errors.Is(wrapped, base) {
		t.Error("errors.Is did not reach the underlying error: Unwrap is broken")
	}
}

func TestHelpTextMatchesCommands(t *testing.T) {
	root := newRootCmd(io.Discard, io.Discard, testInfo)

	registered := map[string]bool{}
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
		if cmd.Short == "" {
			t.Errorf("command %q has no short description in help.yaml", cmd.Name())
		}
	}
	for name := range help.Commands {
		if !registered[name] {
			t.Errorf("help.yaml describes %q, but there is no such command", name)
		}
	}
	if help.Root.Short == "" || help.Root.Long == "" {
		t.Error("root short and long descriptions must not be empty")
	}
}
