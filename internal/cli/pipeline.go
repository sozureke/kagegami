package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/advice"
	"github.com/sozureke/kagegami/internal/agent"
	"github.com/sozureke/kagegami/internal/store"
)

func newPipelineCmd() *cobra.Command {
	var (
		caseName string
		asJSON   bool
		history  bool
		show     int64
	)
	cmd := &cobra.Command{
		Use:   "pipeline [request]",
		Short: help.Commands["pipeline"].Short,
		Long:  help.Commands["pipeline"].Long,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, rawDir, err := openCaseForAdvice(cmd, caseName)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			switch {
			case history:
				runs, err := st.Repo().ListPipelineRuns(cmd.Context())
				if err != nil {
					return err
				}
				return printPipelineHistory(cmd.OutOrStdout(), runs, asJSON)
			case show > 0:
				run, err := st.Repo().GetPipelineRun(cmd.Context(), show)
				if errors.Is(err, store.ErrNotFound) {
					return &usageError{err: fmt.Errorf("there is no pipeline run %d", show)}
				}
				if err != nil {
					return err
				}
				return printPipelineRun(cmd.OutOrStdout(), run, asJSON)
			}

			set, err := buildModelsForAdvice(cmd, st)
			if err != nil {
				return err
			}
			tr := &agent.Tier2{Provider: set.Tier2, Store: st, RawDir: rawDir, Options: agent.DefaultTier2Options()}
			res, err := tr.Plan(cmd.Context(), requestText(args, "Lay out a pipeline for this target."))
			if errors.Is(err, advice.ErrNoScope) {
				return &usageError{err: errors.New("the case has no scope: set one with 'kagegami case set --scope ...'")}
			}
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res.Pipeline)
			}
			printPipeline(cmd.OutOrStdout(), res.Pipeline, res.Dropped)
			return nil
		},
	}
	cmd.Flags().StringVar(&caseName, "case", "", "the case to plan for (needed when there is more than one)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().BoolVar(&history, "history", false, "list saved runs instead of asking the model again")
	cmd.Flags().Int64Var(&show, "show", 0, "print one saved run by id instead of asking the model again")
	return cmd
}

func printPipeline(w io.Writer, p advice.Pipeline, dropped []advice.Drop) {
	if p.NeedsRecon {
		fmt.Fprintf(w, "The map is too thin for a pipeline yet.\n%s\n", p.Summary)
		return
	}
	fmt.Fprintf(w, "%s\n\n", p.Summary)
	for _, s := range p.Steps {
		fmt.Fprintf(w, "%d. [%s] %s\n   why: %s\n", s.Priority, s.Confidence, s.Vector, s.Why)
		for _, c := range s.Commands {
			fmt.Fprintf(w, "   $ %s\n", c)
		}
	}
	if len(dropped) > 0 {
		fmt.Fprintf(w, "\n%d step(s) or commands were left out:\n", len(dropped))
		for _, d := range dropped {
			fmt.Fprintf(w, "  - %s\n", d.String())
		}
	}
}

func printPipelineRun(w io.Writer, run store.PipelineRun, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(run)
	}
	var steps []advice.Step
	if err := json.Unmarshal([]byte(run.Steps), &steps); err != nil {
		return fmt.Errorf("the stored run is not readable: %w", err)
	}
	fmt.Fprintf(w, "run %d, %s, model %s\n", run.ID, run.CreatedAt.UTC().Format("2006-01-02 15:04"), run.Model)
	printPipeline(w, advice.Pipeline{Summary: run.Summary, Steps: steps}, nil)
	return nil
}

func printPipelineHistory(w io.Writer, runs []store.PipelineRun, asJSON bool) error {
	if asJSON {
		if runs == nil {
			runs = []store.PipelineRun{}
		}
		return json.NewEncoder(w).Encode(runs)
	}
	if len(runs) == 0 {
		fmt.Fprintln(w, "No pipeline has been run yet.")
		return nil
	}
	for _, r := range runs {
		fmt.Fprintf(w, "%d  %s  %s  %s\n", r.ID, r.CreatedAt.UTC().Format("2006-01-02 15:04"), r.Model, oneLineFor(r.Summary))
	}
	return nil
}

func oneLineFor(s string) string {
	const limit = 100
	r := []rune(s)
	for i, c := range r {
		if c == '\n' {
			r = r[:i]
			break
		}
	}
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return string(r)
}
