package eval

import (
	"encoding/json"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
)

// object returns the JSON object in a model's text, which may carry words or a code fence around it.
func object(text string) []byte {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil
	}
	return []byte(text[start : end+1])
}

// tier2Claims returns the commands in an answer of tier 2, a Suggestion or a Pipeline, or in
// anything shaped like one. Text that is not an answer (a read_output call, prose) has none.
func tier2Claims(text string) string {
	var a struct {
		Command string `json:"command"`
		Steps   []struct {
			Commands []string `json:"commands"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(object(text), &a); err != nil {
		return ""
	}
	lines := []string{a.Command}
	for _, s := range a.Steps {
		lines = append(lines, s.Commands...)
	}
	return strings.Join(lines, "\n")
}

// tier1Claims returns what a delta says is on the map, as text: its hosts, services and paths. The
// notes and findings are words, which can quote the output of the target, and are left out.
func tier1Claims(d recon.StateDelta) string {
	d.Notes, d.Findings = nil, nil
	data, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(data)
}

// tier1AttemptClaims is tier1Claims for the text of a model's answer.
func tier1AttemptClaims(text string) string {
	var d recon.StateDelta
	if err := json.Unmarshal(object(text), &d); err != nil {
		return ""
	}
	return tier1Claims(d)
}
