package tui

import "context"

// Backend is where the panel gets what its keys show. The panel never reads a database or calls a
// model itself: whatever runs it provides these, and every call may take a while (a model is asked),
// so the panel makes them off its own loop.
type Backend interface {
	// Map returns the map of the case as plain text.
	Map(ctx context.Context) (string, error)
	// Pipeline returns the latest plan of the case as plain text, or, when fresh is set or there is
	// none yet, asks the model for a new one.
	Pipeline(ctx context.Context, fresh bool) (string, error)
	// Next asks the model for the next step. auto says the panel asks because a command has just
	// finished, so the answer should start with what that command showed; otherwise it is the person
	// who asks.
	Next(ctx context.Context, auto bool) (Suggestion, error)
	// Ask answers a free question from the tester, over the map, the journal and the recent commands.
	Ask(ctx context.Context, question string) (Suggestion, error)
}

// Suggestion is the answer to Next.
type Suggestion struct {
	Rationale  string
	Command    string
	Risk       string
	Confidence string
	// NeedsRecon says there was nothing to suggest yet: the Rationale says what is missing.
	NeedsRecon bool
}
