package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sozureke/kagegami/pkg/llm"
)

var bg = context.Background()

// server answers /api/chat with the given handler and records the last request body.
func server(t *testing.T, h func(w http.ResponseWriter, body map[string]any)) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(data, &body)
		body["_path"], body["_method"], body["_ctype"] = r.URL.Path, r.Method, r.Header.Get("Content-Type")
		mu.Lock()
		last = body
		mu.Unlock()
		h(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func good(w http.ResponseWriter, _ map[string]any) {
	reply(w, map[string]any{
		"model": "llama3.1:8b", "message": map[string]string{"role": "assistant", "content": `{"hosts":[]}`},
		"done": true, "done_reason": "stop", "prompt_eval_count": 120, "eval_count": 8,
	})
}

func newProvider(t *testing.T, url string) *Provider {
	t.Helper()
	p, err := New(Config{Model: "llama3.1:8b", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompleteSendsTheRequestAndReadsTheAnswer(t *testing.T) {
	srv, last := server(t, good)
	p := newProvider(t, srv.URL+"/")
	temp := 0.0
	schema := json.RawMessage(`{"type":"object","properties":{"hosts":{"type":"array"}}}`)
	resp, err := p.Complete(bg, llm.Request{
		System:      "you extract",
		Messages:    []llm.Message{{Role: llm.User, Content: "output here"}},
		Schema:      schema,
		MaxTokens:   500,
		Temperature: &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"hosts":[]}` || resp.Usage.PromptTokens != 120 || resp.Usage.CompletionTokens != 8 || resp.Finish != llm.FinishStop || resp.Model != "llama3.1:8b" {
		t.Errorf("response = %+v", resp)
	}
	m := last()
	if m["_path"] != "/api/chat" || m["_method"] != "POST" || m["_ctype"] != "application/json" {
		t.Errorf("request line: %v %v %v", m["_method"], m["_path"], m["_ctype"])
	}
	if m["model"] != "llama3.1:8b" || m["stream"] != false || m["keep_alive"] != keepAlive {
		t.Errorf("fields = %v", m)
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "you extract" || msgs[1].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v", msgs)
	}
	var want any
	_ = json.Unmarshal(schema, &want)
	if !reflect.DeepEqual(m["format"], want) {
		t.Errorf("format = %v, want the schema itself", m["format"])
	}
	opts := m["options"].(map[string]any)
	if opts["num_ctx"] != float64(DefaultContext) || opts["num_predict"] != float64(500) || opts["temperature"] != float64(0) {
		t.Errorf("options = %v", opts)
	}
}

func TestOptionalPartsAreLeftOut(t *testing.T) {
	srv, last := server(t, good)
	if _, err := newProvider(t, srv.URL).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	m := last()
	if _, ok := m["format"]; ok {
		t.Error("a format was sent without a schema")
	}
	opts := m["options"].(map[string]any)
	if _, ok := opts["temperature"]; ok {
		t.Error("a temperature was sent although none was asked for")
	}
	if _, ok := opts["num_predict"]; ok {
		t.Error("a token limit was sent although none was asked for")
	}
	if msgs := m["messages"].([]any); len(msgs) != 1 {
		t.Errorf("an empty system prompt was sent: %v", msgs)
	}
}

func TestCompleteRefusesARequestWithoutMessages(t *testing.T) {
	srv, _ := server(t, good)
	if _, err := newProvider(t, srv.URL).Complete(bg, llm.Request{System: "s"}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("err = %v", err)
	}
}

func TestFinishReasons(t *testing.T) {
	for reason, want := range map[string]llm.FinishReason{"stop": llm.FinishStop, "length": llm.FinishLength, "load": llm.FinishOther, "": llm.FinishOther} {
		srv, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
			reply(w, map[string]any{"message": map[string]string{"content": "x"}, "done": true, "done_reason": reason})
		})
		resp, err := newProvider(t, srv.URL).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}})
		if err != nil || resp.Finish != want {
			t.Errorf("reason %q: %v, %v", reason, resp.Finish, err)
		}
	}
}

func TestErrorsAreTold(t *testing.T) {
	req := llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}}
	run := func(status int, body string) error {
		srv, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		})
		_, err := newProvider(t, srv.URL).Complete(bg, req)
		return err
	}

	t.Run("a model that is not pulled", func(t *testing.T) {
		var m *llm.ModelMissingError
		err := run(404, `{"error":"model 'llama3.1:8b' not found"}`)
		if !errors.As(err, &m) || m.Model != "llama3.1:8b" || m.Hint != "pull it with: ollama pull llama3.1:8b" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a 404 that is not about the model", func(t *testing.T) {
		var m *llm.ModelMissingError
		err := run(404, `page not found`)
		var u *llm.UnavailableError
		if errors.As(err, &m) || !errors.As(err, &u) || u.Temporary {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a server error is temporary", func(t *testing.T) {
		for _, status := range []int{500, 502, 503, 429} {
			if err := run(status, `{"error":"overloaded"}`); !llm.Retryable(err) || !strings.Contains(err.Error(), "overloaded") {
				t.Errorf("status %d: err = %v", status, err)
			}
		}
	})
	t.Run("a client error is not", func(t *testing.T) {
		if err := run(401, `{"error":"no"}`); err == nil || llm.Retryable(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a prompt that does not fit", func(t *testing.T) {
		if err := run(400, `{"error":"context length exceeded"}`); !errors.Is(err, llm.ErrContextTooLong) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("an error inside a 200", func(t *testing.T) {
		if err := run(200, `{"error":"the model crashed"}`); !llm.Retryable(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("an answer that is not JSON", func(t *testing.T) {
		if err := run(200, `<html>`); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("what the server says is cleaned and shortened", func(t *testing.T) {
		err := run(500, `{"error":"bad\u001b[31m thing\n`+strings.Repeat("x", 1000)+`"}`)
		text := err.Error()
		if strings.ContainsAny(text, "\x1b\n") || len(text) > 600 || !strings.Contains(text, "bad") {
			t.Errorf("err = %q", text)
		}
	})
	t.Run("an empty error body falls back to the status text", func(t *testing.T) {
		if err := run(503, ``); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestServerNotRunning(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // now nothing listens there
	_, err := newProvider(t, addr).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}})
	var u *llm.UnavailableError
	if !errors.As(err, &u) || u.Temporary || !strings.Contains(u.Hint, "ollama serve") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), addr) && strings.Contains(err.Error(), "http://") {
		t.Errorf("the error carries the full URL: %v", err)
	}
}

func TestTimeoutsAndCancellation(t *testing.T) {
	slow, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
		time.Sleep(300 * time.Millisecond)
		good(w, nil)
	})
	req := llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}}

	t.Run("a call without a deadline gets the default one", func(t *testing.T) {
		p, _ := New(Config{Model: "m", BaseURL: slow.URL, Timeout: 30 * time.Millisecond})
		_, err := p.Complete(bg, req)
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want the deadline", err)
		}
	})
	t.Run("a call with its own deadline keeps it", func(t *testing.T) {
		p, _ := New(Config{Model: "m", BaseURL: slow.URL, Timeout: time.Millisecond})
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		if _, err := p.Complete(ctx, req); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cancelling stops the call and says so", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		go func() { time.Sleep(30 * time.Millisecond); cancel() }()
		_, err := newProvider(t, slow.URL).Complete(ctx, req)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer elsewhere.Close()
	srv, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
		http.Redirect(w, &http.Request{}, elsewhere.URL, http.StatusTemporaryRedirect)
	})
	_, err := newProvider(t, srv.URL).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "the output of commands"}}})
	if err == nil || hits.Load() != 0 {
		t.Errorf("err %v, the other server was hit %d times: the prompt went where nobody chose", err, hits.Load())
	}
}

func TestAnAnswerThatIsTooLargeIsRefused(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxBody+10))
	})
	_, err := newProvider(t, srv.URL).Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
}

// Ollama cuts a prompt that does not fit its window without saying so; a prompt that filled the
// window is reported.
func TestATruncatedPromptIsReported(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
		reply(w, map[string]any{"message": map[string]string{"content": "partial"}, "done": true, "done_reason": "stop", "prompt_eval_count": 4096})
	})
	p, _ := New(Config{Model: "m", BaseURL: srv.URL, Context: 4096})
	resp, err := p.Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}})
	if !errors.Is(err, llm.ErrContextTooLong) {
		t.Errorf("err = %v", err)
	}
	if resp.Usage.PromptTokens != 4096 {
		t.Error("the cost of the call was lost")
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" || r.Method != http.MethodGet {
			http.Error(w, "no", 404)
			return
		}
		reply(w, map[string]any{"models": []map[string]string{{"name": "llama3.1:8b"}, {"name": "huihui_ai/qwen2.5-abliterate:14b"}}})
	}))
	defer srv.Close()
	got, err := newProvider(t, srv.URL).Models(bg)
	if err != nil || len(got) != 2 || got[0] != "llama3.1:8b" || got[1] != "huihui_ai/qwen2.5-abliterate:14b" {
		t.Errorf("%v, %v", got, err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	var u *llm.UnavailableError
	if _, err := newProvider(t, url).Models(bg); !errors.As(err, &u) {
		t.Errorf("err = %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "x") }))
	defer bad.Close()
	if _, err := newProvider(t, bad.URL).Models(bg); err == nil {
		t.Error("a list that is not JSON was accepted")
	}
}

func TestNewChecksItsInput(t *testing.T) {
	for name, c := range map[string]Config{
		"no model":        {BaseURL: DefaultBaseURL},
		"blank model":     {Model: "  "},
		"no scheme":       {Model: "m", BaseURL: "localhost:11434"},
		"another scheme":  {Model: "m", BaseURL: "ftp://localhost"},
		"no host":         {Model: "m", BaseURL: "http://"},
		"with a password": {Model: "m", BaseURL: "http://u:p@localhost:11434"},
		"not a URL":       {Model: "m", BaseURL: "http://%zz"},
	} {
		if _, err := New(c); !errors.Is(err, llm.ErrInvalidRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	p, err := New(Config{Model: "llama3.1:8b"})
	if err != nil || p.base.String() != DefaultBaseURL || p.context != DefaultContext || p.timeout != DefaultTimeout {
		t.Errorf("defaults: %+v, %v", p, err)
	}
	if p.Name() != "ollama/llama3.1:8b" {
		t.Errorf("name = %q", p.Name())
	}
	caps := p.Capabilities()
	if !caps.StrictSchema || caps.Tools || caps.Caching || caps.ContextTokens != DefaultContext {
		t.Errorf("capabilities = %+v", caps)
	}
}

func TestEndpointKeepsAPathPrefix(t *testing.T) {
	p, _ := New(Config{Model: "m", BaseURL: "https://host.example/ollama/"})
	if got := p.endpoint("/api/chat"); got != "https://host.example/ollama/api/chat" {
		t.Errorf("endpoint = %q", got)
	}
}

func TestRegister(t *testing.T) {
	r := llm.NewRegistry()
	if err := Register(r, Config{BaseURL: "http://127.0.0.1:1111", Context: 4096}); err != nil {
		t.Fatal(err)
	}
	p, err := r.New("ollama/huihui_ai/qwen2.5-abliterate:14b", llm.Options{})
	if err != nil || p.Name() != "ollama/huihui_ai/qwen2.5-abliterate:14b" || p.Capabilities().ContextTokens != 4096 {
		t.Fatalf("%v, %v", p, err)
	}
	if p.(*Provider).base.Host != "127.0.0.1:1111" {
		t.Error("the default address was not used")
	}
	p, _ = r.New("ollama/m", llm.Options{BaseURL: "http://192.168.64.1:11434"})
	if p.(*Provider).base.Host != "192.168.64.1:11434" {
		t.Error("the address of the options did not win")
	}
	if _, err := r.New("ollama/m", llm.Options{BaseURL: "nonsense"}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("err = %v", err)
	}
	if err := Register(r, Config{}); err == nil {
		t.Error("a second registration was accepted")
	}
}

func TestWorksWithTheDecorators(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ map[string]any) {
		reply(w, map[string]any{"message": map[string]string{"content": "  "}, "done": true, "prompt_eval_count": 5, "eval_count": 1})
	})
	b := llm.NewBudget(100)
	p := llm.WithBudget(llm.Checked(newProvider(t, srv.URL)), b)
	_, err := p.Complete(bg, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}})
	if !errors.Is(err, llm.ErrEmptyResponse) || b.Used().Total() != 6 {
		t.Errorf("err %v, used %+v: an empty answer is an error but its cost counts", err, b.Used())
	}
}
