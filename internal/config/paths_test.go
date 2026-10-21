package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv builds an Env from a map, so tests never touch the real environment.
func fakeEnv(home string, vars map[string]string) Env {
	return Env{Getenv: func(k string) string { return vars[k] }, Home: home}
}

func TestNewPaths(t *testing.T) {
	tests := []struct {
		name       string
		env        Env
		wantConfig string
		wantData   string
		wantFile   string
		wantErr    string // substring; empty means success
	}{
		{
			name:       "defaults",
			env:        fakeEnv("/home/u", nil),
			wantConfig: "/home/u/.config/kagegami",
			wantData:   "/home/u/.local/share/kagegami",
			wantFile:   "/home/u/.config/kagegami/config.yaml",
		},
		{
			name:       "xdg overrides defaults",
			env:        fakeEnv("/home/u", map[string]string{"XDG_CONFIG_HOME": "/xc", "XDG_DATA_HOME": "/xd"}),
			wantConfig: "/xc/kagegami",
			wantData:   "/xd/kagegami",
			wantFile:   "/xc/kagegami/config.yaml",
		},
		{
			name:       "only one xdg variable set",
			env:        fakeEnv("/home/u", map[string]string{"XDG_DATA_HOME": "/xd"}),
			wantConfig: "/home/u/.config/kagegami",
			wantData:   "/xd/kagegami",
			wantFile:   "/home/u/.config/kagegami/config.yaml",
		},
		{
			name:       "relative xdg is ignored",
			env:        fakeEnv("/home/u", map[string]string{"XDG_CONFIG_HOME": "rel", "XDG_DATA_HOME": "rel"}),
			wantConfig: "/home/u/.config/kagegami",
			wantData:   "/home/u/.local/share/kagegami",
			wantFile:   "/home/u/.config/kagegami/config.yaml",
		},
		{
			name:       "kagegami home wins over xdg",
			env:        fakeEnv("/home/u", map[string]string{"KAGEGAMI_HOME": "/kg", "XDG_CONFIG_HOME": "/xc", "XDG_DATA_HOME": "/xd"}),
			wantConfig: "/kg",
			wantData:   "/kg/data",
			wantFile:   "/kg/config.yaml",
		},
		{
			name:       "kagegami config overrides the file only",
			env:        fakeEnv("/home/u", map[string]string{"KAGEGAMI_HOME": "/kg", "KAGEGAMI_CONFIG": "/etc/kg.yaml"}),
			wantConfig: "/kg",
			wantData:   "/kg/data",
			wantFile:   "/etc/kg.yaml",
		},
		{
			name:       "kagegami config alone keeps default dirs",
			env:        fakeEnv("/home/u", map[string]string{"KAGEGAMI_CONFIG": "/etc/kg.yaml"}),
			wantConfig: "/home/u/.config/kagegami",
			wantData:   "/home/u/.local/share/kagegami",
			wantFile:   "/etc/kg.yaml",
		},
		{
			name:    "relative kagegami home is rejected",
			env:     fakeEnv("/home/u", map[string]string{"KAGEGAMI_HOME": "rel"}),
			wantErr: "KAGEGAMI_HOME must be an absolute path",
		},
		{
			name:    "relative kagegami config is rejected",
			env:     fakeEnv("/home/u", map[string]string{"KAGEGAMI_CONFIG": "kg.yaml"}),
			wantErr: "KAGEGAMI_CONFIG must be an absolute path",
		},
		{
			name:    "no home and no overrides",
			env:     fakeEnv("", nil),
			wantErr: "cannot locate the home directory",
		},
		{
			name:       "no home is fine with kagegami home",
			env:        fakeEnv("", map[string]string{"KAGEGAMI_HOME": "/kg"}),
			wantConfig: "/kg",
			wantData:   "/kg/data",
			wantFile:   "/kg/config.yaml",
		},
		{
			name:       "no home is fine with both xdg variables",
			env:        fakeEnv("", map[string]string{"XDG_CONFIG_HOME": "/xc", "XDG_DATA_HOME": "/xd"}),
			wantConfig: "/xc/kagegami",
			wantData:   "/xd/kagegami",
			wantFile:   "/xc/kagegami/config.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewPaths(tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := filepath.FromSlash(tt.wantConfig); p.ConfigDir() != want {
				t.Errorf("ConfigDir = %q, want %q", p.ConfigDir(), want)
			}
			if want := filepath.FromSlash(tt.wantData); p.DataDir() != want {
				t.Errorf("DataDir = %q, want %q", p.DataDir(), want)
			}
			if want := filepath.FromSlash(tt.wantFile); p.ConfigFile() != want {
				t.Errorf("ConfigFile = %q, want %q", p.ConfigFile(), want)
			}
		})
	}
}

func TestDerivedDirs(t *testing.T) {
	p, err := NewPaths(fakeEnv("/home/u", map[string]string{"KAGEGAMI_HOME": "/kg"}))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"templates", p.TemplatesDir(), "/kg/templates"},
		{"cases", p.CasesDir(), "/kg/data/cases"},
		{"logs", p.LogsDir(), "/kg/data/logs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if want := filepath.FromSlash(tt.want); tt.got != want {
				t.Errorf("got %q, want %q", tt.got, want)
			}
		})
	}
}

func TestCaseDirValid(t *testing.T) {
	p, err := NewPaths(fakeEnv("/home/u", nil))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"htb-box.1", "A_b", "a", "0day", "a..b", strings.Repeat("a", 64)} {
		t.Run(name, func(t *testing.T) {
			dir, err := p.CaseDir(name)
			if err != nil {
				t.Fatalf("CaseDir(%q) error: %v", name, err)
			}
			if want := filepath.Join(p.CasesDir(), name); dir != want {
				t.Errorf("CaseDir(%q) = %q, want %q", name, dir, want)
			}
		})
	}
}

func TestCaseDirInvalid(t *testing.T) {
	p, err := NewPaths(fakeEnv("/home/u", nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"single dot", "."},
		{"parent directory", ".."},
		{"three dots", "..."},
		{"hidden", ".hidden"},
		{"leading dash", "-flag"},
		{"leading underscore", "_x"},
		{"slash inside", "a/b"},
		{"backslash inside", `a\b`},
		{"traversal prefix", "../x"},
		{"traversal suffix", "x/.."},
		{"absolute path", "/abs"},
		{"space", "a b"},
		{"nul byte", "a\x00b"},
		{"newline", "a\nb"},
		{"non-ascii", "é"},
		{"too long", strings.Repeat("a", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := p.CaseDir(tt.input)
			if !errors.Is(err, ErrInvalidCaseName) {
				t.Fatalf("CaseDir(%q) err = %v, want ErrInvalidCaseName", tt.input, err)
			}
			if dir != "" {
				t.Errorf("CaseDir(%q) returned %q together with an error", tt.input, dir)
			}
			if err := ValidateCaseName(tt.input); !errors.Is(err, ErrInvalidCaseName) {
				t.Errorf("ValidateCaseName(%q) err = %v, want ErrInvalidCaseName", tt.input, err)
			}
		})
	}
}

// FuzzCaseDirStaysInside checks the property that matters for security: whatever
// the name is, a successful CaseDir result is directly inside CasesDir.
func FuzzCaseDirStaysInside(f *testing.F) {
	for _, s := range []string{"ok", "..", "a/../b", ".x", "a\x00", "a..b"} {
		f.Add(s)
	}
	p, err := NewPaths(fakeEnv("/home/u", nil))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, name string) {
		dir, err := p.CaseDir(name)
		if err != nil {
			return
		}
		if filepath.Dir(dir) != p.CasesDir() {
			t.Fatalf("CaseDir(%q) = %q escapes %q", name, dir, p.CasesDir())
		}
	})
}

func TestOSEnv(t *testing.T) {
	t.Setenv("HOME", "/tmp/kg-home")
	t.Setenv("KAGEGAMI_HOME", "/tmp/kg-root")

	env, err := OSEnv()
	if err != nil {
		t.Fatalf("OSEnv error: %v", err)
	}
	if env.Home != "/tmp/kg-home" {
		t.Errorf("Home = %q, want /tmp/kg-home", env.Home)
	}
	if got := env.Getenv("KAGEGAMI_HOME"); got != "/tmp/kg-root" {
		t.Errorf("Getenv(KAGEGAMI_HOME) = %q, want /tmp/kg-root", got)
	}
}

func TestRunDir(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want string
	}{
		{"default", fakeEnv("/home/u", nil), "/home/u/.local/share/kagegami/run"},
		{"xdg data", fakeEnv("/home/u", map[string]string{"XDG_DATA_HOME": "/xd"}), "/xd/kagegami/run"},
		{"KAGEGAMI_HOME", fakeEnv("/home/u", map[string]string{"KAGEGAMI_HOME": "/kg", "XDG_DATA_HOME": "/xd"}), "/kg/run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewPaths(tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.RunDir(); got != tt.want {
				t.Errorf("RunDir() = %q, want %q", got, tt.want)
			}
		})
	}
}
