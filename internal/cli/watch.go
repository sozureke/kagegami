package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/pkg/tmux"
)

// tmuxLauncher is the part of tmux the watch launcher needs; *tmux.Client satisfies it.
type tmuxLauncher interface {
	InsideTmux() bool
	CurrentPane() (tmux.PaneID, error)
	Piped(ctx context.Context, pane tmux.PaneID) (bool, error)
	SplitWindow(ctx context.Context, o tmux.SplitOptions) (tmux.PaneID, error)
}

func newWatchCmd() *cobra.Command {
	var (
		caseName string
		target   string
		goal     string
		phase    string
		scope    []string
		addScope []string
		noAnim   bool
		percent  int
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: help.Commands["watch"].Short,
		Long:  help.Commands["watch"].Long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := tmux.New(tmux.ExecRunner{}, os.Getenv)
			changes := engagement.Changes{Phase: optional(cmd, "phase", phase), AddScope: addScope}
			changes.Target = optional(cmd, "target", target)
			changes.Goal = optional(cmd, "goal", goal)
			if cmd.Flags().Changed("scope") {
				changes.Scope = &scope
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("find my own path: %w", err)
			}
			return launchWatch(cmd.Context(), launchConfig{
				client:   client,
				exe:      exe,
				caseName: caseName,
				changes:  changes,
				noAnim:   noAnim,
				percent:  percent,
				stderr:   cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&caseName, "case", "", "the case to watch (needed when there is more than one)")
	cmd.Flags().StringVar(&target, "target", "", "set the target of the case before watching")
	cmd.Flags().StringVar(&goal, "goal", "", "set the goal of the case")
	cmd.Flags().StringVar(&phase, "phase", "", "set the phase of the case")
	cmd.Flags().StringArrayVar(&scope, "scope", nil, "set the scope (repeatable; replaces what is there)")
	cmd.Flags().StringArrayVar(&addScope, "add-scope", nil, "add an entry to the scope (repeatable)")
	cmd.Flags().BoolVar(&noAnim, "no-anim", false, "skip the opening animation in the panel")
	cmd.Flags().IntVar(&percent, "size", 42, "width of the panel pane, in percent of the window")
	return cmd
}

// optional returns a pointer to v when its flag was set, and nil otherwise, so an unset flag leaves
// the engagement field as it is.
func optional(cmd *cobra.Command, name, v string) *string {
	if cmd.Flags().Changed(name) {
		return &v
	}
	return nil
}

type launchConfig struct {
	client   tmuxLauncher
	exe      string
	caseName string
	changes  engagement.Changes
	noAnim   bool
	percent  int
	// getenv reads the environment of the shell the command was run in; nil means this process's.
	getenv func(string) string
	stderr interface{ Write([]byte) (int, error) }
}

// launchWatch opens the panel in a new tmux pane beside the pane the command was run in. The new
// pane runs `kagegami panel`, which does the watching; this process only sets things up and exits.
func launchWatch(ctx context.Context, c launchConfig) error {
	if !c.client.InsideTmux() {
		return errors.New("kagegami watch runs inside tmux: start tmux, then run it in the pane whose commands you want watched")
	}
	pane, err := c.client.CurrentPane()
	if err != nil {
		return err
	}

	if busy, err := c.client.Piped(ctx, pane); err != nil {
		return err
	} else if busy {
		return fmt.Errorf("pane %s is already being watched (by another kagegami, or by something else that pipes its output): close that first", pane)
	}

	env, err := config.OSEnv()
	if err != nil {
		return err
	}
	paths, err := config.NewPaths(env)
	if err != nil {
		return err
	}
	name, err := resolveWatchCase(ctx, paths, c.caseName, c.changes)
	if err != nil {
		return err
	}

	getenv := c.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	command := panelCommand(c.exe, panelArgs(pane, name, c.noAnim), forwardedEnv(getenv, forwardedVars))
	if _, err := c.client.SplitWindow(ctx, tmux.SplitOptions{
		Target:     pane,
		Horizontal: true,
		Percent:    c.percent,
		Command:    command,
		KeepFocus:  true, // the person stays in their shell; the panel draws itself beside it
		Env:        forwardedEnv(getenv, secretVars),
	}); err != nil {
		return fmt.Errorf("open the panel pane: %w", err)
	}
	_, _ = fmt.Fprintf(c.stderr, "kagegami: panel opened beside pane %s, watching case %q\n", pane, name)
	return nil
}

// forwardedVars are the settings of the shell that decide where Kagegami keeps its files and what the
// panel plays. A new tmux pane gets the environment of the tmux server, not of the shell it was
// opened from, so a variable set in the shell's startup file would otherwise be lost: the panel
// would look for the case in another place and find none.
var forwardedVars = []string{"KAGEGAMI_HOME", "KAGEGAMI_CONFIG", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "KAGEGAMI_NO_ANIM"}

// secretVars are the variables that hold a key. They reach the panel through the environment of its
// pane (tmux -e) and never through the text of its command, which any process can read in the list
// of processes.
var secretVars = []string{"OPENROUTER_API_KEY"}

// forwardedEnv returns the NAME=value pairs of those of names that are set.
func forwardedEnv(getenv func(string) string, names []string) []string {
	var out []string
	for _, name := range names {
		if v := getenv(name); v != "" {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// panelCommand is the shell command tmux runs in the new pane. If the panel stops with an error the
// pane would vanish with the message in it, so on failure it waits for Enter.
func panelCommand(exe string, args, env []string) string {
	var b strings.Builder
	if len(env) > 0 {
		b.WriteString("env")
		for _, e := range env {
			b.WriteString(" " + tmux.ShellQuote(e))
		}
		b.WriteString(" ")
	}
	b.WriteString(tmux.ShellQuote(exe))
	for _, a := range args {
		b.WriteString(" " + tmux.ShellQuote(a))
	}
	b.WriteString(` || { printf '\nkagegami: the panel stopped (see above). Press Enter to close this pane.\n'; read -r _; }`)
	return b.String()
}

// panelArgs are the arguments of the panel process that runs in the new pane.
func panelArgs(pane tmux.PaneID, caseName string, noAnim bool) []string {
	args := []string{"panel", "--pane", string(pane), "--case", caseName}
	if noAnim {
		args = append(args, "--no-anim")
	}
	return args
}

// resolveWatchCase applies the engagement changes (when any flag was given) and returns the name of
// the case to watch. With no changes it just finds the case the person means.
func resolveWatchCase(ctx context.Context, paths config.Paths, caseName string, changes engagement.Changes) (string, error) {
	if !changes.Any() {
		name, _, err := resolveCase(paths, caseName)
		return name, err
	}
	name, db, err := resolveCase(paths, caseName)
	if err != nil {
		return "", err
	}
	st, err := store.Open(ctx, db)
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()
	if _, err := engagement.Update(ctx, st, name, changes); err != nil {
		return "", err
	}
	return name, nil
}
