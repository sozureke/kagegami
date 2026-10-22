package cli

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/splash"
)

func newSplashCmd() *cobra.Command {
	var loop bool
	var artFile string
	cmd := &cobra.Command{
		Use:   "splash",
		Short: help.Commands["splash"].Short,
		Long:  help.Commands["splash"].Long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
				return errors.New("the animation needs a terminal")
			}
			cfg, err := loadConfigOrDefault()
			if err != nil {
				return err
			}
			art := splash.Art
			if artFile != "" {
				data, err := os.ReadFile(artFile) //nolint:gosec // the person names the file to play
				if err != nil {
					return err
				}
				art = string(data)
			}
			s := cfg.Splash
			s.Loop = s.Loop || loop
			return splash.Run(cmd.Context(), art, s)
		},
	}
	cmd.Flags().BoolVar(&loop, "loop", false, "play it again and again until a key is pressed, to tune the settings")
	cmd.Flags().StringVar(&artFile, "art", "", "play this file instead of the built-in art")
	return cmd
}
