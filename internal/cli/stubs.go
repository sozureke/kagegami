package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func notImplemented(cmd *cobra.Command, _ []string) error {
	return fmt.Errorf("%s is not implemented yet", cmd.Name())
}

func newStubCmd(name string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: help.Commands[name].Short,
		Args:  noArgs,
		RunE:  notImplemented,
	}
}

func newInitCmd() *cobra.Command   { return newStubCmd("init") }
func newWatchCmd() *cobra.Command  { return newStubCmd("watch") }
func newNextCmd() *cobra.Command   { return newStubCmd("next") }
func newReportCmd() *cobra.Command { return newStubCmd("report") }
func newMapCmd() *cobra.Command    { return newStubCmd("map") }
