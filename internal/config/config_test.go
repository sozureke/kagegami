package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// exampleConfig is the sample configuration from the design document. It must always load.
const exampleConfig = `
model:
  tier1: ollama/llama3.1:8b
  tier2: ollama/qwen2.5:14b
  cloud_fallback: false

privacy:
  mask_credentials: true
  mask_flags: true
  mask_hosts: false

execution:
  mode: propose
  confirm_destructive: true

engagement:
  scaffold_folders: true
  auto_report: true
  report_template: ~/.config/kagegami/templates/pentest.md
  author: sozureke

phases:
  recon:
    emphasis: "full information gathering, synthesis into a pipeline"
  web:
    emphasis: "curl/ffuf/httpx, no browser"

capture:
  trigger: shell-hooks
  ignore: [cd, ls, clear, pwd]

logging:
  level: debug
`

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() does not validate: %v", err)
	}
}

func TestParseEmptyInputsGiveDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"zero bytes", ""},
		{"only whitespace", "\n\n  \n"},
		{"only a comment", "# nothing here yet\n"},
		{"explicit empty document", "---\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse([]byte(tt.in), "test.yaml")
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			want := Default()
			if got.Execution != want.Execution || got.Model != want.Model || got.Logging != want.Logging ||
				got.Privacy != want.Privacy || got.Engagement != want.Engagement ||
				got.Capture.Trigger != want.Capture.Trigger || !slices.Equal(got.Capture.Ignore, want.Capture.Ignore) {
				t.Errorf("got %+v, want defaults %+v", got, want)
			}
		})
	}
}

func TestParsePartialKeepsOtherDefaults(t *testing.T) {
	got, err := parse([]byte("execution:\n  mode: approve-run\n"), "test.yaml")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if got.Execution.Mode != "approve-run" {
		t.Errorf("Execution.Mode = %q, want approve-run", got.Execution.Mode)
	}
	// A sibling key in the same section and keys in other sections keep their defaults.
	if !got.Execution.ConfirmDestructive {
		t.Error("Execution.ConfirmDestructive lost its default (true)")
	}
	if got.Model.Tier1 != Default().Model.Tier1 {
		t.Errorf("Model.Tier1 = %q, want default %q", got.Model.Tier1, Default().Model.Tier1)
	}
}

func TestParseCanTurnDefaultTrueOff(t *testing.T) {
	got, err := parse([]byte("privacy:\n  mask_credentials: false\n"), "test.yaml")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if got.Privacy.MaskCredentials {
		t.Error("explicit false for mask_credentials was overridden by the default")
	}
	if !got.Privacy.MaskFlags {
		t.Error("mask_flags should keep its default (true)")
	}
}

func TestParseListReplacesDefault(t *testing.T) {
	got, err := parse([]byte("capture:\n  ignore: [whoami]\n"), "test.yaml")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if want := []string{"whoami"}; !slices.Equal(got.Capture.Ignore, want) {
		t.Errorf("Capture.Ignore = %v, want %v (the file's list replaces the default)", got.Capture.Ignore, want)
	}

	got, err = parse([]byte("capture:\n  ignore: []\n"), "test.yaml")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(got.Capture.Ignore) != 0 {
		t.Errorf("Capture.Ignore = %v, want empty for an explicit empty list", got.Capture.Ignore)
	}
}

func TestParseExampleConfig(t *testing.T) {
	got, err := parse([]byte(exampleConfig), "example.yaml")
	if err != nil {
		t.Fatalf("the documented example config does not load: %v", err)
	}
	if got.Engagement.Author != "sozureke" {
		t.Errorf("Engagement.Author = %q, want sozureke", got.Engagement.Author)
	}
	if got.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want debug", got.Logging.Level)
	}
	if got.Phases["web"].Emphasis != "curl/ffuf/httpx, no browser" {
		t.Errorf("Phases[web] = %+v", got.Phases["web"])
	}
	if len(got.Phases) != 2 {
		t.Errorf("len(Phases) = %d, want 2", len(got.Phases))
	}
}

func TestParseRejectsBadYAML(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"unknown top-level key", "bogus:\n  tier1: a/b\n", "field bogus not found"},
		{"unknown nested key", "execution:\n  mdoe: propose\n", "field mdoe not found"},
		{"unknown key in a phase", "phases:\n  recon:\n    bogus: x\n", "field bogus not found"},
		{"broken syntax", "model: [\n", "parse test.yaml"},
		{"wrong type", "execution:\n  confirm_destructive: maybe\n", "parse test.yaml"},
		{"scalar instead of mapping", "model: ollama/llama3.1:8b\n", "parse test.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.in), "test.yaml")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr []string // all substrings must be present; empty means valid
	}{
		{"defaults", func(*Config) {}, nil},
		{"approve-run", func(c *Config) { c.Execution.Mode = "approve-run" }, nil},
		{"pipe-pane", func(c *Config) { c.Capture.Trigger = "pipe-pane" }, nil},
		{"cloud model", func(c *Config) { c.Model.Tier2 = "anthropic/claude-fable-5-1" }, nil},
		{"no ceiling", func(c *Config) { c.Limits.SessionTokens = 0 }, nil},
		{"negative ceiling", func(c *Config) { c.Limits.SessionTokens = -1 }, []string{"limits.session_tokens"}},
		{"tier2 left empty", func(c *Config) { c.Model.Tier2 = "" }, nil},
		{"another Ollama address", func(c *Config) { c.Providers.Ollama.BaseURL = "http://192.168.64.1:11434" }, nil},
		{"https Ollama", func(c *Config) { c.Providers.Ollama.BaseURL = "https://ollama.example.com" }, nil},
		{"empty address", func(c *Config) { c.Providers.Ollama.BaseURL = "" }, []string{"providers.ollama.base_url"}},
		{"address without a scheme", func(c *Config) { c.Providers.Ollama.BaseURL = "localhost:11434" }, []string{"providers.ollama.base_url"}},
		{"address with another scheme", func(c *Config) { c.Providers.Ollama.BaseURL = "ftp://localhost" }, []string{"providers.ollama.base_url"}},
		{"address with a password", func(c *Config) { c.Providers.Ollama.BaseURL = "http://user:pw@localhost:11434" }, []string{"providers.ollama.base_url"}},
		{"address without a host", func(c *Config) { c.Providers.Ollama.BaseURL = "http://" }, []string{"providers.ollama.base_url"}},
		{
			"bad mode", func(c *Config) { c.Execution.Mode = "run" },
			[]string{`execution.mode: "run" is invalid`, `"propose", "approve-run"`},
		},
		{
			"empty mode", func(c *Config) { c.Execution.Mode = "" },
			[]string{`execution.mode: "" is invalid`},
		},
		{
			"mode is case-sensitive", func(c *Config) { c.Execution.Mode = "Propose" },
			[]string{`execution.mode: "Propose" is invalid`},
		},
		{
			"bad trigger", func(c *Config) { c.Capture.Trigger = "magic" },
			[]string{`capture.trigger: "magic" is invalid`},
		},
		{
			"bad log level", func(c *Config) { c.Logging.Level = "loud" },
			[]string{`logging.level: "loud" is invalid`, `"debug", "info", "warn", "error"`},
		},
		{
			"tier1 without slash", func(c *Config) { c.Model.Tier1 = "llama3" },
			[]string{"model.tier1"},
		},
		{
			"tier1 empty model", func(c *Config) { c.Model.Tier1 = "ollama/" },
			[]string{"model.tier1"},
		},
		{
			"tier1 empty provider", func(c *Config) { c.Model.Tier1 = "/llama3.1" },
			[]string{"model.tier1"},
		},
		{
			"tier1 with space", func(c *Config) { c.Model.Tier1 = "ollama/llama 3" },
			[]string{"model.tier1"},
		},
		{
			"tier1 empty", func(c *Config) { c.Model.Tier1 = "" },
			[]string{"model.tier1"},
		},
		{
			"tier2 bad", func(c *Config) { c.Model.Tier2 = "x" },
			[]string{"model.tier2"},
		},
		{"all problems are reported together", func(c *Config) {
			c.Model.Tier1 = "bad"
			c.Model.Tier2 = "bad"
			c.Execution.Mode = "run"
			c.Capture.Trigger = "magic"
			c.Logging.Level = "loud"
		}, []string{"model.tier1", "model.tier2", "execution.mode", "capture.trigger", "logging.level"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestValidationErrorListsEveryProblemOnItsOwnLine(t *testing.T) {
	in := "model:\n  tier1: bad\nexecution:\n  mode: run\nlogging:\n  level: loud\n"
	_, err := parse([]byte(in), "test.yaml")
	if err == nil {
		t.Fatal("expected an error")
	}
	lines := strings.Split(err.Error(), "\n")
	if lines[0] != "invalid config test.yaml:" {
		t.Errorf("first line = %q", lines[0])
	}
	if len(lines) != 4 {
		t.Errorf("got %d lines, want a header plus 3 problems:\n%s", len(lines), err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		missing := filepath.Join(dir, "nope.yaml")
		_, err := Load(missing)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error %q does not mention the path", err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty.yaml")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load of an empty file: %v", err)
		}
		if got.Execution.Mode != "propose" {
			t.Errorf("Execution.Mode = %q, want the default", got.Execution.Mode)
		}
	})

	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(dir, "ok.yaml")
		if err := os.WriteFile(path, []byte(exampleConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.Engagement.Author != "sozureke" {
			t.Errorf("Author = %q", got.Engagement.Author)
		}
	})

	t.Run("invalid file names the file", func(t *testing.T) {
		path := filepath.Join(dir, "bad.yaml")
		if err := os.WriteFile(path, []byte("execution:\n  mode: run\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("err = %v, want it to mention %s", err, path)
		}
		if errors.Is(err, ErrNotFound) {
			t.Error("a validation failure must not look like ErrNotFound")
		}
	})

	t.Run("path is a directory", func(t *testing.T) {
		_, err := Load(dir)
		if err == nil {
			t.Fatal("expected an error when the path is a directory")
		}
		if errors.Is(err, ErrNotFound) {
			t.Error("a directory must not be reported as ErrNotFound")
		}
	})
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		name, path, home, want string
	}{
		{"leading tilde", "~/a/b.md", "/home/u", "/home/u/a/b.md"},
		{"just tilde slash", "~/", "/home/u", "/home/u"},
		{"bare tilde is left alone", "~", "/home/u", "~"},
		{"other user is left alone", "~root/a", "/home/u", "~root/a"},
		{"tilde in the middle", "/x/~/y", "/home/u", "/x/~/y"},
		{"absolute path", "/etc/t.md", "/home/u", "/etc/t.md"},
		{"relative path", "t.md", "/home/u", "t.md"},
		{"no home known", "~/a", "", "~/a"},
		{"empty path", "", "/home/u", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandHome(tt.path, tt.home); got != filepath.FromSlash(tt.want) {
				t.Errorf("ExpandHome(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestReportTemplate(t *testing.T) {
	p, err := NewPaths(fakeEnv("/home/u", map[string]string{"KAGEGAMI_HOME": "/kg"}))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{"default when empty", "", "/kg/templates/pentest.md"},
		{"tilde path", "~/tpl/htb.md", "/home/u/tpl/htb.md"},
		{"absolute path", "/srv/tpl.md", "/srv/tpl.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Engagement.ReportTemplate = tt.configured
			if got := cfg.ReportTemplate(p, "/home/u"); got != filepath.FromSlash(tt.want) {
				t.Errorf("ReportTemplate = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTier2FallsBackToTier1(t *testing.T) {
	m := Model{Tier1: "ollama/llama3.1:8b"}
	if got := m.Tier2Spec(); got != "ollama/llama3.1:8b" {
		t.Errorf("Tier2Spec() = %q, want tier1", got)
	}
	m.Tier2 = "ollama/qwen2.5:14b"
	if got := m.Tier2Spec(); got != "ollama/qwen2.5:14b" {
		t.Errorf("Tier2Spec() = %q, want tier2", got)
	}
}

func TestDefaultsChooseOneModelAndALocalOllama(t *testing.T) {
	d := Default()
	if d.Limits.SessionTokens != 1_000_000 {
		t.Errorf("the session ceiling is %d, want 1000000: a ceiling is on by default", d.Limits.SessionTokens)
	}
	if d.Model.Tier1 != "ollama/llama3.1:8b" || d.Model.Tier2 != "" || d.Providers.Ollama.BaseURL != "http://localhost:11434" {
		t.Errorf("defaults = %+v %+v", d.Model, d.Providers)
	}
}

func TestOllamaAddressFromTheFile(t *testing.T) {
	cfg, err := parse([]byte("providers:\n  ollama:\n    base_url: http://192.168.64.1:11434\n"), "t.yaml")
	if err != nil || cfg.Providers.Ollama.BaseURL != "http://192.168.64.1:11434" {
		t.Errorf("%+v, %v", cfg.Providers, err)
	}
	if _, err := parse([]byte("providers:\n  ollama:\n    url: x\n"), "t.yaml"); err == nil {
		t.Error("an unknown key under providers.ollama was accepted")
	}
	if cfg, err := parse([]byte("model:\n  tier2: \"\"\n"), "t.yaml"); err != nil || cfg.Model.Tier2 != "" {
		t.Errorf("%+v, %v", cfg.Model, err)
	}
}
