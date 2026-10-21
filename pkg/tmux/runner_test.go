package tmux

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerUsesTheGivenEnvironment(t *testing.T) {
	out, err := ExecRunner{Binary: "/usr/bin/env", Env: []string{"KG_ONE=1", "KG_TWO=two words"}}.Run(bg)
	if err != nil {
		t.Skipf("env is not at /usr/bin/env: %v", err)
	}
	if out != "KG_ONE=1\nKG_TWO=two words" {
		t.Errorf("environment = %q, want exactly the given one", out)
	}
}

func TestCleanEnvDropsTheTmuxVariables(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("KG_KEEP_ME", "yes")

	env := CleanEnv()
	var kept bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			t.Errorf("%q survived CleanEnv: a private server would find the user's tmux", kv)
		}
		if kv == "KG_KEEP_ME=yes" {
			kept = true
		}
	}
	if !kept {
		t.Error("CleanEnv removed variables it should keep")
	}
	if len(os.Getenv("TMUX")) == 0 {
		t.Error("CleanEnv must not change the process environment")
	}
}

func TestCleanEnvDoesNotDropSimilarNames(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "/somewhere")
	t.Setenv("TMUXINATOR", "1")
	found := map[string]bool{}
	for _, kv := range CleanEnv() {
		found[kv] = true
	}
	for _, kv := range []string{"TMUX_TMPDIR=/somewhere", "TMUXINATOR=1"} {
		if !found[kv] {
			t.Errorf("%s was removed: only TMUX and TMUX_PANE must go", kv)
		}
	}
}

func TestExecRunnerClassifiesErrors(t *testing.T) {
	r := ExecRunner{Binary: "/bin/sh"}
	run := func(stderr string) error {
		_, err := r.Run(bg, "-c", "echo "+ShellQuote(stderr)+" >&2; exit 1")
		return err
	}
	tests := []struct {
		stderr string
		want   error
	}{
		{"no server running on /tmp/tmux-1000/default", ErrNoServer},
		{"error connecting to /tmp/x (No such file or directory)", ErrNoServer},
		{"failed to connect to server", ErrNoServer},
		{"can't find pane: %99", ErrNotFound},
		{"can't find session: nope", ErrNotFound},
	}
	for _, tt := range tests {
		err := run(tt.stderr)
		var ce *CommandError
		if !errors.Is(err, tt.want) || !errors.As(err, &ce) {
			t.Errorf("stderr %q: err = %v, want a *CommandError wrapping %v", tt.stderr, err, tt.want)
			continue
		}
		if !strings.Contains(ce.Stderr, tt.stderr) || !strings.Contains(err.Error(), tt.stderr) {
			t.Errorf("tmux's own message is lost: %v", err)
		}
	}

	other := run("something unexpected")
	if errors.Is(other, ErrNoServer) || errors.Is(other, ErrNotFound) {
		t.Errorf("an unknown message must not be classified: %v", other)
	}
	if other == nil || !strings.Contains(other.Error(), "something unexpected") {
		t.Errorf("err = %v", other)
	}
}

func TestExecRunnerReturnsTheOutputWithoutTheFinalNewline(t *testing.T) {
	r := ExecRunner{Binary: "/bin/sh"}
	for script, want := range map[string]string{
		"printf 'a\\n'":     "a",
		"printf 'a\\n\\n'":  "a\n", // only one newline is removed
		"printf 'a b\\nc'":  "a b\nc",
		"printf ''":         "",
		"printf '  x  \\n'": "  x  ", // spaces are data
	} {
		got, err := r.Run(bg, "-c", script)
		if err != nil || got != want {
			t.Errorf("%s -> %q, %v; want %q", script, got, err, want)
		}
	}
}

func TestExecRunnerPassesGlobalArgsFirst(t *testing.T) {
	out, err := ExecRunner{Binary: "/bin/echo", GlobalArgs: []string{"-n", "global"}}.Run(bg, "then", "args")
	if err != nil || out != "global then args" {
		t.Errorf("out = %q, %v", out, err)
	}
}

func TestExecRunnerDoesNotUseAShell(t *testing.T) {
	out, err := ExecRunner{Binary: "/bin/echo"}.Run(bg, "$HOME", "a;b", "$(id)", "`id`", "*")
	if err != nil {
		t.Fatal(err)
	}
	if out != "$HOME a;b $(id) `id` *" {
		t.Errorf("out = %q: the arguments were interpreted by a shell", out)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	_, err := ExecRunner{Binary: "/no/such/tmux"}.Run(bg, "-V")
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *CommandError", err)
	}
	if errors.Is(err, ErrNoServer) || errors.Is(err, ErrNotFound) {
		t.Errorf("a missing binary is not 'no server' or 'not found': %v", err)
	}
}

func TestExecRunnerStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(bg, 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := ExecRunner{Binary: "/bin/sleep"}.Run(ctx, "30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context's error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v: the command was not stopped", elapsed)
	}

	cancelled, stop := context.WithCancel(bg)
	stop()
	if _, err := (ExecRunner{Binary: "/bin/sleep"}).Run(cancelled, "30"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestCommandErrorMessage(t *testing.T) {
	withStderr := &CommandError{Args: []string{"kill-pane", "-t", "%1"}, Stderr: "can't find pane: %1\n", Err: errors.New("exit status 1")}
	if got, want := withStderr.Error(), "tmux kill-pane -t %1: can't find pane: %1"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	silent := &CommandError{Args: []string{"x"}, Err: errors.New("exit status 2")}
	if got := silent.Error(); got != "tmux x: exit status 2" {
		t.Errorf("Error() = %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	for _, s := range []string{
		"", "plain", "with space", "it's", "'", "''", `"double"`, "$HOME", "$(id)", "`id`", "a;b", "a|b", "a&b",
		"a\nb", "tab\there", `back\slash`, "*", "?", "~", "#comment", "unicode ✓ \U0001F680", "-n", "--", "!history",
	} {
		r := ExecRunner{Binary: "/bin/sh"}
		got, err := r.Run(bg, "-c", "printf %s "+ShellQuote(s))
		if err != nil {
			t.Fatalf("ShellQuote(%q): %v", s, err)
		}
		if got != strings.TrimSuffix(s, "\n") && s != "" {
			t.Errorf("ShellQuote(%q) came back as %q", s, got)
		}
	}
}
