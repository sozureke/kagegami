package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sozureke/kagegami/internal/agent"
	"github.com/sozureke/kagegami/pkg/llm"
)

// The variants of a run: with the worked example in the prompt, and without it.
const (
	VariantExample   = "example"
	VariantNoExample = "no-example"
)

// Status says how a run ended.
const (
	StatusOK       = "ok"
	StatusUnusable = "unusable" // the model did not give an answer that could be used
	StatusError    = "error"    // the model could not be reached, or the case could not be built
)

// Config configures Run.
type Config struct {
	Provider llm.Provider
	// Repeats is how many times each case of tier 2 is run; zero means 3. One run says little about
	// a model that does not answer the same way twice. Tier 1 runs at temperature 0 and is run once.
	Repeats int
	// Variants limits the runs to some of VariantExample and VariantNoExample; empty means both.
	Variants []string
	// Temperature overrides the sampling temperature of tier 2; nil leaves the model's default.
	Temperature *float64
	// Only runs the cases whose name starts with it.
	Only string
	// Progress, if set, is told what is being run.
	Progress func(string)
}

// Outcome is one run of one case.
type Outcome struct {
	Case    string
	Mode    string
	Variant string
	Run     int

	Status string
	Err    string
	// Answer is what the tester would see.
	Answer string
	// Attempts are all the answers the model gave.
	Attempts []string
	Checks   []CheckResult

	Tokens   int
	Duration time.Duration
	// ReadCalls are the read_output calls the model made, and RefusedReads those that were refused
	// because the model asked for output it had not been shown.
	ReadCalls    int
	RefusedReads int
	// Dropped is what the checks removed from a plan.
	Dropped []string
}

// Report is everything a run found.
type Report struct {
	Model       string
	When        time.Time
	Repeats     int
	Temperature *float64
	Outcomes    []Outcome
	// Checklists are the things for a person to look at, by case and mode.
	Checklists []Checklist
}

// Checklist is the list a person works through for the answers of one case.
type Checklist struct {
	Case  string
	Mode  string
	Items []string
}

const maxConsecutiveErrors = 3

// Run runs every case with every variant, one after the other.
func Run(ctx context.Context, cfg Config) (Report, error) {
	if cfg.Provider == nil {
		return Report{}, errors.New("no model to run")
	}
	repeats := cfg.Repeats
	if repeats <= 0 {
		repeats = 3
	}
	variants := cfg.Variants
	if len(variants) == 0 {
		variants = []string{VariantNoExample, VariantExample}
	}
	for _, v := range variants {
		if v != VariantExample && v != VariantNoExample {
			return Report{}, fmt.Errorf("unknown variant %q", v)
		}
	}
	progress := cfg.Progress
	if progress == nil {
		progress = func(string) {}
	}

	fixtures, tier1, err := Load()
	if err != nil {
		return Report{}, err
	}
	rep := Report{Model: cfg.Provider.Name(), When: time.Now().UTC(), Repeats: repeats, Temperature: cfg.Temperature}

	consecutive := 0
	record := func(o Outcome) error {
		rep.Outcomes = append(rep.Outcomes, o)
		progress(fmt.Sprintf("%-26s %-9s %-10s run %d: %s", o.Case, o.Mode, o.Variant, o.Run, o.Status))
		if o.Status == StatusError {
			consecutive++
			if consecutive >= maxConsecutiveErrors {
				return fmt.Errorf("stopped after %d errors in a row; the last: %s", consecutive, o.Err)
			}
		} else {
			consecutive = 0
		}
		return ctx.Err()
	}

	for _, f := range fixtures {
		if !strings.HasPrefix(f.Name, cfg.Only) {
			continue
		}
		for _, mode := range f.modes() {
			rep.Checklists = append(rep.Checklists, Checklist{Case: f.Name, Mode: mode, Items: f.Checklist})
			for _, variant := range variants {
				for run := 1; run <= repeats; run++ {
					if err := record(runTier2(ctx, cfg, f, mode, variant, run)); err != nil {
						return rep, err
					}
				}
			}
		}
	}
	for _, c := range tier1 {
		if !strings.HasPrefix("tier1/"+c.Name, cfg.Only) {
			continue
		}
		// Tier 1 asks for temperature 0 whatever is set here, so a second run would say the same:
		// what it needs for a firmer answer is more cases, not more runs.
		for _, variant := range variants {
			if err := record(runTier1(ctx, cfg, c, variant, 1)); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

// recorder passes calls to the provider and keeps what was sent and what came back.
type recorder struct {
	llm.Provider
	temperature *float64

	mu        sync.Mutex
	requests  []string
	responses []string
}

func (r *recorder) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if req.Temperature == nil {
		req.Temperature = r.temperature
	}
	var sent strings.Builder
	sent.WriteString(req.System)
	for _, m := range req.Messages {
		sent.WriteString("\n")
		sent.WriteString(m.Content)
	}
	resp, err := r.Provider.Complete(ctx, req)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, sent.String())
	if err == nil {
		r.responses = append(r.responses, resp.Text)
	}
	return resp, err
}

// refusedRead is the start of the message the model gets when it asks for output it was not shown.
const refusedRead = "was not shown to you"

// refusedReads counts the refusals in the last request: it repeats every earlier message of the
// exchange, so it holds all of them.
func (r *recorder) refusedReads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return 0
	}
	return strings.Count(r.requests[len(r.requests)-1], refusedRead)
}

func runTier2(ctx context.Context, cfg Config, f Fixture, mode, variant string, run int) Outcome {
	o := Outcome{Case: f.Name, Mode: mode, Variant: variant, Run: run}
	env, err := Build(ctx, f)
	if err != nil {
		o.Status, o.Err = StatusError, err.Error()
		return o
	}
	defer func() { _ = env.Close() }()

	rec := &recorder{Provider: cfg.Provider, temperature: cfg.Temperature}
	opts := agent.DefaultTier2Options()
	opts.NoFewShot = variant == VariantNoExample
	t := &agent.Tier2{Provider: rec, Store: env.Store, RawDir: env.RawDir, Options: opts}

	start := time.Now()
	var runErr error
	switch mode {
	case ModeNext:
		var res agent.NextResult
		res, runErr = t.Next(ctx, requestOr(f.Request, "Suggest the next reconnaissance step."))
		if runErr == nil {
			s := res.Suggestion
			o.Answer = strings.Join([]string{s.Command, s.Rationale, s.Expected}, "\n")
			o.Tokens, o.ReadCalls = res.Usage.Total(), res.ReadOutputCalls
		}
	case ModePipeline:
		var res agent.PlanResult
		res, runErr = t.Plan(ctx, requestOr(f.Request, "Lay out a pipeline for this target."))
		if runErr == nil {
			o.Answer = planText(res)
			o.Tokens, o.ReadCalls = res.Usage.Total(), res.ReadOutputCalls
			for _, d := range res.Dropped {
				o.Dropped = append(o.Dropped, d.String())
			}
		}
	default:
		runErr = fmt.Errorf("unknown mode %q", mode)
	}
	o.Duration = time.Since(start).Round(100 * time.Millisecond)
	o.finish(runErr, rec)
	o.Checks = evaluate(f.Checks, texts{answer: o.Answer, attempts: o.Attempts, requests: rec.requests})
	return o
}

func runTier1(ctx context.Context, cfg Config, c Tier1Case, variant string, run int) Outcome {
	o := Outcome{Case: "tier1/" + c.Name, Mode: ModeTier1, Variant: variant, Run: run}
	output, err := c.Text()
	if err != nil {
		o.Status, o.Err = StatusError, err.Error()
		return o
	}
	rec := &recorder{Provider: cfg.Provider}
	opts := agent.DefaultTier1Options()
	opts.FewShot = variant == VariantExample
	t := &agent.Tier1{Provider: llm.Checked(rec), Options: opts, InScope: scopeOf(c.Scope)}

	start := time.Now()
	res, runErr := t.Extract(ctx, c.Command, 0, output)
	o.Duration = time.Since(start).Round(100 * time.Millisecond)
	if runErr == nil {
		data, err := json.Marshal(res.Delta)
		if err != nil {
			runErr = err
		} else {
			o.Answer = string(data)
		}
		o.Tokens = res.Usage.Total()
		for _, d := range res.Dropped {
			o.Dropped = append(o.Dropped, fmt.Sprint(d))
		}
	}
	o.finish(runErr, rec)
	o.Checks = evaluate(c.Checks, texts{answer: o.Answer, attempts: o.Attempts, requests: rec.requests})
	return o
}

// finish sets the status and what the model said.
func (o *Outcome) finish(err error, rec *recorder) {
	o.Attempts = append([]string(nil), rec.responses...)
	o.RefusedReads = rec.refusedReads()
	switch {
	case err == nil:
		o.Status = StatusOK
	case errors.Is(err, agent.ErrUnusable), errors.Is(err, agent.ErrUnparseable):
		o.Status, o.Err = StatusUnusable, err.Error()
	default:
		o.Status, o.Err = StatusError, err.Error()
	}
}

func scopeOf(addresses []string) func(string) bool {
	set := map[string]bool{}
	for _, a := range addresses {
		set[a] = true
	}
	return func(ip string) bool { return set[ip] }
}

func requestOr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func planText(res agent.PlanResult) string {
	var b strings.Builder
	b.WriteString(res.Pipeline.Summary)
	for _, s := range res.Pipeline.Steps {
		fmt.Fprintf(&b, "\n%d. %s | %s | %s | %s", s.Priority, s.Service, s.Vector, s.Why, strings.Join(s.Commands, " ; "))
	}
	return b.String()
}
