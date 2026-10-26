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
	"github.com/sozureke/kagegami/pkg/llm/openrouter"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run() error {
	model := flag.String("model", "llama3.1:8b", "the model to test: an Ollama model, or openrouter/<vendor>/<model> (the key is read from OPENROUTER_API_KEY)")
	baseURL := flag.String("base-url", ollama.DefaultBaseURL, "the address of Ollama; it must be on this machine")
	repeats := flag.Int("repeats", 3, "runs of each case of tier 2")
	variants := flag.String("variants", "both", "the prompts to run: both, example or no-example")
	only := flag.String("only", "", "run only the cases whose name starts with this (tier 1 cases start with tier1/)")
	temperature := flag.Float64("temperature", 0, "the temperature of tier 2; leave it out for the model's default")
	out := flag.String("out", "", "where to write the report; - for the screen; default docs/eval/<date>-<model>.md")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p, err := provider(ctx, *model, *baseURL)
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

// provider makes the model to test. A model that runs on this machine needs nothing else. One at
// OpenRouter is used only because it is asked for by name: the fixtures are made up and hold no real
// data, which is what makes it fine to send them there, and a real case never goes this way.
func provider(ctx context.Context, model, baseURL string) (llm.Provider, error) {
	if spec, err := llm.ParseSpec(model); err == nil && spec.Provider == "openrouter" {
		p, err := openrouter.New(openrouter.Config{Model: spec.Model, APIKey: os.Getenv(openrouter.KeyEnv)})
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, "eval: the made-up fixtures are sent to OpenRouter, model", spec.Model)
		return p, nil
	}
	local, err := llm.IsLocal(ctx, baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("-base-url: %w", err)
	}
	if !local {
		return nil, fmt.Errorf("-base-url %s is not on this machine; to use a model elsewhere, name it openrouter/<vendor>/<model>", baseURL)
	}
	return ollama.New(ollama.Config{Model: model, BaseURL: baseURL})
}
