package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func newUsageError(format string, a ...any) error {
	return &usageError{err: fmt.Errorf(format, a...)}
}

func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return &usageError{err: err}
	}
	return nil
}
