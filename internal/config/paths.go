package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const appName = "kagegami"

type Env struct {
	Getenv func(string) string
	Home   string // user home dir
}

func OSEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return Env{Getenv: os.Getenv, Home: home}, nil
}

// paths holds the resolved locations of configuration and data.
type Paths struct {
	configDir  string
	dataDir    string
	configFile string
}

func NewPaths(env Env) (Paths, error) {
	var p Paths

	if root := env.Getenv("KAGEGAMI_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return Paths{}, fmt.Errorf("KAGEGAMI_HOME must be an absolute path, got %q", root)
		}
		p.configDir = root
		p.dataDir = filepath.Join(root, "data")
	} else {
		var err error
		if p.configDir, err = xdgDir(env, "XDG_CONFIG_HOME", ".config"); err != nil {
			return Paths{}, err
		}
		if p.dataDir, err = xdgDir(env, "XDG_DATA_HOME", filepath.Join(".local", "share")); err != nil {
			return Paths{}, err
		}
	}

	p.configFile = filepath.Join(p.configDir, "config.yaml")
	if f := env.Getenv("KAGEGAMI_CONFIG"); f != "" {
		if !filepath.IsAbs(f) {
			return Paths{}, fmt.Errorf("KAGEGAMI_CONFIG must be an absolute path, got %q", f)
		}
		p.configFile = f
	}
	return p, nil
}

// xdgDir returns <$xdgVar>/kagegami, or <home>/<fallback>/kagegami when the variable is unset. As the XDG spec requires, a relative value is ignored.
func xdgDir(env Env, xdgVar, fallback string) (string, error) {
	if base := env.Getenv(xdgVar); base != "" && filepath.IsAbs(base) {
		return filepath.Join(base, appName), nil
	}
	if env.Home == "" {
		return "", fmt.Errorf("cannot locate the home directory: set KAGEGAMI_HOME or %s", xdgVar)
	}
	return filepath.Join(env.Home, fallback, appName), nil
}

func (p Paths) ConfigDir() string    { return p.configDir }
func (p Paths) ConfigFile() string   { return p.configFile }
func (p Paths) TemplatesDir() string { return filepath.Join(p.configDir, "templates") }
func (p Paths) DataDir() string      { return p.dataDir }
func (p Paths) CasesDir() string     { return filepath.Join(p.dataDir, "cases") }
func (p Paths) LogsDir() string      { return filepath.Join(p.dataDir, "logs") }

var (
	ErrInvalidCaseName = errors.New("invalid case name")
	caseNameRE         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

func ValidateCaseName(name string) error {
	if !caseNameRE.MatchString(name) {
		return fmt.Errorf("%w %q: use 1-64 characters from A-Z a-z 0-9 . _ - and start with a letter or digit", ErrInvalidCaseName, name)
	}
	return nil
}

func (p Paths) CaseDir(name string) (string, error) {
	if err := ValidateCaseName(name); err != nil {
		return "", err
	}
	return filepath.Join(p.CasesDir(), name), nil
}
