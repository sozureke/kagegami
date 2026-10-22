package cli

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/splash"
	"github.com/sozureke/kagegami/internal/tui"
)

func newWatchCmd(info BuildInfo) *cobra.Command {
	var demo, noAnim bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: help.Commands["watch"].Short,
		Long:  help.Commands["watch"].Long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !demo {
				return errors.New("watch is not implemented yet: try 'kagegami watch --demo' to see the panel")
			}
			if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
				return errors.New("the panel needs a terminal")
			}
			skip := noAnim || os.Getenv("KAGEGAMI_NO_ANIM") != ""
			if !skip {
				// the splash is only for show: a terminal that cannot play it goes on without
				_ = splash.Run(cmd.Context(), splash.Art, splash.DefaultConfig())
			}
			m := tui.New(tui.Config{Version: "kagegami " + info.Version, Demo: true, NoAnim: true})
			_, err := tea.NewProgram(m).Run()
			return err
		},
	}
	cmd.Flags().BoolVar(&demo, "demo", false, "play a made-up session in this terminal, to try the panel out")
	cmd.Flags().BoolVar(&noAnim, "no-anim", false, "skip the opening animation (also KAGEGAMI_NO_ANIM=1)")
	return cmd
}
