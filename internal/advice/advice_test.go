package advice

import (
	"errors"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/schematest"
)

func TestSchemasMatchTheTypes(t *testing.T) {
	schematest.Check(t, Suggestion{}, SuggestionSchema())
	schematest.Check(t, Pipeline{}, PipelineSchema())
	schematest.Strict(t, SuggestionSchema())
	schematest.Strict(t, PipelineSchema())
}

func TestSchemasRequireEveryField(t *testing.T) {
	// a model that may leave a field out will; every field is required
	for name, schema := range map[string]string{"suggestion": string(SuggestionSchema()), "pipeline": string(PipelineSchema())} {
		for _, f := range []string{`"needs_recon"`, `"required"`} {
			if !strings.Contains(schema, f) {
				t.Errorf("%s: %s is missing", name, f)
			}
		}
	}
}

const goodSuggestion = `{"command":"gobuster dir -u http://10.0.0.5:8000 -w /usr/share/wordlists/dirb/common.txt","rationale":"port 8000 is a web server and no directory scan was run","expected":"a list of directories","risk":" Medium ","confidence":"HIGH","needs_recon":false}`

func TestParseSuggestion(t *testing.T) {
	s, err := ParseSuggestion([]byte(goodSuggestion))
	if err != nil || s.Risk != "medium" || s.Confidence != "high" || !strings.HasPrefix(s.Command, "gobuster") {
		t.Fatalf("%+v, %v", s, err)
	}
	thin, err := ParseSuggestion([]byte(`{"command":"","rationale":"no services are known; scan the target first","expected":"","risk":"low","confidence":"low","needs_recon":true}`))
	if err != nil || !thin.NeedsRecon || thin.Command != "" {
		t.Errorf("%+v, %v", thin, err)
	}
	bad := map[string]string{
		"unknown field":        `{"command":"x","rationale":"r","expected":"e","risk":"low","confidence":"low","needs_recon":false,"run":true}`,
		"text after":           goodSuggestion + ` {}`,
		"not json":             `use nmap`,
		"empty":                ``,
		"bad risk":             `{"command":"x","rationale":"r","expected":"e","risk":"extreme","confidence":"low","needs_recon":false}`,
		"bad confidence":       `{"command":"x","rationale":"r","expected":"e","risk":"low","confidence":"sure","needs_recon":false}`,
		"no command":           `{"command":"","rationale":"r","expected":"e","risk":"low","confidence":"low","needs_recon":false}`,
		"recon with a command": `{"command":"nmap x","rationale":"r","expected":"e","risk":"low","confidence":"low","needs_recon":true}`,
		"no rationale":         `{"command":"x","rationale":" ","expected":"e","risk":"low","confidence":"low","needs_recon":false}`,
		"a control character":  "{\"command\":\"x\",\"rationale\":\"a\\u001b[2Jb\",\"expected\":\"e\",\"risk\":\"low\",\"confidence\":\"low\",\"needs_recon\":false}",
		"a long rationale":     `{"command":"x","rationale":"` + strings.Repeat("r", 2001) + `","expected":"e","risk":"low","confidence":"low","needs_recon":false}`,
		"a long command":       `{"command":"` + strings.Repeat("x", 501) + `","rationale":"r","expected":"e","risk":"low","confidence":"low","needs_recon":false}`,
		"a wrong type":         `{"command":1,"rationale":"r","expected":"e","risk":"low","confidence":"low","needs_recon":false}`,
	}
	for name, doc := range bad {
		if _, err := ParseSuggestion([]byte(doc)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

const goodPipeline = `{"summary":" Web server on 8000, SSH on 22. ","needs_recon":false,"steps":[` +
	`{"priority":2,"service":"10.0.0.5:22/tcp","vector":"ssh","why":"OpenSSH is old","commands":["ssh-audit 10.0.0.5"],"confidence":"low"},` +
	`{"priority":1,"service":"10.0.0.5:8000/tcp http","vector":"web content discovery","why":"no directory scan yet","commands":["gobuster dir -u http://10.0.0.5:8000 -w w.txt"],"confidence":" High"}]}`

func TestParsePipeline(t *testing.T) {
	p, err := ParsePipeline([]byte(goodPipeline))
	if err != nil || p.Summary != "Web server on 8000, SSH on 22." || len(p.Steps) != 2 {
		t.Fatalf("%+v, %v", p, err)
	}
	if p.Steps[0].Priority != 1 || p.Steps[0].Vector != "web content discovery" || p.Steps[0].Confidence != "high" || p.Steps[1].Priority != 2 {
		t.Errorf("steps are not in priority order: %+v", p.Steps)
	}
	thin, err := ParsePipeline([]byte(`{"summary":"nothing is known","steps":[],"needs_recon":true}`))
	if err != nil || !thin.NeedsRecon {
		t.Errorf("%+v, %v", thin, err)
	}
	step := func(patch string) string {
		return `{"summary":"s","needs_recon":false,"steps":[{"priority":1,"service":"10.0.0.5","vector":"v","why":"w","commands":[],"confidence":"low"` + patch + `}]}`
	}
	var many strings.Builder
	for i := range 13 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"priority":1,"service":"s","vector":"v","why":"w","commands":[],"confidence":"low"}`)
	}
	bad := map[string]string{
		"unknown field":         `{"summary":"s","needs_recon":true,"steps":[],"run":1}`,
		"unknown step field":    step(`,"run":true`),
		"text after":            goodPipeline + `x`,
		"not json":              `1. scan`,
		"no steps":              `{"summary":"s","steps":[],"needs_recon":false}`,
		"recon with steps":      strings.Replace(goodPipeline, `"needs_recon":false`, `"needs_recon":true`, 1),
		"priority 0":            strings.Replace(step(``), `"priority":1`, `"priority":0`, 1),
		"priority 13":           strings.Replace(step(``), `"priority":1`, `"priority":13`, 1),
		"no service":            strings.Replace(step(``), `"service":"10.0.0.5"`, `"service":""`, 1),
		"no why":                strings.Replace(step(``), `"why":"w"`, `"why":" "`, 1),
		"no vector":             strings.Replace(step(``), `"vector":"v"`, `"vector":""`, 1),
		"bad confidence":        strings.Replace(step(``), `"confidence":"low"`, `"confidence":"certain"`, 1),
		"too many steps":        `{"summary":"s","needs_recon":false,"steps":[` + many.String() + `]}`,
		"too many commands":     strings.Replace(step(``), `"commands":[]`, `"commands":["a","b","c","d","e","f"]`, 1),
		"a control char in why": strings.Replace(step(``), `"why":"w"`, `"why":"a\u0007b"`, 1),
		"a long command":        strings.Replace(step(``), `"commands":[]`, `"commands":["`+strings.Repeat("x", 501)+`"]`, 1),
	}
	for name, doc := range bad {
		if _, err := ParsePipeline([]byte(doc)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(goodSuggestion))
	f.Add([]byte(goodPipeline))
	f.Add([]byte(`{"steps":[null]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if s, err := ParseSuggestion(data); err == nil && s.NeedsRecon == (s.Command != "") {
			t.Fatalf("an accepted suggestion is inconsistent: %+v", s)
		}
		if p, err := ParsePipeline(data); err == nil {
			for i := 1; i < len(p.Steps); i++ {
				if p.Steps[i].Priority < p.Steps[i-1].Priority {
					t.Fatalf("steps are not in order: %+v", p.Steps)
				}
			}
		}
	})
}
