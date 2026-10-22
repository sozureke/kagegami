package eval

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// group is the runs of one case in one mode and variant.
type group struct {
	Case, Mode, Variant string
	Runs                []Outcome
}

func (r Report) groups() []group {
	var out []group
	index := map[string]int{}
	for _, o := range r.Outcomes {
		key := o.Case + "\x00" + o.Mode + "\x00" + o.Variant
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, group{Case: o.Case, Mode: o.Mode, Variant: o.Variant})
		}
		out[i].Runs = append(out[i].Runs, o)
	}
	return out
}

func (g group) count(status string) int {
	n := 0
	for _, o := range g.Runs {
		if o.Status == status {
			n++
		}
	}
	return n
}

// checkTotals returns how many check results passed, and how many there were.
func (g group) checkTotals() (pass, total int) {
	for _, o := range g.Runs {
		for _, c := range o.Checks {
			total++
			if c.Pass {
				pass++
			}
		}
	}
	return pass, total
}

func (g group) averages() (tokens int, took time.Duration, refused int) {
	var tok int
	var dur time.Duration
	n := 0
	for _, o := range g.Runs {
		refused += o.RefusedReads
		if o.Status != StatusOK {
			continue
		}
		n++
		tok += o.Tokens
		dur += o.Duration
	}
	if n == 0 {
		return 0, 0, refused
	}
	return tok / n, (dur / time.Duration(n)).Round(100 * time.Millisecond), refused
}

// WriteMarkdown writes the report as a document for docs/eval.
func (r Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval: %s\n\n", r.Model)
	temp := "the model's default"
	if r.Temperature != nil {
		temp = fmt.Sprint(*r.Temperature)
	}
	fmt.Fprintf(&b, "- date: %s\n- runs of each case: %d\n- temperature: %s\n\n", r.When.Format("2006-01-02 15:04 UTC"), r.Repeats, temp)
	b.WriteString("What the checks say is mechanical: a word that has to be there, or must not be. Whether the advice is *good* is for the checklists at the end, and for reading the answers.\n\n")

	groups := r.groups()
	b.WriteString("## Summary\n\n")
	b.WriteString("| case | mode | prompt | usable | checks passed | refused reads | tokens (mean) | time (mean) |\n|---|---|---|---|---|---|---|---|\n")
	for _, g := range groups {
		pass, total := g.checkTotals()
		tok, took, refused := g.averages()
		fmt.Fprintf(&b, "| %s | %s | %s | %d/%d | %d/%d | %d | %d | %s |\n",
			g.Case, g.Mode, g.Variant, g.count(StatusOK), len(g.Runs), pass, total, refused, tok, took)
	}

	b.WriteString("\n## Refusals\n\nA run is *unusable* when the model never gave an answer that passed the checks in code, after every attempt. A run with an *error* did not reach the model, or the case could not be built.\n\n")
	b.WriteString("| mode | prompt | runs | usable | unusable | errors |\n|---|---|---|---|---|---|\n")
	for _, mode := range []string{ModeNext, ModePipeline, ModeTier1} {
		for _, variant := range []string{VariantNoExample, VariantExample} {
			var runs, ok, bad, errs int
			for _, g := range groups {
				if g.Mode != mode || g.Variant != variant {
					continue
				}
				runs += len(g.Runs)
				ok += g.count(StatusOK)
				bad += g.count(StatusUnusable)
				errs += g.count(StatusError)
			}
			if runs > 0 {
				fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d |\n", mode, variant, runs, ok, bad, errs)
			}
		}
	}

	if failed := r.failedChecks(); len(failed) > 0 {
		b.WriteString("\n## Checks that failed\n\n")
		for _, line := range failed {
			b.WriteString("- " + line + "\n")
		}
	}

	b.WriteString("\n## Answers\n\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "### %s, %s, %s\n\n", g.Case, g.Mode, g.Variant)
		for _, o := range g.Runs {
			fmt.Fprintf(&b, "**run %d: %s**", o.Run, o.Status)
			if o.ReadCalls > 0 || o.RefusedReads > 0 {
				fmt.Fprintf(&b, " (read_output: %d, refused: %d)", o.ReadCalls, o.RefusedReads)
			}
			b.WriteString("\n\n")
			if o.Err != "" {
				fmt.Fprintf(&b, "> %s\n\n", oneLine(o.Err, 300))
			}
			if o.Answer != "" {
				b.WriteString("```text\n" + clip(o.Answer, 1500) + "\n```\n\n")
			}
			// What the model said is the only way to tell a refusal that was deserved from one that was not.
			if o.Status == StatusUnusable {
				for i, a := range o.Attempts {
					fmt.Fprintf(&b, "attempt %d of the model:\n\n```text\n%s\n```\n\n", i+1, clip(a, 1000))
				}
			}
			for _, d := range o.Dropped {
				fmt.Fprintf(&b, "- left out: %s\n", oneLine(d, 200))
			}
			if len(o.Dropped) > 0 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("## Checklists\n\nFor a person, for each case and mode above.\n\n")
	for _, c := range r.Checklists {
		if len(c.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s, %s\n\n", c.Case, c.Mode)
		for _, it := range c.Items {
			b.WriteString("- [ ] " + it + "\n")
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// failedChecks lists the checks that did not pass in every run, with how often they did.
func (r Report) failedChecks() []string {
	var out []string
	for _, g := range r.groups() {
		passes := map[string]int{}
		var order []string
		text := map[string]string{}
		for _, o := range g.Runs {
			for _, c := range o.Checks {
				if _, seen := text[c.ID]; !seen {
					order = append(order, c.ID)
					text[c.ID] = c.Text
				}
				if c.Pass {
					passes[c.ID]++
				}
			}
		}
		for _, id := range order {
			if passes[id] < len(g.Runs) {
				out = append(out, fmt.Sprintf("%s, %s, %s: %q passed %d of %d", g.Case, g.Mode, g.Variant, text[id], passes[id], len(g.Runs)))
			}
		}
	}
	return out
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	return clip(s, limit)
}

func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && s[cut]&0xC0 == 0x80 { // do not cut a character in two
		cut--
	}
	return s[:cut] + "..."
}
