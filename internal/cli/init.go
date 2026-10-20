package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/setup"
)

// printResult lists what init did to each path, one line per item.
func printResult(w io.Writer, res setup.Result) {
	for _, item := range res.Items {
		_, _ = fmt.Fprintf(w, "%-12s %s\n", item.Action, item.Path)
		if item.Backup != "" {
			_, _ = fmt.Fprintf(w, "%-12s previous version kept as %s\n", "", item.Backup)
		}
	}
}

func newInitCmd() *cobra.Command {
	var force bool
	var caseName string

	cmd := &cobra.Command{
		Use:   "init",
		Short: help.Commands["init"].Short,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := config.OSEnv()
			if err != nil {
				return err
			}
			paths, err := config.NewPaths(env)
			if err != nil {
				return err
			}

			res, err := setup.Init(paths, setup.Options{Force: force, Case: caseName})
			printResult(cmd.OutOrStdout(), res) // show partial progress even on failure
			if errors.Is(err, config.ErrInvalidCaseName) {
				return &usageError{err: err}
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files (a .bak copy is kept)")
	cmd.Flags().StringVar(&caseName, "case", "", "also create this case")
	return cmd
}
