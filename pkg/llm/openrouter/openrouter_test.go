package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/pkg/llm"
)

const testKey = "sk-or-v1-0123456789abcdef-NOT-A-REAL-KEY"

var bg = context.Background()

// server answers with what the test sets, and keeps what it was sent.
type server struct {
	*httptest.Server
	status int
	reply  string
	path   string
	header http.Header
	body   map[string]any
}

func newServer(t *testing.T, status int, reply string) *server {
	t.Helper()
	s := &server{status: status, reply: reply}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.path, s.header = r.URL.Path, r.Header.Clone()
		data, _ := io.ReadAll(r.Body)
		s.body = nil
		_ = json.Unmarshal(data, &s.body)
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.reply)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) provider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(Config{Model: "vendor/model-x", APIKey: testKey, BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const okReply = `{"model":"vendor/model-x-2026","choices":[{"message":{"content":"{\"a\":1}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":30}}`

func TestNewChecksTheConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"fine", Config{Model: "a/b", APIKey: "k"}, ""},
		{"a local test server", Config{Model: "a/b", APIKey: "k", BaseURL: "http://127.0.0.1:9999"}, ""},
		{"localhost", Config{Model: "a/b", APIKey: "k", BaseURL: "http://localhost:9999/v1"}, ""},
		{"no model", Config{APIKey: "k"}, "no model"},
		{"no key", Config{Model: "a/b"}, KeyEnv + " is not set"},
		{"a blank key", Config{Model: "a/b", APIKey: "  "}, KeyEnv + " is not set"},
		{"plain http to the internet", Config{Model: "a/b", APIKey: "k", BaseURL: "http://openrouter.example.com/api/v1"}, "is not an https URL"},
		{"a password in the URL", Config{Model: "a/b", APIKey: "k", BaseURL: "https://u:p@example.com"}, "is not an https URL"},
		{"another scheme", Config{Model: "a/b", APIKey: "k", BaseURL: "ftp://example.com"}, "is not an https URL"},
		{"no host", Config{Model: "a/b", APIKey: "k", BaseURL: "https://"}, "is not an https URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr) || !errors.Is(err, llm.ErrInvalidRequest)) {
				t.Fatalf("err = %v, want one with %q", err, tt.wantErr)
			}
		})
	}
}

func TestTheRequestIsAnOpenAIStyleChatCompletion(t *testing.T) {
	s := newServer(t, 200, okReply)
	p := s.provider(t)
	temp := 0.2
	resp, err := p.Complete(bg, llm.Request{
		System:      "You advise.",
		Messages:    []llm.Message{{Role: llm.User, Content: "q1"}, {Role: llm.Assistant, Content: "a1"}, {Role: llm.User, Content: "q2"}},
		Schema:      json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"}}}`),
		MaxTokens:   500,
		Temperature: &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.path != "/chat/completions" {
		t.Errorf("path = %q", s.path)
	}
	if got := s.header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}
	if s.header.Get("Content-Type") != "application/json" || !strings.Contains(s.header.Get("X-Title"), "kagegami") {
		t.Errorf("headers = %v", s.header)
	}
	if s.body["model"] != "vendor/model-x" || s.body["max_tokens"] != float64(500) || s.body["temperature"] != 0.2 {
		t.Errorf("body = %v", s.body)
	}
	msgs := s.body["messages"].([]any)
	roles := ""
	for _, m := range msgs {
		roles += m.(map[string]any)["role"].(string) + " "
	}
	if roles != "system user assistant user " {
		t.Errorf("roles = %q", roles)
	}
	system := msgs[0].(map[string]any)["content"].(string)
	if !strings.HasPrefix(system, "You advise.") || !strings.Contains(system, `"type":"object"`) || !strings.Contains(system, "follows this JSON Schema") {
		t.Errorf("the schema must be written in the system prompt too:\n%s", system)
	}
	rf := s.body["response_format"].(map[string]any)
	spec := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || spec["name"] != "answer" || spec["strict"] != false {
		t.Errorf("response_format = %v: the schema is asked for, and not forced (a model may not support it)", rf)
	}

	if resp.Text != `{"a":1}` || resp.Model != "vendor/model-x-2026" || resp.Finish != llm.FinishStop || resp.Usage.Total() != 150 {
		t.Errorf("response = %+v", resp)
	}
}

func TestWithoutASchemaOrATemperatureNeitherIsSent(t *testing.T) {
	s := newServer(t, 200, okReply)
	if _, err := s.provider(t).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"response_format", "temperature", "max_tokens"} {
		if _, ok := s.body[k]; ok {
			t.Errorf("%q was sent without being asked for", k)
		}
	}
	if msgs := s.body["messages"].([]any); len(msgs) != 1 {
		t.Errorf("with no system prompt and no schema there is no system message: %v", msgs)
	}
}

func TestFinishReasons(t *testing.T) {
	for reason, want := range map[string]llm.FinishReason{"stop": llm.FinishStop, "length": llm.FinishLength, "tool_calls": llm.FinishOther, "": llm.FinishOther} {
		s := newServer(t, 200, fmt.Sprintf(`{"choices":[{"message":{"content":"x"},"finish_reason":%q}]}`, reason))
		resp, err := s.provider(t).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
		if err != nil || resp.Finish != want {
			t.Errorf("finish_reason %q: %v, %v", reason, resp.Finish, err)
		}
	}
}

func TestARefusalIsARefusalNotAnAnswer(t *testing.T) {
	for name, reply := range map[string]string{
		"a refusal":      `{"choices":[{"message":{"content":"","refusal":"I cannot help with that"},"finish_reason":"stop"}]}`,
		"content filter": `{"choices":[{"message":{"content":"x"},"finish_reason":"content_filter"}]}`,
	} {
		s := newServer(t, 200, reply)
		_, err := s.provider(t).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
		var refused *llm.RefusedError
		if !errors.As(err, &refused) || refused.Provider != "openrouter/vendor/model-x" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestErrorsOfTheServerAreOfTheRightKind(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		temporary bool
		as        func(error) bool
		contains  string
	}{
		{"unauthorized", 401, `{"error":{"code":401,"message":"No auth credentials found"}}`, false, isUnavailable, "OPENROUTER_API_KEY"},
		{"forbidden", 403, `{"error":{"message":"nope"}}`, false, isUnavailable, "valid key"},
		{"no credits", 402, `{"error":{"message":"Insufficient credits"}}`, false, isUnavailable, "out of credits"},
		{"rate limit", 429, `{"error":{"message":"slow down"}}`, true, isUnavailable, "slow down"},
		{"server error", 502, `bad gateway`, true, isUnavailable, "HTTP 502"},
		{"another client error", 422, `{"error":{"message":"bad parameter"}}`, false, isUnavailable, "bad parameter"},
		{"model missing", 404, `{"error":{"message":"No endpoints found"}}`, false, func(e error) bool { var m *llm.ModelMissingError; return errors.As(e, &m) }, "openrouter.ai/models"},
		{"context too long", 400, `{"error":{"message":"maximum context length is 8192 tokens"}}`, false, func(e error) bool { return errors.Is(e, llm.ErrContextTooLong) }, "context"},
		{"failure inside a 200", 200, `{"error":{"code":429,"message":"upstream is busy"}}`, true, isUnavailable, "upstream is busy"},
		{"failure inside a 200 with no code", 200, `{"error":{"message":"odd"}}`, true, isUnavailable, "odd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(t, tt.status, tt.body)
			_, err := s.provider(t).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
			if err == nil || !tt.as(err) || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("err = %v", err)
			}
			var u *llm.UnavailableError
			if errors.As(err, &u) && u.Temporary != tt.temporary {
				t.Errorf("Temporary = %v, want %v", u.Temporary, tt.temporary)
			}
		})
	}
}

func isUnavailable(err error) bool { var u *llm.UnavailableError; return errors.As(err, &u) }

func TestAnEmptyOrBrokenAnswerIsAnError(t *testing.T) {
	for name, reply := range map[string]string{"no choices": `{"choices":[]}`, "not json": `<html>`} {
		s := newServer(t, 200, reply)
		if _, err := s.provider(t).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := newServer(t, 200, okReply).provider(t).Complete(bg, llm.Request{}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("no messages: %v", err)
	}
}

// The key must not come out of the program by any of the ways a value gets printed or logged, nor
// in a message of the server that quotes it back.
func TestTheKeyIsNeverPrintedLoggedOrQuotedBack(t *testing.T) {
	s := newServer(t, 401, `{"error":{"message":"Incorrect API key provided: `+testKey+`"}}`)
	p := s.provider(t)
	_, err := p.Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
	if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "•••") {
		t.Fatalf("an error that quotes the key back must have it removed: %v", err)
	}

	cfg := Config{Model: "a/b", APIKey: testKey}
	for _, v := range []any{p, *p, cfg, &cfg} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if got := fmt.Sprintf(format, v); strings.Contains(got, testKey) {
				t.Errorf("fmt.Sprintf(%q) of %T leaks the key: %s", format, v, got)
			}
		}
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("made", "provider", p, "config", cfg, "key", p.key)
	if strings.Contains(buf.String(), testKey) {
		t.Errorf("slog leaks the key: %s", buf.String())
	}
	if b, _ := json.Marshal(struct{ K apiKey }{p.key}); strings.Contains(string(b), testKey) {
		t.Errorf("JSON leaks the key: %s", b)
	}
}

func TestARedirectIsNotFollowedSoTheKeyGoesNowhereElse(t *testing.T) {
	var elsewhere bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere = true }))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	p, err := New(Config{Model: "a/b", APIKey: testKey, BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}); err == nil {
		t.Error("a redirect is an error, not an answer")
	}
	if elsewhere {
		t.Error("the redirect was followed")
	}
}

func TestANetworkFailureIsTemporaryAndACancelledContextIsNot(t *testing.T) {
	s := newServer(t, 200, okReply)
	p := s.provider(t)
	s.Close()
	_, err := p.Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
	var u *llm.UnavailableError
	if !errors.As(err, &u) || !u.Temporary || strings.Contains(err.Error(), "http://") {
		t.Errorf("err = %v: a refused connection can be tried again, and the address is not in the message", err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := p.Complete(ctx, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestRegisterReadsTheKeyFromTheEnvironmentWhenAModelIsMade(t *testing.T) {
	reg := llm.NewRegistry()
	env := map[string]string{}
	if err := Register(reg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.New("openrouter/anthropic/claude-x", llm.Options{}); err == nil || !strings.Contains(err.Error(), KeyEnv) {
		t.Fatalf("without a key the error says which variable to set: %v", err)
	}
	env[KeyEnv] = testKey
	p, err := reg.New("openrouter/anthropic/claude-x", llm.Options{BaseURL: "https://evil.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openrouter/anthropic/claude-x" {
		t.Errorf("name = %q: the vendor is part of the model", p.Name())
	}
	if op := p.(*Provider); op.base.String() != DefaultBaseURL {
		t.Errorf("base = %s: the key goes only to the fixed API, whatever the options say", op.base)
	}
}
