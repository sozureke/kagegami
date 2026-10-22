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
	var noAnim bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: help.Commands["watch"].Short,
		Long:  help.Commands["watch"].Long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
				return errors.New("the panel needs a terminal")
			}
			cfg, err := loadConfigOrDefault()
			if err != nil {
				return err
			}
			if cfg.Splash.Enabled && !noAnim && os.Getenv("KAGEGAMI_NO_ANIM") == "" {
				// the splash is only for show: a terminal that cannot play it goes on without
				_ = splash.Run(cmd.Context(), splash.Art, cfg.Splash)
			}
			m := tui.New(tui.Config{Version: "kagegami " + info.Version})
			_, err = tea.NewProgram(m).Run()
			return err
		},
	}
	cmd.Flags().BoolVar(&noAnim, "no-anim", false, "skip the opening animation (also KAGEGAMI_NO_ANIM=1, or splash.enabled: false in the config)")
	return cmd
}
