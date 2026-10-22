package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/advice"
	"github.com/sozureke/kagegami/internal/agent"
	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/models"
	"github.com/sozureke/kagegami/internal/store"
)

func newNextCmd() *cobra.Command {
	var (
		caseName string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "next [request]",
		Short: help.Commands["next"].Short,
		Long:  help.Commands["next"].Long,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, rawDir, err := openCaseForAdvice(cmd, caseName)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			set, err := buildModelsForAdvice(cmd, st)
			if err != nil {
				return err
			}
			tr := &agent.Tier2{Provider: set.Tier2, Store: st, RawDir: rawDir, Options: agent.DefaultTier2Options()}
			res, err := tr.Next(cmd.Context(), requestText(args, "Suggest the next reconnaissance step."))
			if errors.Is(err, advice.ErrNoScope) {
				return &usageError{err: errors.New("the case has no scope: set one with 'kagegami case set --scope ...'")}
			}
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res.Suggestion)
			}
			printSuggestion(cmd.OutOrStdout(), res.Suggestion)
			return nil
		},
	}
	cmd.Flags().StringVar(&caseName, "case", "", "the case to advise on (needed when there is more than one)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the suggestion as JSON")
	return cmd
}

func printSuggestion(w io.Writer, s advice.Suggestion) {
	if s.NeedsRecon {
		fmt.Fprintf(w, "The map is too thin for a suggestion yet.\n%s\n", s.Rationale)
		return
	}
	fmt.Fprintf(w, "%s\n\n", s.Command)
	fmt.Fprintf(w, "why:        %s\n", s.Rationale)
	fmt.Fprintf(w, "expected:   %s\n", s.Expected)
	fmt.Fprintf(w, "risk:       %s\n", s.Risk)
	fmt.Fprintf(w, "confidence: %s\n", s.Confidence)
}

// requestText joins the words of a free-text argument, or falls back to def when none were given.
func requestText(args []string, def string) string {
	if s := strings.TrimSpace(strings.Join(args, " ")); s != "" {
		return s
	}
	return def
}

// openCaseForAdvice opens the case's database and returns the raw output directory next to it.
func openCaseForAdvice(cmd *cobra.Command, caseName string) (st *store.Store, rawDir string, err error) {
	env, err := config.OSEnv()
	if err != nil {
		return nil, "", err
	}
	paths, err := config.NewPaths(env)
	if err != nil {
		return nil, "", err
	}
	_, db, err := resolveCase(paths, caseName)
	if err != nil {
		return nil, "", err
	}
	st, err = store.Open(cmd.Context(), db)
	if err != nil {
		return nil, "", fmt.Errorf("open the case: %w", err)
	}
	return st, filepath.Join(filepath.Dir(db), "raw"), nil
}

// loadConfigOrDefault reads the configuration file, or falls back to the defaults if it has
// never been written (kagegami init creates it, but a model can still be asked about before that).
func loadConfigOrDefault() (config.Config, error) {
	env, err := config.OSEnv()
	if err != nil {
		return config.Config{}, err
	}
	paths, err := config.NewPaths(env)
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Load(paths.ConfigFile())
	if errors.Is(err, config.ErrNotFound) {
		return config.Default(), nil
	}
	return cfg, err
}

// buildModelsForAdvice loads the configuration and makes the models tier 2 (and tier 1, counted
// apart) needs, with usage recorded to the case. It warns once, to stderr, when a configured
// model is not on this machine.
func buildModelsForAdvice(cmd *cobra.Command, st *store.Store) (*models.Set, error) {
	cfg, err := loadConfigOrDefault()
	if err != nil {
		return nil, err
	}
	record := models.RecordTo(st, func(err error) {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not record model usage: %v\n", err)
	})
	set, err := models.Build(cmd.Context(), cfg, models.Options{Record: record})
	if err != nil {
		return nil, err
	}
	if set.Remote {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: a configured model is not on this machine; its input leaves the machine it runs on.")
	}
	return set, nil
}
