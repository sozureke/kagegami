package llm_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sozureke/kagegami/pkg/llm"
	"github.com/sozureke/kagegami/pkg/llm/llmtest"
)

var bg = context.Background()

func TestParseSpec(t *testing.T) {
	good := map[string]llm.Spec{
		"ollama/llama3.1:8b":                      {Provider: "ollama", Model: "llama3.1:8b"},
		"ollama/huihui_ai/qwen2.5-abliterate:14b": {Provider: "ollama", Model: "huihui_ai/qwen2.5-abliterate:14b"},
		"anthropic/claude-x":                      {Provider: "anthropic", Model: "claude-x"},
		"openai-compat/gpt-4o":                    {Provider: "openai-compat", Model: "gpt-4o"},
	}
	for in, want := range good {
		got, err := llm.ParseSpec(in)
		if err != nil || got != want || got.String() != in {
			t.Errorf("ParseSpec(%q) = %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "ollama", "ollama/", "/model", "Ollama/x", "1ollama/x", "ol lama/x", "ollama/a b", "ollama/a\tb", "ollama/a\nb", "ollama/\x00", "ollama/a\x7f"} {
		if _, err := llm.ParseSpec(in); !errors.Is(err, llm.ErrInvalidRequest) {
			t.Errorf("ParseSpec(%q) error = %v, want ErrInvalidRequest", in, err)
		}
	}
}

func TestRegistry(t *testing.T) {
	r := llm.NewRegistry()
	var got struct {
		model string
		opts  llm.Options
	}
	mk := func(model string, opts llm.Options) (llm.Provider, error) {
		got.model, got.opts = model, opts
		return llmtest.NewFake(), nil
	}
	if err := r.Register("ollama", mk); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("anthropic", mk); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("ollama", mk); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("a second registration: %v", err)
	}
	for _, name := range []string{"", "Bad", "x y"} {
		if err := r.Register(name, mk); err == nil {
			t.Errorf("Register(%q) succeeded", name)
		}
	}
	if err := r.Register("nilfactory", nil); err == nil {
		t.Error("a nil factory was accepted")
	}

	if _, err := r.New("ollama/llama3.1:8b", llm.Options{BaseURL: "http://x:1"}); err != nil || got.model != "llama3.1:8b" || got.opts.BaseURL != "http://x:1" {
		t.Errorf("New: %v, %+v", err, got)
	}
	_, err := r.New("mistral/x", llm.Options{})
	if !errors.Is(err, llm.ErrInvalidRequest) || !strings.Contains(err.Error(), "anthropic, ollama") {
		t.Errorf("an unknown provider: %v", err)
	}
	if _, err := r.New("garbage", llm.Options{}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("a bad spec: %v", err)
	}
	boom := errors.New("boom")
	_ = r.Register("failing", func(string, llm.Options) (llm.Provider, error) { return nil, boom })
	if _, err := r.New("failing/x", llm.Options{}); !errors.Is(err, boom) {
		t.Errorf("a factory error was lost: %v", err)
	}
}

func resolver(m map[string][]string, err error) llm.Resolver {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		if err != nil {
			return nil, err
		}
		var out []netip.Addr
		for _, a := range m[host] {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestIsLocal(t *testing.T) {
	names := resolver(map[string][]string{
		"localhost":     {"127.0.0.1", "::1"},
		"mac.local":     {"192.168.64.1"},
		"mixed.example": {"127.0.0.1", "10.0.0.2"},
		"empty.example": {},
		"loop.example":  {"127.0.0.2"},
	}, nil)
	tests := map[string]bool{
		"http://127.0.0.1:11434":          true,
		"http://127.1.2.3:11434":          true,
		"http://[::1]:11434":              true,
		"http://[::ffff:127.0.0.1]:11434": true,
		"https://localhost":               true,
		"http://localhost:11434/api":      true,
		"http://loop.example":             true,
		"http://192.168.64.1:11434":       false,
		"http://10.0.0.5":                 false,
		"http://0.0.0.0:11434":            false,
		"http://[::]:11434":               false,
		"http://8.8.8.8":                  false,
		"http://mac.local:11434":          false,
		"http://mixed.example":            false,
		"http://empty.example":            false,
		"http://unknown.example":          false,
		"https://api.example.com":         false,
	}
	for u, want := range tests {
		got, err := llm.IsLocal(bg, u, names)
		if err != nil || got != want {
			t.Errorf("IsLocal(%q) = %v, %v; want %v", u, got, err, want)
		}
	}
	t.Run("a failing resolver means remote", func(t *testing.T) {
		got, err := llm.IsLocal(bg, "http://localhost:11434", resolver(nil, errors.New("no dns")))
		if err != nil || got {
			t.Errorf("IsLocal = %v, %v", got, err)
		}
	})
	t.Run("a literal address needs no resolver", func(t *testing.T) {
		called := false
		r := func(context.Context, string) ([]netip.Addr, error) { called = true; return nil, nil }
		if got, _ := llm.IsLocal(bg, "http://127.0.0.1", r); !got || called {
			t.Errorf("local %v, resolver called %v", got, called)
		}
	})
	for _, u := range []string{"", "localhost:11434", "ftp://localhost", "http://", "file:///x", "://x", "http://%zz"} {
		if _, err := llm.IsLocal(bg, u, names); !errors.Is(err, llm.ErrInvalidRequest) {
			t.Errorf("IsLocal(%q) error = %v, want ErrInvalidRequest", u, err)
		}
	}
	t.Run("the system resolver works for localhost", func(t *testing.T) {
		if got, err := llm.IsLocal(bg, "http://localhost:11434", nil); err != nil || !got {
			t.Skipf("this machine's resolver does not map localhost to loopback: %v %v", got, err)
		}
	})
}

func TestUsage(t *testing.T) {
	u := llm.Usage{PromptTokens: 3, CompletionTokens: 4}.Add(llm.Usage{PromptTokens: 10, CompletionTokens: 20})
	if u.Total() != 37 || u.PromptTokens != 13 || u.CompletionTokens != 24 {
		t.Errorf("usage = %+v", u)
	}
}

func TestErrors(t *testing.T) {
	refused := &llm.RefusedError{Provider: "anthropic/x", Reason: "policy"}
	if !strings.Contains(refused.Error(), "refused") || !strings.Contains(refused.Error(), "policy") {
		t.Errorf("%v", refused)
	}
	if strings.Contains((&llm.RefusedError{Provider: "p"}).Error(), ":") {
		t.Error("an empty reason left a dangling colon")
	}
	missing := &llm.ModelMissingError{Provider: "ollama", Model: "m", Hint: "ollama pull m"}
	if !strings.Contains(missing.Error(), "ollama pull m") || !strings.Contains((&llm.ModelMissingError{Provider: "p", Model: "m"}).Error(), `"m"`) {
		t.Errorf("%v", missing)
	}
	cause := errors.New("connection refused")
	un := &llm.UnavailableError{Provider: "ollama", Err: cause, Temporary: true, Hint: "start it with: ollama serve"}
	if !errors.Is(un, cause) || !strings.Contains(un.Error(), "ollama serve") {
		t.Errorf("%v", un)
	}
	wrapped := errors.Join(errors.New("x"), un)
	for err, want := range map[error]bool{
		un: true, wrapped: true,
		&llm.UnavailableError{Provider: "p", Err: cause}: false,
		refused: false, missing: false, llm.ErrEmptyResponse: false, context.Canceled: false, nil: false,
	} {
		if got := llm.Retryable(err); got != want {
			t.Errorf("Retryable(%v) = %v, want %v", err, got, want)
		}
	}
}

func TestChecked(t *testing.T) {
	for _, text := range []string{"", "   ", "\n\t "} {
		p := llm.Checked(llmtest.NewFake(llmtest.Text(text)))
		resp, err := p.Complete(bg, llm.Request{})
		if !errors.Is(err, llm.ErrEmptyResponse) {
			t.Errorf("text %q: err = %v", text, err)
		}
		if resp.Usage.Total() == 0 {
			t.Error("an empty answer still cost tokens, and the cost must not be lost")
		}
	}
	p := llm.Checked(llmtest.NewFake(llmtest.Text("ok")))
	if resp, err := p.Complete(bg, llm.Request{}); err != nil || resp.Text != "ok" {
		t.Errorf("%+v, %v", resp, err)
	}
	boom := errors.New("boom")
	if _, err := llm.Checked(llmtest.NewFake(llmtest.Result{Err: boom})).Complete(bg, llm.Request{}); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	if llm.Checked(llmtest.NewFake()).Name() != "fake/model" {
		t.Error("the wrapper changed the name")
	}
}

func TestBudget(t *testing.T) {
	b := llm.NewBudget(30) // each fake answer costs 15
	p := llm.WithBudget(llmtest.NewFake(llmtest.Text("a"), llmtest.Text("b"), llmtest.Text("c")), b)
	if b.Left() != 30 {
		t.Errorf("left = %d", b.Left())
	}
	for i := range 2 {
		if _, err := p.Complete(bg, llm.Request{}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if b.Left() != 0 || b.Used().Total() != 30 {
		t.Errorf("left %d, used %+v", b.Left(), b.Used())
	}
	if _, err := p.Complete(bg, llm.Request{}); !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Errorf("err = %v, want ErrBudgetExceeded", err)
	}

	t.Run("one call may cross the ceiling, the next may not", func(t *testing.T) {
		b := llm.NewBudget(20)
		p := llm.WithBudget(llmtest.NewFake(llmtest.Text("a"), llmtest.Text("b"), llmtest.Text("c")), b)
		for i := range 2 {
			if _, err := p.Complete(bg, llm.Request{}); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if _, err := p.Complete(bg, llm.Request{}); !errors.Is(err, llm.ErrBudgetExceeded) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a failed call still counts what it cost", func(t *testing.T) {
		b := llm.NewBudget(100)
		failing := llmtest.Result{Response: llm.Response{Usage: llm.Usage{PromptTokens: 40}}, Err: &llm.RefusedError{Provider: "p"}}
		_, _ = llm.WithBudget(llmtest.NewFake(failing), b).Complete(bg, llm.Request{})
		if b.Used().PromptTokens != 40 {
			t.Errorf("used %+v", b.Used())
		}
	})
	t.Run("no ceiling", func(t *testing.T) {
		b := llm.NewBudget(0)
		p := llm.WithBudget(llmtest.NewFake(llmtest.Text("a"), llmtest.Text("b")), b)
		for range 2 {
			if _, err := p.Complete(bg, llm.Request{}); err != nil {
				t.Fatal(err)
			}
		}
		if b.Left() != -1 || b.Used().Total() != 30 {
			t.Errorf("left %d, used %+v", b.Left(), b.Used())
		}
	})
	t.Run("shared by several providers and goroutines", func(t *testing.T) {
		b := llm.NewBudget(0)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p := llm.WithBudget(llmtest.NewFake(llmtest.Text("a"), llmtest.Text("b")), b)
				_, _ = p.Complete(bg, llm.Request{})
				_, _ = p.Complete(bg, llm.Request{})
			}()
		}
		wg.Wait()
		if b.Used().Total() != 8*2*15 {
			t.Errorf("used %+v", b.Used())
		}
	})
}

func temporary() llmtest.Result {
	return llmtest.Result{Err: &llm.UnavailableError{Provider: "p", Err: errors.New("503"), Temporary: true}}
}

func TestRetry(t *testing.T) {
	var slept []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	opts := llm.RetryOptions{Attempts: 4, Delay: time.Second, Sleep: sleep}

	t.Run("a temporary failure is tried again, with a doubling wait", func(t *testing.T) {
		slept = nil
		f := llmtest.NewFake(temporary(), temporary(), llmtest.Text("ok"))
		resp, err := llm.WithRetry(f, opts).Complete(bg, llm.Request{})
		if err != nil || resp.Text != "ok" || len(f.Requests()) != 3 {
			t.Fatalf("%+v, %v, %d calls", resp, err, len(f.Requests()))
		}
		if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
			t.Errorf("waits = %v", slept)
		}
	})
	t.Run("the attempts run out", func(t *testing.T) {
		f := llmtest.NewFake(temporary(), temporary(), temporary(), temporary(), temporary())
		_, err := llm.WithRetry(f, opts).Complete(bg, llm.Request{})
		if !llm.Retryable(err) || len(f.Requests()) != 4 {
			t.Errorf("err %v, %d calls, want 4", err, len(f.Requests()))
		}
	})
	t.Run("nothing else is retried", func(t *testing.T) {
		for name, err := range map[string]error{
			"refusal":         &llm.RefusedError{Provider: "p"},
			"missing model":   &llm.ModelMissingError{Provider: "p", Model: "m"},
			"permanent":       &llm.UnavailableError{Provider: "p", Err: errors.New("x")},
			"empty":           llm.ErrEmptyResponse,
			"context too big": llm.ErrContextTooLong,
			"plain":           errors.New("x"),
		} {
			f := llmtest.NewFake(llmtest.Result{Err: err}, llmtest.Text("never"))
			if _, got := llm.WithRetry(f, opts).Complete(bg, llm.Request{}); !errors.Is(got, err) || len(f.Requests()) != 1 {
				t.Errorf("%s: err %v, %d calls", name, got, len(f.Requests()))
			}
		}
	})
	t.Run("a cancelled context ends the retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		f := llmtest.NewFake(temporary(), llmtest.Text("never"))
		cancelling := llm.RetryOptions{Attempts: 5, Delay: time.Second, Sleep: func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}}
		_, err := llm.WithRetry(f, cancelling).Complete(ctx, llm.Request{})
		if !llm.Retryable(err) || len(f.Requests()) != 1 {
			t.Errorf("err %v, %d calls: the failure that was being retried is what comes back", err, len(f.Requests()))
		}
	})
	t.Run("the real sleep stops with the context", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(bg, 30*time.Millisecond)
		defer cancel()
		f := llmtest.NewFake(temporary(), llmtest.Text("never"))
		start := time.Now()
		_, err := llm.WithRetry(f, llm.RetryOptions{Attempts: 3, Delay: time.Hour}).Complete(ctx, llm.Request{})
		if err == nil || time.Since(start) > 5*time.Second {
			t.Errorf("err %v after %v", err, time.Since(start))
		}
	})
	t.Run("one attempt means no retry", func(t *testing.T) {
		f := llmtest.NewFake(temporary(), llmtest.Text("never"))
		if _, err := llm.WithRetry(f, llm.RetryOptions{}).Complete(bg, llm.Request{}); err == nil || len(f.Requests()) != 1 {
			t.Errorf("err %v, %d calls", err, len(f.Requests()))
		}
	})
}

func TestObserver(t *testing.T) {
	var calls []llm.Call
	f := llmtest.NewFake(llmtest.Text("secret answer"), llmtest.Result{Err: &llm.RefusedError{Provider: "p"}})
	p := llm.WithObserver(f, func(c llm.Call) { calls = append(calls, c) })
	_, _ = p.Complete(bg, llm.Request{System: "secret prompt"})
	_, _ = p.Complete(bg, llm.Request{})
	if len(calls) != 2 || calls[0].Provider != "fake/model" || calls[0].Usage.Total() != 15 || calls[0].Finish != llm.FinishStop || calls[0].Err != nil || calls[1].Err == nil {
		t.Errorf("calls = %+v", calls)
	}
}

func TestDecoratorsCompose(t *testing.T) {
	b := llm.NewBudget(1000)
	f := llmtest.NewFake(temporary(), llmtest.Text(""), llmtest.Text("fine"))
	var p llm.Provider = f
	p = llm.Checked(p)
	p = llm.WithRetry(p, llm.RetryOptions{Attempts: 3, Sleep: func(context.Context, time.Duration) error { return nil }})
	p = llm.WithBudget(p, b)
	// attempt 1: temporary failure, retried; attempt 2: empty answer, not retried
	if _, err := p.Complete(bg, llm.Request{}); !errors.Is(err, llm.ErrEmptyResponse) {
		t.Fatalf("err = %v", err)
	}
	if len(f.Requests()) != 2 || b.Used().Total() != 15 {
		t.Errorf("%d calls, used %+v", len(f.Requests()), b.Used())
	}
}

func TestFakeAndReplay(t *testing.T) {
	f := llmtest.NewFake(llmtest.Text("one"))
	req := llm.Request{System: "s", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	if resp, err := f.Complete(bg, req); err != nil || resp.Text != "one" {
		t.Fatal(resp, err)
	}
	if _, err := f.Complete(bg, req); err == nil || !strings.Contains(err.Error(), "no more answers") {
		t.Errorf("an exhausted fake: %v", err)
	}
	if got := f.Requests(); len(got) != 2 || got[0].System != "s" {
		t.Errorf("requests = %+v", got)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := llmtest.NewFake(llmtest.Text("x")).Complete(ctx, req); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context: %v", err)
	}

	other := llm.Request{System: "s", Messages: []llm.Message{{Role: llm.User, Content: "hi!"}}}
	if llmtest.Key(req) == llmtest.Key(other) || len(llmtest.Key(req)) != 64 {
		t.Error("Key must be stable and tell requests apart")
	}
	r := &llmtest.Replay{Answers: map[string]llm.Response{llmtest.Key(req): {Text: "recorded"}}}
	if resp, err := r.Complete(bg, req); err != nil || resp.Text != "recorded" {
		t.Errorf("%+v, %v", resp, err)
	}
	if _, err := r.Complete(bg, other); err == nil || !strings.Contains(err.Error(), "no recorded answer") {
		t.Errorf("an unrecorded request: %v", err)
	}
	if _, err := llmtest.LoadReplay("nope.json"); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestLoadReplay(t *testing.T) {
	req := llm.Request{System: "s"}
	path := t.TempDir() + "/answers.json"
	data := `{"` + llmtest.Key(req) + `": {"Text": "from file", "Finish": "stop"}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := llmtest.LoadReplay(path)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := r.Complete(bg, req); err != nil || resp.Text != "from file" || resp.Finish != llm.FinishStop {
		t.Errorf("%+v, %v", resp, err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := llmtest.LoadReplay(path); err == nil {
		t.Error("bad JSON was accepted")
	}
}
