package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/setup"
	"github.com/sozureke/kagegami/pkg/tmux"
)

// startOptions are what a bare `kagegami` can be told.
type startOptions struct {
	caseName string
	noAnim   bool
	percent  int
	stderr   io.Writer
}

// runStart is what a bare `kagegami` does: make sure the configuration and the case of this directory
// exist (saying so in the panel when it had to create them), open the panel beside the shell, and,
// when not inside tmux, start a tmux session for it and attach to it.
func runStart(ctx context.Context, o startOptions) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("kagegami lives in tmux, which is not installed: install it (for example, brew install tmux) and run kagegami again")
	}
	env, err := config.OSEnv()
	if err != nil {
		return err
	}
	paths, err := config.NewPaths(env)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the current directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find my own path: %w", err)
	}

	name := o.caseName
	if name == "" {
		name = caseNameForDir(cwd)
	}
	res, err := setup.Init(paths, setup.Options{Case: name})
	if err != nil {
		return err
	}
	notice := createdNotice(paths, name, cwd, res)

	client := tmux.New(tmux.ExecRunner{}, os.Getenv)
	cfg := launchConfig{client: client, exe: exe, caseName: name, noAnim: o.noAnim, percent: o.percent, notice: notice, stderr: o.stderr}
	if client.InsideTmux() {
		return launchWatch(ctx, cfg)
	}

	session := "kagegami-" + name
	runner := tmux.ExecRunner{Binary: tmuxPath}
	if _, err := runner.Run(ctx, "has-session", "-t", "="+session); err != nil { // no such session yet
		out, err := runner.Run(ctx, "new-session", "-d", "-s", session, "-c", cwd, "-P", "-F", "#{pane_id}")
		if err != nil {
			return fmt.Errorf("start tmux: %w", err)
		}
		pane, err := tmux.ParsePaneID(strings.TrimSpace(out))
		if err != nil {
			return err
		}
		cfg.pane = pane
		if err := launchWatch(ctx, cfg); err != nil {
			_, _ = runner.Run(ctx, "kill-session", "-t", "="+session)
			return err
		}
	}
	// the shell becomes tmux: nothing of this process is left to clean up
	return syscall.Exec(tmuxPath, []string{"tmux", "attach-session", "-t", "=" + session}, os.Environ()) //nolint:gosec // tmux found in PATH, arguments fixed or validated above
}

// interactive says whether a person is at both ends of the command, which is when a bare `kagegami`
// opens the app; a script that runs it gets the help.
func interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// caseNameForDir is the name of the case of a directory: its own name, turned into one that is valid.
func caseNameForDir(dir string) string {
	var b strings.Builder
	for _, r := range filepath.Base(dir) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.TrimLeft(b.String(), "._-")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		return "case"
	}
	return name
}

// createdNotice tells the tester, in the panel, what was set up for them. It is empty when everything
// was there already.
func createdNotice(p config.Paths, name, cwd string, res setup.Result) string {
	caseDir, _ := p.CaseDir(name)
	var lines []string
	for _, item := range res.Items {
		if item.Action != setup.Created {
			continue
		}
		switch item.Path {
		case p.ConfigFile():
			lines = append(lines, "first run: created your config, "+item.Path)
		case caseDir:
			lines = append(lines, fmt.Sprintf("created the case %q for %s", name, cwd))
		}
	}
	return strings.Join(lines, "\n")
}
