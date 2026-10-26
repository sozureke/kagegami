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
	// Onboard is called first with every message the tester types. While the case has no scope it
	// takes the message as the task: it sets the target and the scope from the addresses in it and
	// says so (Handled). Once there is a scope it does nothing (not Handled) and the message is a
	// question.
	Onboard(ctx context.Context, message string) (Setup, error)
	// Configure changes what the case is about (/target, /goal, /scope). With nothing to change it
	// only reports what is set now.
	Configure(ctx context.Context, s Settings) (Setup, error)
}

// Settings is a change of what the case is about. A nil field is left as it is.
type Settings struct {
	Target *string
	Goal   *string
	// Scope replaces the scope; AddScope adds to it.
	Scope    *[]string
	AddScope []string
}

// Setup is what became of a change of the engagement: a line for the stream, and whether the case
// has just got its first scope, which is when the assistant should say what to do first.
type Setup struct {
	Summary string
	// Handled says the message was the task and has been dealt with, so it is not a question.
	Handled bool
	// Started says the case had no scope and has one now.
	Started bool
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
