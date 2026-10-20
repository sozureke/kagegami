package setup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/config"
)

// testPaths puts everything under a fresh temporary KAGEGAMI_HOME.
func testPaths(t *testing.T) config.Paths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "kg")
	p, err := config.NewPaths(config.Env{
		Getenv: func(k string) string {
			if k == "KAGEGAMI_HOME" {
				return root
			}
			return ""
		},
		Home: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func templatePath(p config.Paths) string { return filepath.Join(p.TemplatesDir(), "pentest.md") }

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func actions(res Result) []Action {
	out := make([]Action, len(res.Items))
	for i, it := range res.Items {
		out[i] = it.Action
	}
	return out
}

func TestInitFresh(t *testing.T) {
	p := testPaths(t)

	res, err := Init(p, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if want := []Action{Created, Created}; !reflect.DeepEqual(actions(res), want) {
		t.Fatalf("actions = %v, want %v", actions(res), want)
	}
	if got := mustRead(t, p.ConfigFile()); got != string(defaultConfig) {
		t.Error("config.yaml does not match the embedded default")
	}
	if got := mustRead(t, templatePath(p)); got != string(defaultTemplate) {
		t.Error("pentest.md does not match the embedded template")
	}
	if res.Items[0].Path != p.ConfigFile() || res.Items[1].Path != templatePath(p) {
		t.Errorf("unexpected item paths: %+v", res.Items)
	}
}

func TestInitPermissions(t *testing.T) {
	p := testPaths(t)
	if _, err := Init(p, Options{Case: "box1"}); err != nil {
		t.Fatal(err)
	}

	caseDir, err := p.CaseDir("box1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want os.FileMode
	}{
		{p.ConfigDir(), 0o700},
		{p.TemplatesDir(), 0o700},
		{p.ConfigFile(), 0o600},
		{templatePath(p), 0o600},
		{caseDir, 0o700},
		{filepath.Join(caseDir, "raw"), 0o700},
	}
	for _, tt := range tests {
		st, err := os.Stat(tt.path)
		if err != nil {
			t.Errorf("stat %s: %v", tt.path, err)
			continue
		}
		if got := st.Mode().Perm(); got != tt.want {
			t.Errorf("%s mode = %o, want %o", tt.path, got, tt.want)
		}
	}
}

func TestInitIsIdempotentAndKeepsUserEdits(t *testing.T) {
	p := testPaths(t)
	if _, err := Init(p, Options{}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, p.ConfigFile(), "logging:\n  level: debug\n")
	mustWrite(t, templatePath(p), "my own template\n")

	res, err := Init(p, Options{})
	if err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if want := []Action{Skipped, Skipped}; !reflect.DeepEqual(actions(res), want) {
		t.Fatalf("actions = %v, want %v", actions(res), want)
	}
	if got := mustRead(t, p.ConfigFile()); got != "logging:\n  level: debug\n" {
		t.Errorf("config was modified without --force: %q", got)
	}
	if got := mustRead(t, templatePath(p)); got != "my own template\n" {
		t.Errorf("template was modified without --force: %q", got)
	}
	if _, err := os.Stat(p.ConfigFile() + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("a backup was created even though nothing was overwritten")
	}
}

func TestInitForceBacksUpAndOverwrites(t *testing.T) {
	p := testPaths(t)
	if _, err := Init(p, Options{}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, p.ConfigFile(), "# edit one\n")

	res, err := Init(p, Options{Force: true})
	if err != nil {
		t.Fatalf("Init --force: %v", err)
	}
	if want := []Action{Overwritten, Overwritten}; !reflect.DeepEqual(actions(res), want) {
		t.Fatalf("actions = %v, want %v", actions(res), want)
	}
	if got := res.Items[0].Backup; got != p.ConfigFile()+".bak" {
		t.Errorf("Backup = %q, want %q", got, p.ConfigFile()+".bak")
	}
	if got := mustRead(t, p.ConfigFile()+".bak"); got != "# edit one\n" {
		t.Errorf("backup content = %q, want the hand-edited version", got)
	}
	if got := mustRead(t, p.ConfigFile()); got != string(defaultConfig) {
		t.Error("config was not reset to the default")
	}

	// A second --force replaces the older backup with the latest previous version.
	mustWrite(t, p.ConfigFile(), "# edit two\n")
	if _, err := Init(p, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, p.ConfigFile()+".bak"); got != "# edit two\n" {
		t.Errorf("backup after second --force = %q, want %q", got, "# edit two\n")
	}
}

func TestInitForceOnlyTouchesExistingFiles(t *testing.T) {
	p := testPaths(t)
	res, err := Init(p, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Action{Created, Created}; !reflect.DeepEqual(actions(res), want) {
		t.Errorf("actions on a fresh directory with --force = %v, want %v", actions(res), want)
	}
	if _, err := os.Stat(p.ConfigFile() + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("--force on a fresh directory must not create backups")
	}
}

func TestInitCase(t *testing.T) {
	p := testPaths(t)

	res, err := Init(p, Options{Case: "box1"})
	if err != nil {
		t.Fatal(err)
	}
	caseDir, _ := p.CaseDir("box1")
	last := res.Items[len(res.Items)-1]
	if last.Path != caseDir || last.Action != Created {
		t.Errorf("last item = %+v, want case dir created", last)
	}
	if st, err := os.Stat(filepath.Join(caseDir, "raw")); err != nil || !st.IsDir() {
		t.Errorf("raw directory missing: %v", err)
	}

	res, err = Init(p, Options{Case: "box1"})
	if err != nil {
		t.Fatal(err)
	}
	if last := res.Items[len(res.Items)-1]; last.Action != Skipped {
		t.Errorf("existing case action = %v, want skipped", last.Action)
	}

	// A different case is created next to the first one.
	if _, err := Init(p, Options{Case: "box2"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"box1", "box2"} {
		if _, err := os.Stat(filepath.Join(p.CasesDir(), name, "raw")); err != nil {
			t.Errorf("case %s: %v", name, err)
		}
	}
}

func TestInitWithoutCaseCreatesNoCaseDirectory(t *testing.T) {
	p := testPaths(t)
	if _, err := Init(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.CasesDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cases directory exists without --case: %v", err)
	}
}

func TestInitInvalidCaseCreatesNothing(t *testing.T) {
	for _, name := range []string{"..", "../x", "a/b", ".hidden", "-x", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			p := testPaths(t)
			res, err := Init(p, Options{Case: name})
			if !errors.Is(err, config.ErrInvalidCaseName) {
				t.Fatalf("err = %v, want ErrInvalidCaseName", err)
			}
			if len(res.Items) != 0 {
				t.Errorf("items = %+v, want none", res.Items)
			}
			if _, err := os.Stat(p.ConfigDir()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("config directory was created despite the invalid case name: %v", err)
			}
		})
	}
}

// A symlink planted where a file should go must never be written through.
func TestInitDoesNotFollowSymlinks(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "without force"
		if force {
			name = "with force"
		}
		t.Run(name, func(t *testing.T) {
			p := testPaths(t)
			if err := os.MkdirAll(p.ConfigDir(), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "victim")
			if err := os.Symlink(outside, p.ConfigFile()); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			if _, err := Init(p, Options{Force: force}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("Init wrote through the symlink to %s", outside)
			}
		})
	}
}

func TestInitReportsFilesystemErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	p, err := config.NewPaths(config.Env{
		Getenv: func(k string) string {
			if k == "KAGEGAMI_HOME" {
				return filepath.Join(parent, "kg")
			}
			return ""
		},
		Home: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Init(p, Options{})
	if err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("err = %v, want a create error", err)
	}
}

// The embedded config.yaml is documentation users copy from; it must stay valid and
// in sync with the defaults compiled into the program.
func TestEmbeddedConfigMatchesDefault(t *testing.T) {
	p := testPaths(t)
	if _, err := Init(p, Options{}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatalf("embedded config.yaml does not load: %v", err)
	}
	if want := config.Default(); !reflect.DeepEqual(got, want) {
		t.Errorf("embedded config.yaml differs from config.Default():\n got  %+v\n want %+v", got, want)
	}
}

func TestEmbeddedTemplateIsNotEmpty(t *testing.T) {
	if strings.TrimSpace(string(defaultTemplate)) == "" {
		t.Fatal("embedded pentest.md is empty")
	}
}
