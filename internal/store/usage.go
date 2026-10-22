package store

import (
	"context"
	"slices"
)

var tiers = []string{"tier1", "tier2"}

// Usage is one call to a model.
type Usage struct {
	Tier             string // tier1 or tier2
	Model            string // as the provider names it, such as ollama/llama3.1:8b
	PromptTokens     int
	CompletionTokens int
	// OK says whether the call gave a usable answer; a refusal or an empty answer still cost tokens.
	OK bool
}

// UsageTotal is what one model has used in one tier.
type UsageTotal struct {
	Tier             string
	Model            string
	Calls            int
	Failed           int
	PromptTokens     int
	CompletionTokens int
}

// AddUsage records a call to a model.
func (r *Repo) AddUsage(ctx context.Context, u Usage) error {
	if !slices.Contains(tiers, u.Tier) {
		return invalid("tier %q (expected one of %v)", u.Tier, tiers)
	}
	if u.Model == "" || u.PromptTokens < 0 || u.CompletionTokens < 0 {
		return invalid("usage of model %q with %d and %d tokens", u.Model, u.PromptTokens, u.CompletionTokens)
	}
	ok := 0
	if u.OK {
		ok = 1
	}
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO model_usage (ts, tier, model, prompt_tokens, completion_tokens, ok) VALUES (?, ?, ?, ?, ?, ?)`,
		toMS(r.now()), u.Tier, u.Model, u.PromptTokens, u.CompletionTokens, ok)
	return err
}

// UsageTotals returns the use of each model in each tier, tier 1 first.
func (r *Repo) UsageTotals(ctx context.Context) ([]UsageTotal, error) {
	rows, err := r.q.QueryContext(ctx, `
		SELECT tier, model, COUNT(*), SUM(1 - ok), SUM(prompt_tokens), SUM(completion_tokens)
		FROM model_usage GROUP BY tier, model ORDER BY tier, model`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UsageTotal
	for rows.Next() {
		var t UsageTotal
		if err := rows.Scan(&t.Tier, &t.Model, &t.Calls, &t.Failed, &t.PromptTokens, &t.CompletionTokens); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
