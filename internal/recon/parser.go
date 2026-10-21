package recon

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sozureke/kagegami/internal/terminal"
)

// Command is a finished command as the parsers see it.
type Command struct {
	// Line is the command line as the user typed it.
	Line     string
	ExitCode int
}

// Parser turns the output of one kind of tool into a state delta.
//
// The output is the text a person saw (see terminal.Cleaner) and is untrusted: it comes from
// the target. A parser must not panic or run for long on any input, and should skip lines it
// does not understand instead of failing, because the output may hold more than the tool's
// own report (a pipe, several commands, a banner).
type Parser interface {
	// Name identifies the parser in logs and in the source of what it found.
	Name() string
	// Match says whether the parser understands the output of this command.
	Match(cmd Command) bool
	// Parse reads the output. A partial result with an error is allowed and used.
	Parse(ctx context.Context, cmd Command, output string) (StateDelta, error)
}

// Registry holds the known parsers.
type Registry struct {
	parsers []Parser
}

// NewRegistry returns a registry of the given parsers, in order.
func NewRegistry(parsers ...Parser) *Registry {
	return &Registry{parsers: slices.Clone(parsers)}
}

// Programs returns the programs a command line runs, which is what a parser's Match usually
// looks at: "sudo /usr/bin/nmap -sV x | tee out" runs sudo's nmap and tee.
func Programs(line string) []string { return terminal.CommandNames(line) }

// Runs reports whether the command line runs the program, by base name.
func Runs(line, program string) bool { return slices.Contains(Programs(line), program) }

// Match returns the parsers that understand the command, in registry order.
func (r *Registry) Match(cmd Command) []Parser {
	var found []Parser
	for _, p := range r.parsers {
		if p.Match(cmd) {
			found = append(found, p)
		}
	}
	return found
}

// Result is what Run found.
type Result struct {
	// Delta is the validated sum of what the parsers found.
	Delta StateDelta
	// Parsers names the parsers that ran, in order.
	Parsers []string
	// Err joins the failures of parsers that failed or returned an invalid delta. Parsers
	// that succeeded still count.
	Err error
}

// Run applies every parser that matches the command to the output and merges the results. A
// parser that fails, panics or returns an invalid delta does not stop the others.
func (r *Registry) Run(ctx context.Context, cmd Command, output string) Result {
	var res Result
	for _, p := range r.Match(cmd) {
		res.Parsers = append(res.Parsers, p.Name())
		delta, err := safeParse(ctx, p, cmd, output)
		if err != nil {
			res.Err = errors.Join(res.Err, fmt.Errorf("%s: %w", p.Name(), err))
		}
		checked, verr := delta.Validate()
		if verr != nil {
			res.Err = errors.Join(res.Err, fmt.Errorf("%s: %w", p.Name(), verr))
			continue // nothing of an invalid delta is used
		}
		res.Delta = res.Delta.Merge(checked)
	}
	return res
}

func safeParse(ctx context.Context, p Parser, cmd Command, output string) (d StateDelta, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = StateDelta{}, fmt.Errorf("parser panicked: %v", r)
		}
	}()
	return p.Parse(ctx, cmd, output)
}
