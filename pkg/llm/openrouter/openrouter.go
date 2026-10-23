// Package openrouter is a provider for OpenRouter (https://openrouter.ai), which serves many models
// behind one OpenAI-style API. It exists so that Kagegami can be developed and tested against models
// that are better than the ones that fit on a laptop (docs/adr/0012-openrouter-for-development.md).
//
// Whatever the model reads leaves the machine, and OpenRouter passes it on to the company that runs
// the model. The key comes from the environment and nowhere else.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/sozureke/kagegami/pkg/llm"
)

const (
	// DefaultBaseURL is the API of OpenRouter. It is fixed on purpose: a key must not be sent to
	// an address that a configuration file happens to name.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// KeyEnv is the environment variable that holds the key.
	KeyEnv = "OPENROUTER_API_KEY"
	// DefaultTimeout limits one call that has no deadline of its own.
	DefaultTimeout = 3 * time.Minute

	maxBody      = 8 << 20
	maxErrorText = 300
	appTitle     = "kagegami (development)"
)

// apiKey holds the key and prints as dots everywhere: fmt, JSON, text and slog.
type apiKey struct{ v string }

func (apiKey) String() string               { return "•••" }
func (apiKey) GoString() string             { return "•••" }
func (apiKey) MarshalText() ([]byte, error) { return []byte("•••"), nil }
func (apiKey) LogValue() slog.Value         { return slog.StringValue("•••") }

// Config configures a Provider.
type Config struct {
	// Model is the name OpenRouter knows, such as "anthropic/claude-sonnet-4.5".
	Model string
	// APIKey is the key. Required.
	APIKey string
	// BaseURL is the API; empty means DefaultBaseURL. Anything else must be https, or http to this
	// machine (a test server).
	BaseURL string
	// Timeout limits a call whose context has no deadline; zero means DefaultTimeout.
	Timeout time.Duration
	// HTTPClient makes the requests; nil means a client that does not follow redirects.
	HTTPClient *http.Client
}

// String, GoString and LogValue keep the key out of anything that prints a Config.
func (c Config) String() string       { return "openrouter.Config{" + c.Model + ", key •••}" }
func (c Config) GoString() string     { return c.String() }
func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Provider talks to the chat completions endpoint of OpenRouter.
type Provider struct {
	model   string
	key     apiKey
	base    *url.URL
	timeout time.Duration
	client  *http.Client
}

// New checks the configuration and returns the provider. It does not contact the server.
func New(c Config) (*Provider, error) {
	if strings.TrimSpace(c.Model) == "" {
		return nil, fmt.Errorf("%w: no model name", llm.ErrInvalidRequest)
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, fmt.Errorf("%w: %s is not set: export it in your shell; it never goes in the configuration file or in the code", llm.ErrInvalidRequest, KeyEnv)
	}
	raw := c.BaseURL
	if raw == "" {
		raw = DefaultBaseURL
	}
	base, err := url.Parse(raw)
	if err != nil || base.Hostname() == "" || base.User != nil || !safeScheme(base) {
		return nil, fmt.Errorf("%w: %q is not an https URL (or http to this machine) without a password", llm.ErrInvalidRequest, raw)
	}
	p := &Provider{model: c.Model, key: apiKey{c.APIKey}, base: base, timeout: c.Timeout, client: c.HTTPClient}
	if p.timeout <= 0 {
		p.timeout = DefaultTimeout
	}
	if p.client == nil {
		// A redirect would send the key and the prompt to a place that nobody chose.
		p.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return p, nil
}

func safeScheme(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Register adds the provider to a registry under the name "openrouter". The key is read from getenv
// when a model is made, so a missing key is said then, with what to do about it.
func Register(r *llm.Registry, getenv func(string) string) error {
	return r.Register("openrouter", func(model string, _ llm.Options) (llm.Provider, error) {
		// llm.Options.BaseURL is for the providers that run on a configurable address; the key of
		// this one goes only to its own, fixed API.
		return New(Config{Model: model, APIKey: getenv(KeyEnv)})
	})
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return "openrouter/" + p.model }

// String, GoString and LogValue keep the key out of anything that prints a Provider: fmt shows the
// fields of a struct through reflection, and the methods of a field are not used there.
//
// They are on the value, not on the pointer, so that a copy of a Provider is as safe as the Provider.
func (p Provider) String() string       { return "openrouter/" + p.model }
func (p Provider) GoString() string     { return p.String() }
func (p Provider) LogValue() slog.Value { return slog.StringValue(p.String()) }

// redact removes the key from text that came from the server, in case it was quoted back.
func (p *Provider) redact(s string) string {
	if p.key.v == "" {
		return s
	}
	return strings.ReplaceAll(s, p.key.v, "•••")
}

// Capabilities implements llm.Provider. The schema is sent as a request, and a model may or may not
// keep to it, so the answer is validated by the caller either way.
func (p *Provider) Capabilities() llm.Capabilities { return llm.Capabilities{} }

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *jsonSchemaSpec `json:"json_schema,omitempty"`
}

type jsonSchemaSpec struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
}

// Complete implements llm.Provider.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if len(req.Messages) == 0 {
		return llm.Response{}, fmt.Errorf("%w: no messages", llm.ErrInvalidRequest)
	}
	body := chatRequest{Model: p.model, MaxTokens: req.MaxTokens, Temperature: req.Temperature}
	system := req.System
	if len(req.Schema) > 0 {
		// Asked for in the request, and also written in the prompt: a model that ignores the first
		// still reads the second.
		body.ResponseFormat = &responseFormat{Type: "json_schema", JSONSchema: &jsonSchemaSpec{Name: "answer", Schema: req.Schema}}
		system += "\n\nAnswer with one JSON object that follows this JSON Schema, and nothing else:\n" + string(req.Schema)
	}
	if system != "" {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: system})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("%w: %w", llm.ErrInvalidRequest, err)
	}

	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	u := *p.base
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, fmt.Errorf("%w: %w", llm.ErrInvalidRequest, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.key.v)
	httpReq.Header.Set("X-Title", appTitle)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return llm.Response{}, p.transportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return llm.Response{}, p.transportError(ctx, err)
	}
	if len(data) > maxBody {
		return llm.Response{}, fmt.Errorf("%s: the answer is larger than %d bytes", p.Name(), maxBody)
	}
	if resp.StatusCode != http.StatusOK {
		return llm.Response{}, p.statusError(resp.StatusCode, data)
	}

	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return llm.Response{}, fmt.Errorf("%s: the answer is not valid JSON: %w", p.Name(), err)
	}
	if out.Error != nil { // OpenRouter sometimes answers 200 and puts the failure in the body
		return llm.Response{}, p.statusError(statusOf(out.Error), data)
	}
	if len(out.Choices) == 0 {
		return llm.Response{}, fmt.Errorf("%s: the answer has no choices", p.Name())
	}
	c := out.Choices[0]
	if c.Message.Refusal != "" {
		return llm.Response{}, &llm.RefusedError{Provider: p.Name(), Reason: p.redact(sanitize(c.Message.Refusal))}
	}
	if c.FinishReason == "content_filter" {
		return llm.Response{}, &llm.RefusedError{Provider: p.Name(), Reason: "content filter"}
	}
	return llm.Response{
		Text:   c.Message.Content,
		Model:  out.Model,
		Usage:  llm.Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens},
		Finish: finish(c.FinishReason),
	}, nil
}

func finish(reason string) llm.FinishReason {
	switch reason {
	case "stop":
		return llm.FinishStop
	case "length":
		return llm.FinishLength
	}
	return llm.FinishOther
}

// statusOf reads the HTTP status OpenRouter puts in the code of an error in the body.
func statusOf(e *apiError) int {
	var n int
	if err := json.Unmarshal(e.Code, &n); err == nil && n >= 400 && n < 600 {
		return n
	}
	return http.StatusInternalServerError
}

// transportError explains a failure to talk to the server.
func (p *Provider) transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	temporary := true // the network failing is worth another try
	hint := "check the network"
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		hint = "the server did not answer in time"
	}
	return &llm.UnavailableError{Provider: p.Name(), Err: cleanError(err), Temporary: temporary, Hint: hint}
}

// statusError turns an error answer of the server into an error of the right kind. The key is
// never part of what it says.
func (p *Provider) statusError(status int, data []byte) error {
	var body struct {
		Error apiError `json:"error"`
	}
	_ = json.Unmarshal(data, &body)
	text := p.redact(sanitize(body.Error.Message))
	if text == "" {
		text = http.StatusText(status)
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &llm.UnavailableError{Provider: p.Name(), Err: fmt.Errorf("HTTP %d: %s", status, text), Hint: "is " + KeyEnv + " a valid key?"}
	case status == http.StatusPaymentRequired:
		return &llm.UnavailableError{Provider: p.Name(), Err: fmt.Errorf("HTTP %d: %s", status, text), Hint: "the account is out of credits: see openrouter.ai/credits"}
	case status == http.StatusNotFound:
		return &llm.ModelMissingError{Provider: "openrouter", Model: p.model, Hint: "choose one from openrouter.ai/models"}
	case status == http.StatusBadRequest && strings.Contains(strings.ToLower(text), "context"):
		return fmt.Errorf("%s: %w: %s", p.Name(), llm.ErrContextTooLong, text)
	case status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout:
		return &llm.UnavailableError{Provider: p.Name(), Err: fmt.Errorf("HTTP %d: %s", status, text), Temporary: true}
	}
	return &llm.UnavailableError{Provider: p.Name(), Err: fmt.Errorf("HTTP %d: %s", status, text)}
}

// cleanError keeps the cause of a network error but not the URL it may carry.
func cleanError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// sanitize shortens text from a server and removes control characters: it ends up in messages
// shown to a person.
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxErrorText {
		s = string(r[:maxErrorText]) + "..."
	}
	return s
}
