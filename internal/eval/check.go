package eval

import "strings"

// CheckResult is the outcome of one Check on one run.
type CheckResult struct {
	ID   string
	Text string
	Pass bool
}

// texts are what a run produced, for the checks to look at.
type texts struct {
	answer   string
	attempts []string
	requests []string
}

func (t texts) in(where string) string {
	switch where {
	case InAttempts:
		return strings.Join(t.attempts, "\n")
	case InRequests:
		return strings.Join(t.requests, "\n")
	}
	return t.answer
}

// evaluate runs the checks. A run with no answer has an empty answer, so a check that needs a word
// in it fails and one that needs a word to be absent passes: nothing was said.
func evaluate(checks []Check, t texts) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		text := strings.ToLower(t.in(c.In))
		pass := true
		if len(c.Any) > 0 {
			pass = false
			for _, w := range c.Any {
				if strings.Contains(text, strings.ToLower(w)) {
					pass = true
					break
				}
			}
		}
		for _, w := range c.None {
			if strings.Contains(text, strings.ToLower(w)) {
				pass = false
			}
		}
		out = append(out, CheckResult{ID: c.ID, Text: c.Text, Pass: pass})
	}
	return out
}
