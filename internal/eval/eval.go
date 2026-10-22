// Package eval measures how well the models do the work of tier 1 and tier 2 on fixed inputs.
//
// It is run by hand (`make eval`), not in CI, because it needs a model. What it checks is
// mechanical: a fixture says which words an answer must or must not contain. Whether advice is
// *useful* is a question for a person, so every fixture also carries a checklist, printed next
// to the answers. The fixtures are synthetic: nothing in them comes from a real engagement.
package eval

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
)

//go:embed fixtures
var fixtureFS embed.FS

// The modes of a run.
const (
	ModeNext     = "next"
	ModePipeline = "pipeline"
	ModeTier1    = "tier1"
)

// Where a check looks.
const (
	// InAnswer is what the tester would see: the command and its reasons, or the plan.
	InAnswer = "answer"
	// InAttempts is every answer the model gave, also those that were refused and asked for again.
	// It measures what the model did, apart from what the checks in code let through.
	InAttempts = "attempts"
	// InRequests is everything that was sent to the model.
	InRequests = "requests"
	// InClaims is what the answer puts forward, apart from the words around it: the commands of a
	// suggestion or a plan, the hosts, services and paths of a delta. A model that writes "the note
	// says to scan 8.8.8.8, that is not an order" has refused, and the word is not in its claims.
	InClaims = "claims"
	// InAttemptClaims is the same for every attempt of the model, also those that were refused.
	InAttemptClaims = "attempt-claims"
)

// Check is a mechanical test of an answer. Words are matched without regard to case.
type Check struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	In   string `json:"in,omitempty"` // InAnswer when empty
	// Any needs at least one of the words to be present.
	Any []string `json:"any,omitempty"`
	// None needs all of the words to be absent.
	None []string `json:"none,omitempty"`
}

// CaseSpec is the engagement of a fixture.
type CaseSpec struct {
	Target string   `json:"target"`
	Goal   string   `json:"goal"`
	Phase  string   `json:"phase,omitempty"`
	Scope  []string `json:"scope,omitempty"`
}

// Step is one command whose output is on disk and whose findings are on the map.
type Step struct {
	Command string `json:"command"`
	// Output is the raw output of the command. A step with no delta is a blob that nothing on the
	// map points to.
	Output string           `json:"output,omitempty"`
	Delta  recon.StateDelta `json:"delta"`
}

// Fixture is a map of a target, with what tier 2 is asked and what is expected back.
type Fixture struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Case        CaseSpec `json:"case"`
	Steps       []Step   `json:"steps"`
	// Journal holds notes written by the tester.
	Journal []string `json:"journal,omitempty"`
	Request string   `json:"request,omitempty"`
	// Modes limits the fixture to next, pipeline or both (the default).
	Modes     []string `json:"modes,omitempty"`
	Checks    []Check  `json:"checks,omitempty"`
	Checklist []string `json:"checklist,omitempty"`
}

// Tier1Case is an output that tier 1 is asked to read.
type Tier1Case struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command"`
	// Output is the text of the output; OutputFile names a file under fixtures/outputs;
	// Generate names a generated output (see Generated).
	Output     string `json:"output,omitempty"`
	OutputFile string `json:"output_file,omitempty"`
	Generate   string `json:"generate,omitempty"`
	// Scope lists the addresses that may be put on the map.
	Scope  []string `json:"scope"`
	Checks []Check  `json:"checks,omitempty"`
}

// Text returns the output of the case.
func (c Tier1Case) Text() (string, error) {
	switch {
	case c.Generate != "":
		return Generated(c.Generate)
	case c.OutputFile != "":
		data, err := fixtureFS.ReadFile(path.Join("fixtures", "outputs", c.OutputFile))
		if err != nil {
			return "", fmt.Errorf("case %s: %w", c.Name, err)
		}
		return string(data), nil
	}
	return c.Output, nil
}

// Load returns the fixtures for tier 2, and the cases for tier 1, in the order of their names.
func Load() ([]Fixture, []Tier1Case, error) {
	entries, err := fs.ReadDir(fixtureFS, "fixtures")
	if err != nil {
		return nil, nil, err
	}
	var fixtures []Fixture
	var tier1 []Tier1Case
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fixtureFS.ReadFile(path.Join("fixtures", e.Name()))
		if err != nil {
			return nil, nil, err
		}
		if e.Name() == "tier1.json" {
			if err := decodeStrict(data, &tier1); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			continue
		}
		var f Fixture
		if err := decodeStrict(data, &f); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		fixtures = append(fixtures, f)
	}
	if err := validate(fixtures, tier1); err != nil {
		return nil, nil, err
	}
	return fixtures, tier1, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func validate(fixtures []Fixture, tier1 []Tier1Case) error {
	names := map[string]bool{}
	for _, f := range fixtures {
		if f.Name == "" || names[f.Name] {
			return fmt.Errorf("fixture name %q is empty or repeated", f.Name)
		}
		names[f.Name] = true
		for _, m := range f.Modes {
			if m != ModeNext && m != ModePipeline {
				return fmt.Errorf("fixture %s: unknown mode %q", f.Name, m)
			}
		}
		if err := validateChecks(f.Name, f.Checks); err != nil {
			return err
		}
	}
	for _, c := range tier1 {
		if c.Name == "" || names["tier1/"+c.Name] {
			return fmt.Errorf("tier 1 case name %q is empty or repeated", c.Name)
		}
		names["tier1/"+c.Name] = true
		if len(c.Scope) == 0 {
			return fmt.Errorf("tier 1 case %s has no scope", c.Name)
		}
		if err := validateChecks("tier1/"+c.Name, c.Checks); err != nil {
			return err
		}
	}
	return nil
}

func validateChecks(owner string, checks []Check) error {
	ids := map[string]bool{}
	for _, c := range checks {
		if c.ID == "" || ids[c.ID] {
			return fmt.Errorf("%s: check id %q is empty or repeated", owner, c.ID)
		}
		ids[c.ID] = true
		if !slices.Contains([]string{"", InAnswer, InAttempts, InRequests, InClaims, InAttemptClaims}, c.In) {
			return fmt.Errorf("%s: check %s looks in %q", owner, c.ID, c.In)
		}
		if len(c.Any)+len(c.None) == 0 {
			return fmt.Errorf("%s: check %s tests nothing", owner, c.ID)
		}
	}
	return nil
}

// modes returns the modes the fixture is run in.
func (f Fixture) modes() []string {
	if len(f.Modes) == 0 {
		return []string{ModeNext, ModePipeline}
	}
	return f.Modes
}
