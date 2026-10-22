package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/mapview"
	"github.com/sozureke/kagegami/internal/store"
)

func newMapCmd() *cobra.Command {
	var (
		caseName string
		asJSON   bool
		all      bool
		stats    bool
	)
	cmd := &cobra.Command{
		Use:   "map",
		Short: help.Commands["map"].Short,
		Long:  help.Commands["map"].Long,
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
			dbPath, err := caseDatabase(paths, caseName)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			st, err := store.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("open the case: %w", err)
			}
			defer func() { _ = st.Close() }()

			view, err := mapview.Load(ctx, st)
			if err != nil {
				return fmt.Errorf("read the map: %w", err)
			}
			if asJSON {
				return mapview.JSON(cmd.OutOrStdout(), view)
			}
			return mapview.Text(cmd.OutOrStdout(), view, mapview.Options{All: all, Stats: stats})
		},
	}
	cmd.Flags().StringVar(&caseName, "case", "", "the case to show (needed when there is more than one)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the whole map as JSON")
	cmd.Flags().BoolVar(&stats, "stats", false, "show what the models have used on the case")
	cmd.Flags().BoolVar(&all, "all", false, "show closed ports, every path and every note")
	return cmd
}

// caseDatabase finds the database of a case that exists. It never creates one: a misspelled
// case name must not leave an empty case behind.
func caseDatabase(p config.Paths, name string) (string, error) {
	if name == "" {
		cases, err := listCases(p)
		if err != nil {
			return "", err
		}
		switch len(cases) {
		case 0:
			return "", fmt.Errorf("there is no case yet: create one with 'kagegami init --case NAME'")
		case 1:
			name = cases[0]
		default:
			return "", newUsageError("there are several cases (%s): choose one with --case", strings.Join(cases, ", "))
		}
	}
	dir, err := p.CaseDir(name)
	if errors.Is(err, config.ErrInvalidCaseName) {
		return "", &usageError{err: err}
	}
	if err != nil {
		return "", err
	}
	db := filepath.Join(dir, store.FileName)
	info, err := os.Stat(db)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("there is no case %q: create it with 'kagegami init --case %s'", name, name)
	}
	return db, nil
}

// listCases returns the names of the cases that have a database, sorted.
func listCases(p config.Paths) ([]string, error) {
	entries, err := os.ReadDir(p.CasesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the cases: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || config.ValidateCaseName(e.Name()) != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(p.CasesDir(), e.Name(), store.FileName)); err == nil && info.Mode().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
