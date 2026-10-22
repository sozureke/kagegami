package llm

import (
	"errors"
	"fmt"
)

var (
	// ErrEmptyResponse is returned when a provider answers successfully with no text. It is an
	// error, not an empty success: a model that says nothing has not answered.
	ErrEmptyResponse = errors.New("the model returned an empty answer")
	// ErrContextTooLong is returned when the request does not fit the model's context window.
	ErrContextTooLong = errors.New("the request does not fit the model's context window")
	// ErrBudgetExceeded is returned when the token ceiling of a session is reached.
	ErrBudgetExceeded = errors.New("the token budget is used up")
	// ErrInvalidRequest is returned for a request that cannot be sent.
	ErrInvalidRequest = errors.New("invalid request")
)

// RefusedError means the model declined to answer: a refusal stop reason, a content filter,
// a safety block. Callers show it to the person and do not retry (ADR-0010).
type RefusedError struct {
	Provider string
	// Reason is what the provider said, if anything.
	Reason string
}

func (e *RefusedError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("%s refused to answer", e.Provider)
	}
	return fmt.Sprintf("%s refused to answer: %s", e.Provider, e.Reason)
}

// UnavailableError means the provider could not be reached or failed on its side: the service
// is not running, the network failed, a server error. Temporary says whether trying again
// can help.
type UnavailableError struct {
	Provider  string
	Err       error
	Temporary bool
	// Hint tells the person what to do, if there is something to do.
	Hint string
}

func (e *UnavailableError) Error() string {
	msg := fmt.Sprintf("%s is not available: %v", e.Provider, e.Err)
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// ModelMissingError means the provider does not have the model.
type ModelMissingError struct {
	Provider string
	Model    string
	// Hint is the command that fixes it, such as "ollama pull llama3.1:8b".
	Hint string
}

func (e *ModelMissingError) Error() string {
	msg := fmt.Sprintf("%s has no model %q", e.Provider, e.Model)
	if e.Hint != "" {
		msg += ": " + e.Hint
	}
	return msg
}

// Retryable reports whether an error is worth trying again: a temporary unavailability and
// nothing else. A refusal, an empty answer, a missing model and a cancelled context are not.
func Retryable(err error) bool {
	var u *UnavailableError
	return errors.As(err, &u) && u.Temporary
}
