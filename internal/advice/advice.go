// Package advice holds what tier 2 answers with, a single next step or a pipeline, and the checks
// that an answer of a model must pass before a person sees it as advice. A model can give a command
// that is well formed and wrong: against an address nobody is testing, chained with others, or
// aimed at a service that is not on the map. The schema cannot tell; these checks can.
package advice

import (
	"bytes"
	_ "embed" // the schemas are part of the binary
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// SchemaVersion is the version of the shapes below. It changes whenever the JSON of an answer does.
const SchemaVersion = 1

//go:embed suggestion.schema.json
var suggestionSchema []byte

//go:embed pipeline.schema.json
var pipelineSchema []byte

// SuggestionSchema is the JSON Schema of a Suggestion, as given to a model.
func SuggestionSchema() json.RawMessage { return bytes.Clone(suggestionSchema) }

// PipelineSchema is the JSON Schema of a Pipeline, as given to a model.
func PipelineSchema() json.RawMessage { return bytes.Clone(pipelineSchema) }

// Levels used for risk and confidence.
var levels = []string{"low", "medium", "high"}

// Suggestion is the next step: one command and why.
type Suggestion struct {
	// Command is one command line to run.
	Command string `json:"command"`
	// Rationale says why, in terms of what is on the map.
	Rationale string `json:"rationale"`
	// Expected says what the command should show.
	Expected string `json:"expected"`
	// Risk is how noisy or intrusive the command is: low, medium or high.
	Risk string `json:"risk"`
	// Confidence is how sure the model is that this is the right step: low, medium or high.
	Confidence string `json:"confidence"`
	// NeedsRecon says that the map is too thin to suggest anything: the Command is empty and
	// the Rationale says what is missing.
	NeedsRecon bool `json:"needs_recon"`
}

// Pipeline is a plan for the whole map.
type Pipeline struct {
	// Summary is the state of the target in a few sentences.
	Summary string `json:"summary"`
	Steps   []Step `json:"steps"`
	// NeedsRecon says that the map is too thin for a plan.
	NeedsRecon bool `json:"needs_recon"`
}

// Step is one stage of a pipeline.
type Step struct {
	// Priority orders the steps, 1 first.
	Priority int `json:"priority"`
	// Service is the service the step is about, written <address>:<port>/<proto>, or just <address>.
	Service string `json:"service"`
	// Vector names the approach, such as "web content discovery".
	Vector string `json:"vector"`
	// Why says why this step, in terms of what is on the map.
	Why string `json:"why"`
	// Commands to run for the step; empty when Confidence is low.
	Commands   []string `json:"commands"`
	Confidence string   `json:"confidence"`
}

// ErrInvalid is wrapped by errors about an answer that cannot be used as it is.
var ErrInvalid = errors.New("invalid advice")

const (
	maxText    = 2000
	maxCommand = 500
	maxSteps   = 12
	maxCmds    = 5
)

// ParseSuggestion reads a Suggestion from JSON strictly: unknown fields and text after the value
// are errors. The result is normalized (text trimmed, levels in lower case) and its shape checked;
// whether the command may be run is Check's business.
func ParseSuggestion(data []byte) (Suggestion, error) {
	var s Suggestion
	if err := decode(data, &s); err != nil {
		return Suggestion{}, err
	}
	s.Command = strings.TrimSpace(s.Command)
	s.Rationale = strings.TrimSpace(s.Rationale)
	s.Expected = strings.TrimSpace(s.Expected)
	s.Risk = strings.ToLower(strings.TrimSpace(s.Risk))
	s.Confidence = strings.ToLower(strings.TrimSpace(s.Confidence))
	if err := firstError(
		oneOf("risk", s.Risk), oneOf("confidence", s.Confidence),
		length("rationale", s.Rationale, maxText), length("expected", s.Expected, maxText),
		length("command", s.Command, maxCommand),
		printable("rationale", s.Rationale), printable("expected", s.Expected),
	); err != nil {
		return Suggestion{}, err
	}
	switch {
	case s.NeedsRecon && s.Command != "":
		return Suggestion{}, fmt.Errorf("%w: needs_recon with a command", ErrInvalid)
	case !s.NeedsRecon && s.Command == "":
		return Suggestion{}, fmt.Errorf("%w: no command and no needs_recon", ErrInvalid)
	case s.Rationale == "":
		return Suggestion{}, fmt.Errorf("%w: no rationale", ErrInvalid)
	}
	return s, nil
}

// ParsePipeline reads a Pipeline from JSON strictly and normalizes it. Steps are put in priority
// order; a step needs a priority, a service, a vector and a why.
func ParsePipeline(data []byte) (Pipeline, error) {
	var p Pipeline
	if err := decode(data, &p); err != nil {
		return Pipeline{}, err
	}
	p.Summary = strings.TrimSpace(p.Summary)
	if err := firstError(length("summary", p.Summary, maxText), printable("summary", p.Summary)); err != nil {
		return Pipeline{}, err
	}
	if len(p.Steps) > maxSteps {
		return Pipeline{}, fmt.Errorf("%w: %d steps, the limit is %d", ErrInvalid, len(p.Steps), maxSteps)
	}
	for i := range p.Steps {
		st := &p.Steps[i]
		st.Service = strings.TrimSpace(st.Service)
		st.Vector = strings.TrimSpace(st.Vector)
		st.Why = strings.TrimSpace(st.Why)
		st.Confidence = strings.ToLower(strings.TrimSpace(st.Confidence))
		if len(st.Commands) > maxCmds {
			return Pipeline{}, fmt.Errorf("%w: step %d has %d commands, the limit is %d", ErrInvalid, i, len(st.Commands), maxCmds)
		}
		for j := range st.Commands {
			st.Commands[j] = strings.TrimSpace(st.Commands[j])
			if err := length("command", st.Commands[j], maxCommand); err != nil {
				return Pipeline{}, fmt.Errorf("step %d: %w", i, err)
			}
		}
		if err := firstError(
			oneOf("confidence", st.Confidence), required("service", st.Service), required("vector", st.Vector), required("why", st.Why),
			length("service", st.Service, 200), length("vector", st.Vector, 200), length("why", st.Why, maxText),
			printable("vector", st.Vector), printable("why", st.Why), printable("service", st.Service),
		); err != nil {
			return Pipeline{}, fmt.Errorf("step %d: %w", i, err)
		}
		if st.Priority < 1 || st.Priority > maxSteps {
			return Pipeline{}, fmt.Errorf("%w: step %d has priority %d", ErrInvalid, i, st.Priority)
		}
	}
	sortSteps(p.Steps)
	if p.NeedsRecon && len(p.Steps) > 0 {
		return Pipeline{}, fmt.Errorf("%w: needs_recon with steps", ErrInvalid)
	}
	if !p.NeedsRecon && len(p.Steps) == 0 {
		return Pipeline{}, fmt.Errorf("%w: no steps and no needs_recon", ErrInvalid)
	}
	return p, nil
}

func sortSteps(steps []Step) {
	for i := 1; i < len(steps); i++ {
		for j := i; j > 0 && steps[j].Priority < steps[j-1].Priority; j-- {
			steps[j], steps[j-1] = steps[j-1], steps[j]
		}
	}
}

func decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: text after the JSON value", ErrInvalid)
	}
	return nil
}

func oneOf(field, v string) error {
	for _, l := range levels {
		if v == l {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %q is not one of %v", ErrInvalid, field, truncate(v), levels)
}

func required(field, v string) error {
	if v == "" {
		return fmt.Errorf("%w: %s must not be empty", ErrInvalid, field)
	}
	return nil
}

func length(field, v string, limit int) error {
	if len([]rune(v)) > limit {
		return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalid, field, limit)
	}
	return nil
}

// printable refuses control characters other than a newline: text from a model reaches a terminal.
func printable(field, v string) error {
	for _, r := range v {
		if unicode.IsControl(r) && r != '\n' {
			return fmt.Errorf("%w: %s has a control character", ErrInvalid, field)
		}
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func truncate(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "..."
	}
	return s
}
