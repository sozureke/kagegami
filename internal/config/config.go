package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

var ErrNotFound = errors.New("config file not found")

type Config struct {
	Model      Model            `yaml:"model"`
	Privacy    Privacy          `yaml:"privacy"`
	Execution  Execution        `yaml:"execution"`
	Engagement Engagement       `yaml:"engagement"`
	Phases     map[string]Phase `yaml:"phases"`
	Capture    Capture          `yaml:"capture"`
	Logging    Logging          `yaml:"logging"`
}

type Model struct {
	Tier1         string `yaml:"tier1"`
	Tier2         string `yaml:"tier2"`
	CloudFallback bool   `yaml:"cloud_fallback"`
}

type Privacy struct {
	MaskCredentials bool `yaml:"mask_credentials"`
	MaskFlags       bool `yaml:"mask_flags"`
	MaskHosts       bool `yaml:"mask_hosts"`
}

type Execution struct {
	Mode               string `yaml:"mode"`
	ConfirmDestructive bool   `yaml:"confirm_destructive"`
}

type Engagement struct {
	ScaffoldFolders bool   `yaml:"scaffold_folders"`
	AutoReport      bool   `yaml:"auto_report"`
	ReportTemplate  string `yaml:"report_template"`
	Author          string `yaml:"author"`
}

type Phase struct {
	Emphasis string `yaml:"emphasis"`
}

type Capture struct {
	Trigger string   `yaml:"trigger"`
	Ignore  []string `yaml:"ignore"`
}

type Logging struct {
	Level string `yaml:"level"`
}

// Default returns the configuration used for every key the file leaves out.
func Default() Config {
	return Config{
		Model:      Model{Tier1: "ollama/llama3.1:8b", Tier2: "ollama/qwen2.5:14b"},
		Privacy:    Privacy{MaskCredentials: true, MaskFlags: true},
		Execution:  Execution{Mode: "propose", ConfirmDestructive: true},
		Engagement: Engagement{ScaffoldFolders: true, AutoReport: true},
		Phases:     map[string]Phase{},
		Capture:    Capture{Trigger: "shell-hooks", Ignore: []string{"cd", "ls", "clear", "pwd"}},
		Logging:    Logging{Level: "info"},
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the user's own config location by design
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return parse(data, path)
}

func parse(data []byte, name string) (Config, error) {
	cfg := Default()

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty file (for example a bare `touch config.yaml`) is valid and means "all defaults".
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse %s: %w", name, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s:\n%w", name, err)
	}
	return cfg, nil
}

var modelRE = regexp.MustCompile(`^\S+/\S+$`)

// Validate checks every value and reports all problems at once.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	oneOf := func(key, got string, allowed ...string) {
		if !slices.Contains(allowed, got) {
			add("%s: %q is invalid (expected %s)", key, got, quoteJoin(allowed))
		}
	}

	if !modelRE.MatchString(c.Model.Tier1) {
		add("model.tier1: %q is invalid (expected provider/model, e.g. ollama/llama3.1:8b)", c.Model.Tier1)
	}
	if !modelRE.MatchString(c.Model.Tier2) {
		add("model.tier2: %q is invalid (expected provider/model, e.g. ollama/qwen2.5:14b)", c.Model.Tier2)
	}
	oneOf("execution.mode", c.Execution.Mode, "propose", "approve-run")
	oneOf("capture.trigger", c.Capture.Trigger, "shell-hooks", "pipe-pane")
	oneOf("logging.level", c.Logging.Level, "debug", "info", "warn", "error")

	return errors.Join(errs...)
}

func quoteJoin(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// ExpandHome expands a leading "~/" to the home directory. Anything else, including
// "~user/...", is returned unchanged.
func ExpandHome(path, home string) string {
	if home != "" && strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// ReportTemplate returns the report template path: the configured one with "~"
// expanded, or the default pentest.md in the templates directory.
func (c Config) ReportTemplate(p Paths, home string) string {
	if c.Engagement.ReportTemplate == "" {
		return filepath.Join(p.TemplatesDir(), "pentest.md")
	}
	return ExpandHome(c.Engagement.ReportTemplate, home)
}
