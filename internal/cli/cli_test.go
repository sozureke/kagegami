package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/pipeline"
	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/secret"
	"github.com/sozureke/kagegami/internal/store"
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

func TestHookCommand(t *testing.T) {
	t.Setenv("KAGEGAMI_HOME", "/kg")
	code, stdout, stderr := runCLI(t, "hook", "zsh")
	if code != 0 || stderr != "" {
		t.Fatalf("hook zsh: code %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "__kg_dir='/kg/run'") || strings.Contains(stdout, "@RUN_DIR@") {
		t.Errorf("the script does not name the run directory:\n%.300s", stdout)
	}

	for _, args := range [][]string{{"hook"}, {"hook", "zsh", "bash"}, {"hook", "fish"}} {
		code, stdout, stderr := runCLI(t, args...)
		if code != 2 || stdout != "" || stderr == "" {
			t.Errorf("%v: code %d, stdout %q, stderr %q; want a usage error (2) and nothing on stdout", args, code, stdout, stderr)
		}
	}

	t.Setenv("KAGEGAMI_HOME", "relative")
	if code, stdout, _ := runCLI(t, "hook", "zsh"); code == 0 || stdout != "" {
		t.Errorf("a bad KAGEGAMI_HOME must fail without printing a script: code %d", code)
	}
}

// caseWithHost makes a case named name that holds one host, one service and one credential.
func caseWithHost(t *testing.T, home, name string) {
	t.Helper()
	t.Setenv("KAGEGAMI_HOME", home)
	if code, _, stderr := runCLI(t, "init", "--case", name); code != 0 {
		t.Fatalf("init: %d %s", code, stderr)
	}
	st, err := store.Open(context.Background(), filepath.Join(home, "data", "cases", name, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	d := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.5", Hostname: "box.htb", Status: "up"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.5", Port: 22, Proto: "tcp", State: "open", Name: "ssh", Version: "OpenSSH 8.2"}},
	}
	if _, err := pipeline.Apply(context.Background(), st, d, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repo().UpsertCredential(context.Background(), store.Credential{Username: "admin", Secret: secret.New("topsecret-123"), Kind: "password"}); err != nil {
		t.Fatal(err)
	}
}

func TestMapCommand(t *testing.T) {
	home := t.TempDir()
	caseWithHost(t, home, "htb-box")

	t.Run("the only case is used, and secrets are masked", func(t *testing.T) {
		code, stdout, stderr := runCLI(t, "map")
		if code != 0 || stderr != "" {
			t.Fatalf("code %d, stderr %q", code, stderr)
		}
		for _, want := range []string{"10.0.0.5  box.htb", "22/tcp", "OpenSSH 8.2", "admin", "•••"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%q is missing from:\n%s", want, stdout)
			}
		}
		if strings.Contains(stdout, "topsecret-123") {
			t.Fatal("a secret was printed")
		}
	})
	t.Run("json", func(t *testing.T) {
		code, stdout, _ := runCLI(t, "map", "--json", "--case", "htb-box")
		var v struct {
			Hosts       []map[string]any `json:"hosts"`
			Credentials []map[string]any `json:"credentials"`
		}
		if code != 0 || json.Unmarshal([]byte(stdout), &v) != nil || len(v.Hosts) != 1 || v.Credentials[0]["secret"] != "•••" {
			t.Errorf("code %d, output:\n%s", code, stdout)
		}
		if strings.Contains(stdout, "topsecret-123") {
			t.Fatal("a secret was printed")
		}
	})
	t.Run("a case that does not exist is not created", func(t *testing.T) {
		code, _, stderr := runCLI(t, "map", "--case", "typo")
		if code != 1 || !strings.Contains(stderr, `no case "typo"`) || !strings.Contains(stderr, "kagegami init --case typo") {
			t.Errorf("code %d, stderr %q", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(home, "data", "cases", "typo")); err == nil {
			t.Error("the command created a case")
		}
	})
	t.Run("an invalid case name is a usage error", func(t *testing.T) {
		if code, _, _ := runCLI(t, "map", "--case", "../etc"); code != 2 {
			t.Errorf("code %d", code)
		}
	})
	t.Run("several cases need a choice", func(t *testing.T) {
		caseWithHost(t, home, "another")
		code, _, stderr := runCLI(t, "map")
		if code != 2 || !strings.Contains(stderr, "another, htb-box") || !strings.Contains(stderr, "--case") {
			t.Errorf("code %d, stderr %q", code, stderr)
		}
		if code, stdout, _ := runCLI(t, "map", "--case", "another"); code != 0 || !strings.Contains(stdout, "10.0.0.5") {
			t.Errorf("code %d", code)
		}
	})
	t.Run("a folder without a database is not a case", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(home, "data", "cases", "stray"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, stderr := runCLI(t, "map"); strings.Contains(stderr, "stray") {
			t.Errorf("stderr %q", stderr)
		}
	})
	t.Run("extra arguments", func(t *testing.T) {
		if code, _, _ := runCLI(t, "map", "extra"); code != 2 {
			t.Errorf("code %d", code)
		}
	})
}

func TestMapWithoutAnyCase(t *testing.T) {
	t.Setenv("KAGEGAMI_HOME", t.TempDir())
	code, _, stderr := runCLI(t, "map")
	if code != 1 || !strings.Contains(stderr, "kagegami init --case NAME") {
		t.Errorf("code %d, stderr %q", code, stderr)
	}
}
