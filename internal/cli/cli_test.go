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

	"github.com/spf13/cobra"

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
		{"stub is not implemented", []string{"report"}, 1, "", "not implemented yet"},
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
	_, stdout, _ := runCLI(t, "report")
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
	var walk func(prefix string, cmds []*cobra.Command)
	walk = func(prefix string, cmds []*cobra.Command) {
		for _, cmd := range cmds {
			name := prefix + cmd.Name()
			registered[name] = true
			if cmd.Short == "" {
				t.Errorf("command %q has no short description in help.yaml", name)
			}
			walk(name+" ", cmd.Commands())
		}
	}
	walk("", root.Commands())
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

func TestMapStats(t *testing.T) {
	home := t.TempDir()
	caseWithHost(t, home, "stats")
	st, err := store.Open(context.Background(), filepath.Join(home, "data", "cases", "stats", store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Repo().AddUsage(context.Background(), store.Usage{Tier: "tier1", Model: "ollama/llama3.1:8b", PromptTokens: 12, CompletionTokens: 3, OK: true}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	code, stdout, stderr := runCLI(t, "map", "--stats")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "12 prompt + 3 answer = 15 tokens") {
		t.Errorf("code %d, stderr %q, stdout:\n%s", code, stderr, stdout)
	}
	if _, stdout, _ := runCLI(t, "map"); strings.Contains(stdout, "tokens") {
		t.Errorf("--stats is off by default:\n%s", stdout)
	}
}

func TestCaseSetAndShow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KAGEGAMI_HOME", home)
	if code, _, stderr := runCLI(t, "init", "--case", "box"); code != 0 {
		t.Fatalf("init: %d %s", code, stderr)
	}

	t.Run("nothing set yet", func(t *testing.T) {
		code, out, _ := runCLI(t, "case", "show")
		if code != 0 || !strings.Contains(out, "No scope is set") || !strings.Contains(out, "kagegami case set --scope") {
			t.Errorf("code %d:\n%s", code, out)
		}
	})
	t.Run("a target alone sets the scope too, and says so", func(t *testing.T) {
		code, out, _ := runCLI(t, "case", "set", "--target", "10.10.10.5", "--goal", "user and root")
		if code != 0 || !strings.Contains(out, "scope    10.10.10.5") || !strings.Contains(out, "it is now the target") {
			t.Errorf("code %d:\n%s", code, out)
		}
	})
	t.Run("the scope flag repeats and replaces", func(t *testing.T) {
		code, out, _ := runCLI(t, "case", "set", "--scope", "10.10.10.0/24", "--scope", "10.10.20.5-10.10.20.9")
		if code != 0 || !strings.Contains(out, "10.10.10.0/24, 10.10.20.5-10.10.20.9") || strings.Contains(out, "it is now the target") {
			t.Errorf("code %d:\n%s", code, out)
		}
		_, out, _ = runCLI(t, "case", "set", "--add-scope", "192.168.1.50")
		if !strings.Contains(out, "10.10.10.0/24, 10.10.20.5-10.10.20.9, 192.168.1.50") || !strings.Contains(out, "user and root") {
			t.Errorf("--add-scope:\n%s", out)
		}
	})
	t.Run("json", func(t *testing.T) {
		code, out, _ := runCLI(t, "case", "show", "--json")
		var v struct {
			Case   string   `json:"case"`
			Target string   `json:"target"`
			Scope  []string `json:"scope"`
			Phase  string   `json:"phase"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Case != "box" || v.Target != "10.10.10.5" || len(v.Scope) != 3 || v.Phase != "recon" {
			t.Errorf("code %d, %+v\n%s", code, v, out)
		}
	})
	t.Run("mistakes are usage errors and change nothing", func(t *testing.T) {
		for _, args := range [][]string{
			{"case", "set"},
			{"case", "set", "--scope", "0.0.0.0/0"},
			{"case", "set", "--scope", "box.htb"},
			{"case", "set", "--phase", "Bad Phase"},
			{"case", "set", "--target", ""},
			{"case", "set", "--case", "../x", "--goal", "g"},
			{"case", "show", "extra"},
			{"case", "bogus"},
		} {
			if code, out, stderr := runCLI(t, args...); code != 2 || out != "" || stderr == "" {
				t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, stderr)
			}
		}
		_, out, _ := runCLI(t, "case", "show")
		if !strings.Contains(out, "10.10.10.0/24, 10.10.20.5-10.10.20.9, 192.168.1.50") {
			t.Errorf("a refused change altered the scope:\n%s", out)
		}
	})
	t.Run("a case that does not exist", func(t *testing.T) {
		if code, _, stderr := runCLI(t, "case", "set", "--case", "nope", "--goal", "g"); code != 1 || !strings.Contains(stderr, `no case "nope"`) {
			t.Errorf("code %d, stderr %q", code, stderr)
		}
	})
	t.Run("the case command alone shows its help", func(t *testing.T) {
		if code, out, _ := runCLI(t, "case"); code != 0 || !strings.Contains(out, "set") || !strings.Contains(out, "show") {
			t.Errorf("code %d:\n%s", code, out)
		}
	})
}

func TestNextWithoutScope(t *testing.T) {
	home := t.TempDir()
	caseWithHost(t, home, "no-scope")
	code, out, stderr := runCLI(t, "next", "--case", "no-scope")
	if code != 2 || out != "" || !strings.Contains(stderr, "kagegami case set --scope") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, stderr)
	}
}

func TestPipelineWithoutScope(t *testing.T) {
	home := t.TempDir()
	caseWithHost(t, home, "no-scope")
	code, out, stderr := runCLI(t, "pipeline", "--case", "no-scope")
	if code != 2 || out != "" || !strings.Contains(stderr, "kagegami case set --scope") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, stderr)
	}
}

func TestNextAndPipelineNeedACase(t *testing.T) {
	t.Setenv("KAGEGAMI_HOME", t.TempDir())
	for _, cmd := range []string{"next", "pipeline"} {
		if code, _, stderr := runCLI(t, cmd); code != 1 || !strings.Contains(stderr, "kagegami init --case") {
			t.Errorf("%s: code %d, stderr %q", cmd, code, stderr)
		}
	}
}

func TestPipelineHistoryAndShowNeedNoModel(t *testing.T) {
	home := t.TempDir()
	caseWithHost(t, home, "hist")

	code, out, _ := runCLI(t, "pipeline", "--case", "hist", "--history")
	if code != 0 || !strings.Contains(out, "No pipeline has been run yet.") {
		t.Errorf("empty history: code %d, out %q", code, out)
	}
	code, out, _ = runCLI(t, "pipeline", "--case", "hist", "--history", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("empty history json: code %d, out %q", code, out)
	}
	if code, _, stderr := runCLI(t, "pipeline", "--case", "hist", "--show", "1"); code != 2 || !strings.Contains(stderr, "no pipeline run 1") {
		t.Errorf("missing run: code %d, stderr %q", code, stderr)
	}

	st, err := store.Open(context.Background(), filepath.Join(home, "data", "cases", "hist", store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.Repo().SavePipelineRun(context.Background(), "ollama/llama3.1:8b", "one host with ssh",
		`[{"priority":1,"service":"10.0.0.5:22/tcp","vector":"ssh","why":"check the version","commands":["ssh-audit 10.0.0.5"],"confidence":"medium"}]`)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	code, out, _ = runCLI(t, "pipeline", "--case", "hist", "--history")
	if code != 0 || !strings.Contains(out, "one host with ssh") || !strings.Contains(out, "ollama/llama3.1:8b") {
		t.Errorf("history: code %d, out %q", code, out)
	}
	code, out, _ = runCLI(t, "pipeline", "--case", "hist", "--show", fmt.Sprint(run.ID))
	if code != 0 || !strings.Contains(out, "one host with ssh") || !strings.Contains(out, "ssh-audit 10.0.0.5") || !strings.Contains(out, "[medium] ssh") {
		t.Errorf("show: code %d, out %q", code, out)
	}
	code, out, _ = runCLI(t, "pipeline", "--case", "hist", "--show", fmt.Sprint(run.ID), "--json")
	var decoded store.PipelineRun
	if code != 0 || json.Unmarshal([]byte(out), &decoded) != nil || decoded.ID != run.ID {
		t.Errorf("show json: code %d, out %q", code, out)
	}
}

func TestNextAndPipelineFlags(t *testing.T) {
	home := t.TempDir()
	caseWithHost(t, home, "flags") // has no scope, so the model is never reached: see below

	// Several free-text words after "next"/"pipeline" are a request, not separate arguments;
	// cobra must accept them and let our own code run (and reach the "no scope" error, not an
	// argument-count error).
	for _, args := range [][]string{
		{"next", "extra", "words", "are", "the", "request"},
		{"pipeline", "focus", "on", "the", "web", "server"},
	} {
		code, out, stderr := runCLI(t, append(args, "--case", "flags")...)
		if code != 2 || out != "" || !strings.Contains(stderr, "case has no scope") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, stderr)
		}
	}
	// --history and --show together: --history is checked first and wins, without a model call
	if code, out, _ := runCLI(t, "pipeline", "--case", "flags", "--history", "--show", "1"); code != 0 || !strings.Contains(out, "No pipeline has been run yet.") {
		t.Errorf("--history and --show together: code %d, out %q", code, out)
	}
	if code, _, _ := runCLI(t, "pipeline", "--case", "flags", "--show", "-1"); code != 2 {
		t.Errorf("a negative --show was accepted: code %d", code)
	}
}

func TestWatchNeedsTmux(t *testing.T) {
	t.Setenv("TMUX", "") // a test run inside tmux must never split the person's own window
	t.Setenv("TMUX_PANE", "")
	code, stdout, stderr := runCLI(t, "watch", "--no-anim")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "inside tmux") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, _, stderr := runCLI(t, "watch", "--demo"); code != 2 || !strings.Contains(stderr, "unknown flag") {
		t.Errorf("--demo is gone: code=%d stderr=%q", code, stderr)
	}
}

func TestPanelNeedsATerminalAndAPane(t *testing.T) {
	if code, _, stderr := runCLI(t, "panel", "--pane", "%1"); code != 1 || !strings.Contains(stderr, "needs a terminal") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := runCLI(t, "panel", "--bogus"); code != 2 {
		t.Errorf("an unknown flag is a usage error: %d", code)
	}
	for _, c := range newRootCmd(io.Discard, io.Discard, testInfo).Commands() {
		if c.Name() == "panel" && !c.Hidden {
			t.Error("panel is run by watch, not typed: it must stay out of the help")
		}
	}
}

func TestSplashNeedsATerminal(t *testing.T) {
	code, stdout, stderr := runCLI(t, "splash", "--loop")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "needs a terminal") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
