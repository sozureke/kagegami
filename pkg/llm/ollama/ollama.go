// Package ollama is the llm.Provider for a local Ollama server (https://ollama.com).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/sozureke/kagegami/pkg/llm"
)

const (
	// DefaultBaseURL is where Ollama listens unless told otherwise.
	DefaultBaseURL = "http://localhost:11434"
	// DefaultContext is the context window Kagegami asks for.
	DefaultContext = 8192
	// DefaultTimeout limits one call that has no deadline of its own.
	DefaultTimeout = 5 * time.Minute

	maxBody      = 8 << 20
	maxErrorText = 300
	// keepAlive keeps the model in memory between the calls of a session.
	keepAlive = "10m"
)

// Config configures a Provider.
type Config struct {
	// Model is the name Ollama knows, such as "llama3.1:8b".
	Model string
	// BaseURL is the address of the server; empty means DefaultBaseURL.
	BaseURL string
	// Context is the size of the context window to ask for; zero means DefaultContext.
	Context int
	// Timeout limits a call whose context has no deadline; zero means DefaultTimeout.
	Timeout time.Duration
	// HTTPClient makes the requests; nil means a client that does not follow redirects.
	HTTPClient *http.Client
}

// Provider talks to Ollama's /api/chat.
type Provider struct {
	model   string
	base    *url.URL
	context int
	timeout time.Duration
	client  *http.Client
}

// New checks the configuration and returns the provider. It does not contact the server.
func New(c Config) (*Provider, error) {
	if strings.TrimSpace(c.Model) == "" {
		return nil, fmt.Errorf("%w: no model name", llm.ErrInvalidRequest)
	}
	raw := c.BaseURL
	if raw == "" {
		raw = DefaultBaseURL
	}
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil {
		return nil, fmt.Errorf("%w: %q is not an http or https URL without a password", llm.ErrInvalidRequest, raw)
	}
	p := &Provider{model: c.Model, base: base, context: c.Context, timeout: c.Timeout, client: c.HTTPClient}
	if p.context <= 0 {
		p.context = DefaultContext
	}
	if p.timeout <= 0 {
		p.timeout = DefaultTimeout
	}
	if p.client == nil {
		// A redirect would send the prompt, which holds the output of commands, to a place
		// that nobody chose.
		p.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return p, nil
}

// Register adds the provider to a registry under the name "ollama". Options.BaseURL overrides
// defaults.BaseURL when it is set.
func Register(r *llm.Registry, defaults Config) error {
	return r.Register("ollama", func(model string, opts llm.Options) (llm.Provider, error) {
		c := defaults
		c.Model = model
		if opts.BaseURL != "" {
			c.BaseURL = opts.BaseURL
		}
		return New(c)
	})
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return "ollama/" + p.model }

// Capabilities implements llm.Provider.
func (p *Provider) Capabilities() llm.Capabilities {
	return llm.Capabilities{StrictSchema: true, ContextTokens: p.context}
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []chatMessage   `json:"messages"`
	Stream    bool            `json:"stream"`
	Format    json.RawMessage `json:"format,omitempty"`
	Options   chatOptions     `json:"options"`
	KeepAlive string          `json:"keep_alive"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	NumCtx      int      `json:"num_ctx"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type chatResponse struct {
	Model           string      `json:"model"`
	Message         chatMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
	Error           string      `json:"error"`
}

// Complete implements llm.Provider.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if len(req.Messages) == 0 {
		return llm.Response{}, fmt.Errorf("%w: no messages", llm.ErrInvalidRequest)
	}
	body := chatRequest{
		Model:     p.model,
		Stream:    false,
		Format:    req.Schema,
		KeepAlive: keepAlive,
		Options:   chatOptions{NumCtx: p.context, NumPredict: req.MaxTokens, Temperature: req.Temperature},
	}
	if req.System != "" {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: req.System})
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/api/chat"), bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, fmt.Errorf("%w: %w", llm.ErrInvalidRequest, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
	if out.Error != "" {
		return llm.Response{}, p.statusError(http.StatusInternalServerError, data)
	}
	result := llm.Response{
		Text:   out.Message.Content,
		Model:  out.Model,
		Usage:  llm.Usage{PromptTokens: out.PromptEvalCount, CompletionTokens: out.EvalCount},
		Finish: finish(out.DoneReason),
	}
	// Ollama cuts a prompt that does not fit the window and answers the rest. A prompt that
	// filled the window is, most likely, such a prompt.
	if out.PromptEvalCount >= p.context {
		return result, fmt.Errorf("%s: %w (%d tokens counted, window %d)", p.Name(), llm.ErrContextTooLong, out.PromptEvalCount, p.context)
	}
	return result, nil
}

// Models lists the models the server has, as it names them ("llama3.1:8b").
func (p *Provider) Models(ctx context.Context) ([]string, error) {
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint("/api/tags"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, p.transportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, p.transportError(ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, p.statusError(resp.StatusCode, data)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil, fmt.Errorf("%s: the list of models is not valid JSON: %w", p.Name(), err)
	}
	var names []string
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

func (p *Provider) endpoint(path string) string {
	u := *p.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
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

// transportError explains a failure to talk to the server.
func (p *Provider) transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	hint := "is Ollama running at " + p.base.Host + "? start it with: ollama serve"
	temporary := false
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		hint, temporary = "the server did not answer in time", true
	}
	return &llm.UnavailableError{Provider: p.Name(), Err: cleanError(err), Temporary: temporary, Hint: hint}
}

// statusError turns an error answer of the server into an error of the right kind.
func (p *Provider) statusError(status int, data []byte) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &body)
	text := sanitize(body.Error)
	if text == "" {
		text = http.StatusText(status)
	}
	switch {
	case status == http.StatusNotFound && strings.Contains(strings.ToLower(text), "model"):
		return &llm.ModelMissingError{Provider: "ollama", Model: p.model, Hint: "pull it with: ollama pull " + p.model}
	case status == http.StatusBadRequest && strings.Contains(strings.ToLower(text), "context"):
		return fmt.Errorf("%s: %w: %s", p.Name(), llm.ErrContextTooLong, text)
	case status >= 500 || status == http.StatusTooManyRequests:
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
