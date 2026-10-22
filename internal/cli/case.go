package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/store"
)

func newCaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "case",
		Short: help.Commands["case"].Short,
		Long:  help.Commands["case"].Long,
		Args:  rejectArgs,
		RunE:  printHelp,
	}
	cmd.AddCommand(newCaseSetCmd(), newCaseShowCmd())
	return cmd
}

func newCaseSetCmd() *cobra.Command {
	var (
		caseName string
		target   string
		goal     string
		roe      string
		phase    string
		scope    []string
		addScope []string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: help.Commands["case set"].Short,
		Long:  help.Commands["case set"].Long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var c engagement.Changes
			f := cmd.Flags()
			if f.Changed("target") {
				c.Target = &target
			}
			if f.Changed("goal") {
				c.Goal = &goal
			}
			if f.Changed("roe") {
				c.ROE = &roe
			}
			if f.Changed("phase") {
				c.Phase = &phase
			}
			if f.Changed("scope") {
				c.Scope = &scope
			}
			c.AddScope = addScope
			if c.Target == nil && c.Goal == nil && c.ROE == nil && c.Phase == nil && c.Scope == nil && len(c.AddScope) == 0 {
				return newUsageError("nothing to set: give at least one of --target, --goal, --scope, --add-scope, --roe, --phase")
			}

			st, name, err := openCaseStore(cmd, caseName)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			res, err := engagement.Update(cmd.Context(), st, name, c)
			if errors.Is(err, engagement.ErrInvalid) {
				return &usageError{err: err}
			}
			if err != nil {
				return err
			}
			printEngagement(cmd.OutOrStdout(), res.Engagement)
			if res.ScopeFromTarget {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nThe scope was empty, so it is now the target. Change it with --scope.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&caseName, "case", "", "the case (needed when there is more than one)")
	f.StringVar(&target, "target", "", "the target: an address, a network or a name")
	f.StringVar(&goal, "goal", "", "what the engagement is after, for example \"user and root flag\"")
	f.StringVar(&roe, "roe", "", "rules of engagement: what is not allowed")
	f.StringVar(&phase, "phase", "", "the phase, for example recon or web")
	f.StringArrayVar(&scope, "scope", nil, "what may be touched: an address, a network (10.0.0.0/24) or a range (10.0.0.5-10.0.0.20); repeat the flag; replaces the scope")
	f.StringArrayVar(&addScope, "add-scope", nil, "like --scope, but adds to the scope")
	return cmd
}

func newCaseShowCmd() *cobra.Command {
	var (
		caseName string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: help.Commands["case show"].Short,
		Long:  help.Commands["case show"].Long,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, name, err := openCaseStore(cmd, caseName)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			e, err := engagement.Get(cmd.Context(), st)
			if errors.Is(err, store.ErrNotFound) {
				e = store.Engagement{Name: name}
			} else if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(engagementJSON(e))
			}
			printEngagement(cmd.OutOrStdout(), e)
			return nil
		},
	}
	cmd.Flags().StringVar(&caseName, "case", "", "the case (needed when there is more than one)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

// openCaseStore opens the database of the case the person means and returns the case's name.
func openCaseStore(cmd *cobra.Command, caseName string) (*store.Store, string, error) {
	env, err := config.OSEnv()
	if err != nil {
		return nil, "", err
	}
	paths, err := config.NewPaths(env)
	if err != nil {
		return nil, "", err
	}
	name, db, err := resolveCase(paths, caseName)
	if err != nil {
		return nil, "", err
	}
	st, err := store.Open(cmd.Context(), db)
	if err != nil {
		return nil, "", fmt.Errorf("open the case: %w", err)
	}
	return st, name, nil
}

func printEngagement(w io.Writer, e store.Engagement) {
	show := func(label, v string) {
		if v == "" {
			v = "-"
		}
		_, _ = fmt.Fprintf(w, "%-8s %s\n", label, v)
	}
	show("case", e.Name)
	show("target", e.Target)
	show("goal", e.Goal)
	show("scope", e.Scope)
	show("phase", e.Phase)
	show("roe", e.ROE)
	if e.Scope == "" {
		_, _ = fmt.Fprintln(w, "\nNo scope is set: commands are read by parsers only, the model is off, and next and pipeline will not run.\nSet it with: kagegami case set --scope 10.10.10.0/24")
	}
}

func engagementJSON(e store.Engagement) map[string]any {
	scope := engagement.Scope(e).Entries()
	if scope == nil {
		scope = []string{}
	}
	return map[string]any{"case": e.Name, "target": e.Target, "goal": e.Goal, "scope": scope, "phase": e.Phase, "roe": e.ROE}
}
