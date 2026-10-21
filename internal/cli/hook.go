package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/hook"
)

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hook <shell>",
		Short: help.Commands["hook"].Short,
		Long:  help.Commands["hook"].Long,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: fmt.Errorf("%w (supported: %s)", err, strings.Join(hook.Shells(), ", "))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := config.OSEnv()
			if err != nil {
				return err
			}
			paths, err := config.NewPaths(env)
			if err != nil {
				return err
			}
			script, err := hook.Render(args[0], paths.RunDir())
			if errors.Is(err, hook.ErrUnknownShell) {
				return &usageError{err: err}
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), script)
			return err
		},
	}
}
