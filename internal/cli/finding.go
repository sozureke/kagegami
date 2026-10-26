package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/store"
)

func newFindingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "finding",
		Short: help.Commands["finding"].Short,
		Long:  help.Commands["finding"].Long,
		Args:  rejectArgs,
		RunE:  printHelp,
	}
	cmd.AddCommand(newFindingAddCmd())
	return cmd
}

func newFindingAddCmd() *cobra.Command {
	var (
		caseName    string
		severity    string
		description string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "add TITLE",
		Short: help.Commands["finding add"].Short,
		Long:  help.Commands["finding add"].Long,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return newUsageError("give a title for the finding")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openCaseStore(cmd, caseName)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			f := store.Finding{Severity: severity, Title: strings.Join(args, " "), Description: description}
			added, err := st.Repo().AddFinding(cmd.Context(), f)
			if errors.Is(err, store.ErrInvalid) {
				return &usageError{err: err}
			}
			if err != nil {
				return err
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(findingJSON(added))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "finding %d added: [%s] %s\n", added.ID, added.Severity, added.Title)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&caseName, "case", "", "the case to add the finding to (needed when there is more than one)")
	f.StringVar(&severity, "severity", "", "info, low, medium, high or critical")
	f.StringVar(&description, "description", "", "what was found, and why it matters")
	f.BoolVar(&asJSON, "json", false, "print the added finding as JSON")
	return cmd
}

func findingJSON(f store.Finding) map[string]any {
	return map[string]any{
		"id":          f.ID,
		"severity":    f.Severity,
		"title":       f.Title,
		"description": f.Description,
		"created_at":  f.CreatedAt,
	}
}
