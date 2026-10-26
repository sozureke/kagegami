package cli

import (
	"context"
	"strings"

	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/scope"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/tui"
)

// Onboard takes the first message of a case that has no scope as the task: the addresses in it become
// the target and the scope, and the message itself the goal. The scope is the line the tool will not
// cross, so it is read from what the tester typed by the rules of the scope package and never by a
// model, and the tester is told exactly what was set. Once a scope exists a message is just a question.
func (b *panelBackend) Onboard(ctx context.Context, message string) (tui.Setup, error) {
	if e, err := engagement.Get(ctx, b.st); err == nil && !engagement.Scope(e).IsEmpty() {
		return tui.Setup{}, nil
	}
	found := addressesIn(message)
	if len(found) == 0 {
		return tui.Setup{Handled: true, Summary: noTargetYet}, nil
	}
	goal := strings.TrimSpace(message)
	res, err := engagement.Update(ctx, b.st, b.name, engagement.Changes{Target: &found[0], Goal: &goal, Scope: &found})
	if err != nil {
		return tui.Setup{}, err
	}
	return tui.Setup{Handled: true, Started: true, Summary: "task taken. " + describeEngagement(res.Engagement)}, nil
}

// noTargetYet is what the tester hears when they write to a case that has no target: the model stays off
// until there is one.
const noTargetYet = "I do not know what to look at yet. Give me the target: an address, a network or a range, like 10.10.10.5 or 10.10.10.0/24 (or describe the task with the address in it)."

// Configure applies /target, /goal and /scope. With nothing to change it says what is set.
func (b *panelBackend) Configure(ctx context.Context, s tui.Settings) (tui.Setup, error) {
	had := false
	if e, err := engagement.Get(ctx, b.st); err == nil {
		had = !engagement.Scope(e).IsEmpty()
	}
	changes := engagement.Changes{Target: s.Target, Goal: s.Goal, Scope: s.Scope, AddScope: s.AddScope}
	var e store.Engagement
	if changes.Any() {
		res, err := engagement.Update(ctx, b.st, b.name, changes)
		if err != nil {
			return tui.Setup{}, err
		}
		e = res.Engagement
	} else if got, err := engagement.Get(ctx, b.st); err == nil {
		e = got
	}
	return tui.Setup{
		Summary: describeEngagement(e),
		Started: !had && changes.Any() && !engagement.Scope(e).IsEmpty(),
	}, nil
}

// addressesIn finds the addresses, networks and ranges in a piece of text: the words that are one, as
// the scope package reads them. Names are not found, because a scope is made of addresses.
func addressesIn(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == ',' || r == ';' || r == '\n' || r == '\t' }) {
		word = strings.Trim(word, "()[]<>\"'`.:")
		if word == "" {
			continue
		}
		sc, err := scope.Parse([]string{word})
		if err != nil || sc.IsEmpty() {
			continue
		}
		if entry := sc.String(); !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out
}

// describeEngagement says in one line what a case is about.
func describeEngagement(e store.Engagement) string {
	sc := engagement.Scope(e)
	var parts []string
	if e.Target != "" {
		parts = append(parts, "target "+e.Target)
	}
	if !sc.IsEmpty() {
		parts = append(parts, "scope "+sc.String())
	}
	if e.Goal != "" {
		parts = append(parts, "goal: "+oneLineCmd(e.Goal))
	}
	if len(parts) == 0 {
		return "nothing is set yet: /target <address> to begin"
	}
	return strings.Join(parts, " · ")
}
