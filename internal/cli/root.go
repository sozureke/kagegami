package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func rejectArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return newUsageError("unknown command %q", args[0])
	}
	return nil
}

func printHelp(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func newRootCmd(stdout, stderr io.Writer, info BuildInfo) *cobra.Command {
	var start startOptions
	root := &cobra.Command{
		Use:           "kagegami",
		Short:         help.Root.Short,
		Long:          help.Root.Long,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          rejectArgs,
		// at a terminal a bare kagegami opens the app; a script that runs it gets the help
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive() {
				return printHelp(cmd, args)
			}
			start.stderr = cmd.ErrOrStderr()
			return runStart(cmd.Context(), start)
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.Flags().StringVar(&start.caseName, "case", "", "the case to open (default: the one for this directory, made if there is none)")
	root.Flags().BoolVar(&start.noAnim, "no-anim", false, "skip the opening animation")
	root.Flags().IntVar(&start.percent, "size", 42, "width of the panel pane, in percent of the window")

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})

	root.AddCommand(
		newInitCmd(),
		newWatchCmd(),
		newPanelCmd(info),
		newSplashCmd(),
		newHookCmd(),
		newNextCmd(),
		newPipelineCmd(),
		newReportCmd(),
		newMapCmd(),
		newCaseCmd(),
		newVersionCmd(info),
	)
	return root
}

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func Run(args []string, stdout, stderr io.Writer, info BuildInfo) int {
	if args == nil {
		args = []string{}
	}

	root := newRootCmd(stdout, stderr, info)
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return 0
	}

	var ue *usageError
	if errors.As(err, &ue) {
		_, _ = fmt.Fprintf(stderr, "kagegami: %v\nRun 'kagegami --help' for usage.\n", err)
		return 2
	}
	_, _ = fmt.Fprintf(stderr, "kagegami: %v\n", err)
	return 1
}
