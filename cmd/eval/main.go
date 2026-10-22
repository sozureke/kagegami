// Command eval runs the fixed cases of internal/eval against a model on this machine and writes a
// report to docs/eval. It is run by hand (make eval), never in CI: it needs a model.
//
//	go run ./cmd/eval [-model llama3.1:8b] [-repeats 3] [-only web-box] [-out path|-]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/sozureke/kagegami/internal/eval"
	"github.com/sozureke/kagegami/pkg/llm"
	"github.com/sozureke/kagegami/pkg/llm/ollama"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run() error {
	model := flag.String("model", "llama3.1:8b", "the Ollama model to test")
	baseURL := flag.String("base-url", ollama.DefaultBaseURL, "the address of Ollama; it must be on this machine")
	repeats := flag.Int("repeats", 3, "runs of each case of tier 2")
	variants := flag.String("variants", "both", "the prompts to run: both, example or no-example")
	only := flag.String("only", "", "run only the cases whose name starts with this (tier 1 cases start with tier1/)")
	temperature := flag.Float64("temperature", 0, "the temperature of tier 2; leave it out for the model's default")
	out := flag.String("out", "", "where to write the report; - for the screen; default docs/eval/<date>-<model>.md")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The fixtures hold no real data, but the rule is the same as for the program: a model that is
	// not on this machine is used only when someone has decided so, and an eval is not that decision.
	local, err := llm.IsLocal(ctx, *baseURL, nil)
	if err != nil {
		return fmt.Errorf("-base-url: %w", err)
	}
	if !local {
		return fmt.Errorf("-base-url %s is not on this machine; the eval runs against a local model only", *baseURL)
	}
	p, err := ollama.New(ollama.Config{Model: *model, BaseURL: *baseURL})
	if err != nil {
		return err
	}

	cfg := eval.Config{Provider: p, Repeats: *repeats, Only: *only, Progress: func(l string) { fmt.Fprintln(os.Stderr, l) }}
	switch *variants {
	case "both":
	case eval.VariantExample, eval.VariantNoExample:
		cfg.Variants = []string{*variants}
	default:
		return fmt.Errorf("-variants %q: use both, example or no-example", *variants)
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "temperature" {
			cfg.Temperature = temperature
		}
	})

	rep, runErr := eval.Run(ctx, cfg)
	if len(rep.Outcomes) == 0 {
		return runErr
	}
	// An interrupted or failed run still has results worth keeping.
	if err := write(rep, *out); err != nil {
		return err
	}
	return runErr
}

func write(rep eval.Report, out string) error {
	if out == "-" {
		return rep.WriteMarkdown(os.Stdout)
	}
	if out == "" {
		name := strings.NewReplacer(":", "-", "/", "-").Replace(rep.Model)
		out = filepath.Join("docs", "eval", rep.When.Format("2006-01-02")+"-"+name+".md")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	f, err := os.Create(out) //nolint:gosec // the path is the one the person gave, or under docs/eval
	if err != nil {
		return err
	}
	if err := rep.WriteMarkdown(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "report written to", out)
	return nil
}
