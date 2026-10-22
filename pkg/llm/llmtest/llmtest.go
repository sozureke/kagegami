// Package llmtest has providers for tests: they answer from a script or from recorded answers,
// and need no model.
package llmtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/sozureke/kagegami/pkg/llm"
)

// Result is what a fake provider returns for one call.
type Result struct {
	Response llm.Response
	Err      error
}

// Fake answers from a list, in order, and keeps the requests it was given.
type Fake struct {
	ModelName string
	Caps      llm.Capabilities

	mu       sync.Mutex
	results  []Result
	requests []llm.Request
}

// NewFake returns a fake that returns the results one by one. When they run out it returns an error.
func NewFake(results ...Result) *Fake { return &Fake{ModelName: "fake/model", results: results} }

// Text is a shortcut for a successful answer.
func Text(s string) Result {
	return Result{Response: llm.Response{Text: s, Finish: llm.FinishStop, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5}}}
}

// Name implements llm.Provider.
func (f *Fake) Name() string { return f.ModelName }

// Capabilities implements llm.Provider.
func (f *Fake) Capabilities() llm.Capabilities { return f.Caps }

// Requests returns the requests received so far.
func (f *Fake) Requests() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.Request(nil), f.requests...)
}

// Complete implements llm.Provider.
func (f *Fake) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if len(f.results) == 0 {
		return llm.Response{}, errors.New("llmtest: the fake has no more answers")
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r.Response, r.Err
}

// Key is the key under which a recorded answer to a request is kept: a hash of the request.
func Key(req llm.Request) string {
	data, _ := json.Marshal(req)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Replay answers requests with recorded answers, found by Key. A request that was not recorded
// is an error that says so, which makes a change in a prompt visible in a test.
type Replay struct {
	ModelName string
	Caps      llm.Capabilities
	Answers   map[string]llm.Response
}

// LoadReplay reads recorded answers from a JSON file (a map from key to response).
func LoadReplay(path string) (*Replay, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a test fixture
	if err != nil {
		return nil, err
	}
	r := &Replay{ModelName: "replay/model"}
	if err := json.Unmarshal(data, &r.Answers); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return r, nil
}

// Name implements llm.Provider.
func (r *Replay) Name() string { return r.ModelName }

// Capabilities implements llm.Provider.
func (r *Replay) Capabilities() llm.Capabilities { return r.Caps }

// Complete implements llm.Provider.
func (r *Replay) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	resp, ok := r.Answers[Key(req)]
	if !ok {
		return llm.Response{}, fmt.Errorf("llmtest: no recorded answer for the request %s", Key(req))
	}
	return resp, nil
}
