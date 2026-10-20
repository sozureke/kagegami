package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points every path variable at a fresh temporary directory and returns
// the KAGEGAMI_HOME it uses.
func isolate(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "kg")
	t.Setenv("KAGEGAMI_HOME", root)
	t.Setenv("KAGEGAMI_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	return root
}

func TestInitCommandCreatesFiles(t *testing.T) {
	root := isolate(t)

	code, stdout, stderr := runCLI(t, "init", "--case", "box1")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %q", code, stderr)
	}
	for _, want := range []string{
		"created      " + filepath.Join(root, "config.yaml"),
		"created      " + filepath.Join(root, "templates", "pentest.md"),
		"created      " + filepath.Join(root, "data", "cases", "box1"),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		t.Errorf("config.yaml was not created: %v", err)
	}
}

func TestInitCommandIsIdempotent(t *testing.T) {
	isolate(t)
	if code, _, stderr := runCLI(t, "init"); code != 0 {
		t.Fatalf("first run: exit %d, stderr %q", code, stderr)
	}

	code, stdout, stderr := runCLI(t, "init")
	if code != 0 {
		t.Fatalf("second run: exit %d, stderr %q", code, stderr)
	}
	if strings.Contains(stdout, "created") || strings.Contains(stdout, "overwritten") {
		t.Errorf("second run changed something:\n%s", stdout)
	}
	if got := strings.Count(stdout, "skipped"); got != 2 {
		t.Errorf("skipped lines = %d, want 2:\n%s", got, stdout)
	}
}

func TestInitCommandForceReportsBackup(t *testing.T) {
	root := isolate(t)
	if code, _, _ := runCLI(t, "init"); code != 0 {
		t.Fatal("first run failed")
	}

	code, stdout, stderr := runCLI(t, "init", "--force")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "overwritten") {
		t.Errorf("stdout does not mention overwriting:\n%s", stdout)
	}
	if want := "previous version kept as " + filepath.Join(root, "config.yaml.bak"); !strings.Contains(stdout, want) {
		t.Errorf("stdout is missing %q:\n%s", want, stdout)
	}
}

func TestInitCommandRejectsBadInput(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"path traversal in case", []string{"init", "--case", "../x"}, "invalid case name"},
		{"hidden case", []string{"init", "--case", ".hidden"}, "invalid case name"},
		{"unknown flag", []string{"init", "--bogus"}, "unknown flag"},
		{"positional argument", []string{"init", "extra"}, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := isolate(t)

			code, stdout, stderr := runCLI(t, tt.args...)
			if code != 2 {
				t.Errorf("exit code = %d, want 2 (usage error)", code)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want containing %q", stderr, tt.wantStderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Errorf("rejected input still created %s", root)
			}
		})
	}
}

func TestInitCommandFailsOnRelativeHome(t *testing.T) {
	isolate(t)
	t.Setenv("KAGEGAMI_HOME", "relative/path")

	code, _, stderr := runCLI(t, "init")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "KAGEGAMI_HOME must be an absolute path") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestInitIsListedInHelp(t *testing.T) {
	_, stdout, _ := runCLI(t, "init", "--help")
	for _, want := range []string{"--force", "--case"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("init --help does not document %s:\n%s", want, stdout)
		}
	}
}
