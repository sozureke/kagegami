// Package llm defines the Provider interface and the pieces around it: errors, a registry
// of backends, and decorators for the policies that do not belong to any one model (limits on
// tokens, retries, an empty-answer check). It knows nothing about Kagegami.
package llm

import (
	"context"
	"encoding/json"
)

// Role says who wrote a message.
type Role string

// The roles of a conversation. The system prompt is separate (Request.System).
const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role    Role
	Content string
}

// Request is one call to a model.
type Request struct {
	// System is the system prompt.
	System   string
	Messages []Message
	// Schema is a JSON Schema the answer must follow. Nil asks for free text. A provider that
	// cannot enforce a schema still asks for it in the prompt; the caller validates the answer.
	Schema json.RawMessage
	// MaxTokens limits the answer. Zero means the provider's default.
	MaxTokens int
	// Temperature is the sampling temperature. Nil means the provider's default.
	Temperature *float64
}

// Usage is what a call cost, as the provider counted it.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Total is the number of tokens of the call.
func (u Usage) Total() int { return u.PromptTokens + u.CompletionTokens }

// Add returns the sum of two usages.
func (u Usage) Add(o Usage) Usage {
	return Usage{PromptTokens: u.PromptTokens + o.PromptTokens, CompletionTokens: u.CompletionTokens + o.CompletionTokens}
}

// FinishReason says why the model stopped.
type FinishReason string

// The reasons that matter to a caller.
const (
	// FinishStop: the model finished its answer.
	FinishStop FinishReason = "stop"
	// FinishLength: the answer was cut by the token limit. A structured answer cut this way
	// is not complete.
	FinishLength FinishReason = "length"
	// FinishOther: any other reason the provider gave.
	FinishOther FinishReason = "other"
)

// Response is the answer of a model.
type Response struct {
	Text   string
	Usage  Usage
	Finish FinishReason
	// Model is the model that answered, as the provider names it.
	Model string
}

// Capabilities tell a caller what a model can do, so that what it lacks can be made up for.
type Capabilities struct {
	// StrictSchema: the provider enforces Request.Schema itself.
	StrictSchema bool
	// Tools: the model can call tools.
	Tools bool
	// Caching: the provider caches a stable prompt prefix.
	Caching bool
	// ContextTokens is the size of the context window; zero means unknown.
	ContextTokens int
}

// Provider is a model that answers requests.
type Provider interface {
	// Name identifies the provider and model, as in "ollama/llama3.1:8b".
	Name() string
	Capabilities() Capabilities
	// Complete sends the request and returns the answer. It must stop when ctx is done. A
	// refusal is reported as *RefusedError, never as a normal answer.
	Complete(ctx context.Context, req Request) (Response, error)
}
