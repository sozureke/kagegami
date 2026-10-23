package models

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/pkg/llm"
	"github.com/sozureke/kagegami/pkg/llm/llmtest"
)

var bg = context.Background()

type record struct {
	tier, model string
	usage       llm.Usage
	ok          bool
}

type recorder struct {
	mu   sync.Mutex
	rows []record
}

func (r *recorder) record(tier, model string, u llm.Usage, ok bool) {
	r.mu.Lock()
	r.rows = append(r.rows, record{tier, model, u, ok})
	r.mu.Unlock()
}

// registry offers one scripted provider per model name.
func registry(t *testing.T, fakes map[string]*llmtest.Fake) *llm.Registry {
	t.Helper()
	reg := llm.NewRegistry()
	if err := reg.Register("ollama", func(model string, _ llm.Options) (llm.Provider, error) {
		f, ok := fakes[model]
		if !ok {
			t.Fatalf("no fake for %q", model)
		}
		f.ModelName = "ollama/" + model
		return f, nil
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func fast() llm.RetryOptions {
	return llm.RetryOptions{Attempts: 3, Delay: time.Millisecond, Sleep: func(context.Context, time.Duration) error { return nil }}
}

func req() llm.Request { return llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}} }

func TestBuildOneModelForBothTiers(t *testing.T) {
	cfg := config.Default() // tier1 llama3.1:8b, tier2 empty
	f1, f2 := llmtest.NewFake(llmtest.Text("a")), llmtest.NewFake(llmtest.Text("b"))
	// both tiers ask the registry for the same model; give each its own scripted fake
	calls := 0
	reg := llm.NewRegistry()
	_ = reg.Register("ollama", func(model string, _ llm.Options) (llm.Provider, error) {
		calls++
		f := []*llmtest.Fake{f1, f2}[calls-1]
		f.ModelName = "ollama/" + model
		return f, nil
	})
	rec := &recorder{}
	set, err := Build(bg, cfg, Options{Registry: reg, Record: rec.record, Retry: fast()})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("%d providers were made, want one per tier", calls)
	}
	if _, err := set.Tier1.Complete(bg, req()); err != nil {
		t.Fatal(err)
	}
	if _, err := set.Tier2.Complete(bg, req()); err != nil {
		t.Fatal(err)
	}
	if len(rec.rows) != 2 || rec.rows[0].tier != Tier1 || rec.rows[1].tier != Tier2 || rec.rows[0].model != "ollama/llama3.1:8b" || rec.rows[1].model != "ollama/llama3.1:8b" {
		t.Errorf("records = %+v: each tier is counted apart, also when the model is the same", rec.rows)
	}
	if rec.rows[0].usage.Total() != 15 || !rec.rows[0].ok {
		t.Errorf("record = %+v", rec.rows[0])
	}
	if set.Budget.Used().Total() != 30 {
		t.Errorf("the tiers do not share the budget: used %+v", set.Budget.Used())
	}
}

func TestBuildTwoModels(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Tier2 = "ollama/qwen2.5:14b"
	fakes := map[string]*llmtest.Fake{"llama3.1:8b": llmtest.NewFake(llmtest.Text("one")), "qwen2.5:14b": llmtest.NewFake(llmtest.Text("two"))}
	set, err := Build(bg, cfg, Options{Registry: registry(t, fakes)})
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := set.Tier1.Complete(bg, req())
	r2, _ := set.Tier2.Complete(bg, req())
	if r1.Text != "one" || r2.Text != "two" {
		t.Errorf("answers = %q, %q", r1.Text, r2.Text)
	}
}

func TestTheBudgetStopsBothTiersAndTheCostOfFailuresCounts(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.SessionTokens = 40 // each answer costs 15
	cfg.Model.Tier2 = "ollama/big:14b"
	fakes := map[string]*llmtest.Fake{
		"llama3.1:8b": llmtest.NewFake(llmtest.Text("a"), llmtest.Text("b"), llmtest.Text("c")),
		"big:14b":     llmtest.NewFake(llmtest.Text("d"), llmtest.Text("e")),
	}
	set, err := Build(bg, cfg, Options{Registry: registry(t, fakes)})
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []llm.Provider{set.Tier1, set.Tier2, set.Tier1} { // 45 tokens: the ceiling is crossed by the third
		if _, err := p.Complete(bg, req()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	for name, p := range map[string]llm.Provider{"tier1": set.Tier1, "tier2": set.Tier2} {
		if _, err := p.Complete(bg, req()); !errors.Is(err, llm.ErrBudgetExceeded) {
			t.Errorf("%s after the ceiling: %v", name, err)
		}
	}
}

func TestNoCeilingWhenTheConfigurationSaysZero(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.SessionTokens = 0
	set, err := Build(bg, cfg, Options{Registry: registry(t, map[string]*llmtest.Fake{"llama3.1:8b": llmtest.NewFake(llmtest.Text("a"), llmtest.Text("b"))})})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := set.Tier1.Complete(bg, req()); err != nil {
			t.Fatal(err)
		}
	}
	if set.Budget.Left() != -1 {
		t.Errorf("left = %d, want no ceiling", set.Budget.Left())
	}
}

func TestChecksAndRetriesAreInPlace(t *testing.T) {
	temporary := llmtest.Result{Err: &llm.UnavailableError{Provider: "p", Err: errors.New("503"), Temporary: true}}
	empty := llmtest.Text("  ")
	rec := &recorder{}
	f := llmtest.NewFake(temporary, temporary, llmtest.Text("fine"), empty, llmtest.Text("never reached"))
	set, err := Build(bg, config.Default(), Options{Registry: registry(t, map[string]*llmtest.Fake{"llama3.1:8b": f}), Record: rec.record, Retry: fast()})
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := set.Tier1.Complete(bg, req()); err != nil || resp.Text != "fine" {
		t.Fatalf("%+v, %v: two temporary failures are retried", resp, err)
	}
	if _, err := set.Tier1.Complete(bg, req()); !errors.Is(err, llm.ErrEmptyResponse) {
		t.Errorf("an empty answer: %v", err)
	}
	if len(f.Requests()) != 4 {
		t.Errorf("%d calls: an empty answer must not be retried", len(f.Requests()))
	}
	// every attempt is reported, the failed ones with ok=false
	oks := ""
	for _, r := range rec.rows {
		if r.ok {
			oks += "y"
		} else {
			oks += "n"
		}
	}
	if oks != "nnyn" {
		t.Errorf("records = %q (n = no usable answer)", oks)
	}
}

func TestRemoteIsToldByTheAddress(t *testing.T) {
	names := func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "localhost":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		case "mac.local":
			return []netip.Addr{netip.MustParseAddr("192.168.64.1")}, nil
		}
		return nil, errors.New("no such host")
	}
	fakes := func() map[string]*llmtest.Fake { return map[string]*llmtest.Fake{"llama3.1:8b": llmtest.NewFake()} }
	for url, want := range map[string]bool{
		"http://localhost:11434":     false,
		"http://127.0.0.1:11434":     false,
		"http://[::1]:11434":         false,
		"http://192.168.64.1:11434":  true,
		"http://mac.local:11434":     true,
		"http://unknown.example":     true,
		"https://ollama.example.com": true,
	} {
		cfg := config.Default()
		cfg.Providers.Ollama.BaseURL = url
		cfg.Model.CloudFallback = true // this test is about Remote, not about the gate M6.8 adds; see TestCloudFallbackGate
		set, err := Build(bg, cfg, Options{Registry: registry(t, fakes()), Resolver: names})
		if err != nil || set.Remote != want {
			t.Errorf("%s: remote = %v, %v; want %v", url, set != nil && set.Remote, err, want)
		}
	}
	t.Run("a model that is not Ollama is remote", func(t *testing.T) {
		cfg := config.Default()
		cfg.Model.Tier2 = "anthropic/claude-x"
		cfg.Model.CloudFallback = true
		reg := registry(t, fakes())
		_ = reg.Register("anthropic", func(string, llm.Options) (llm.Provider, error) { return llmtest.NewFake(), nil })
		set, err := Build(bg, cfg, Options{Registry: reg, Resolver: names})
		if err != nil || !set.Remote {
			t.Errorf("remote = %v, %v", set != nil && set.Remote, err)
		}
	})
}

// M6.8: with cloud_fallback false (the default), no code path can reach a provider that is not
// on this machine. Build refuses to even construct one.
func TestCloudFallbackGate(t *testing.T) {
	names := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "localhost" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return nil, errors.New("no such host")
	}
	fakes := func() map[string]*llmtest.Fake { return map[string]*llmtest.Fake{"llama3.1:8b": llmtest.NewFake()} }

	t.Run("a remote Ollama is refused by default", func(t *testing.T) {
		cfg := config.Default()
		cfg.Providers.Ollama.BaseURL = "http://192.168.64.1:11434"
		_, err := Build(bg, cfg, Options{Registry: registry(t, fakes()), Resolver: names})
		if !errors.Is(err, ErrCloudFallbackOff) || !strings.Contains(err.Error(), "model.tier1") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a cloud provider is refused by default", func(t *testing.T) {
		cfg := config.Default()
		cfg.Model.Tier2 = "anthropic/claude-x"
		reg := registry(t, fakes())
		called := false
		_ = reg.Register("anthropic", func(string, llm.Options) (llm.Provider, error) { called = true; return llmtest.NewFake(), nil })
		_, err := Build(bg, cfg, Options{Registry: reg, Resolver: names})
		if !errors.Is(err, ErrCloudFallbackOff) || !strings.Contains(err.Error(), "model.tier2") {
			t.Errorf("err = %v", err)
		}
		if called {
			t.Error("the cloud provider's factory ran although cloud_fallback is false: it must never even be constructed")
		}
	})
	t.Run("a local Ollama is never gated, cloud_fallback false or not", func(t *testing.T) {
		for _, cloud := range []bool{false, true} {
			cfg := config.Default()
			cfg.Model.CloudFallback = cloud
			if _, err := Build(bg, cfg, Options{Registry: registry(t, fakes()), Resolver: names}); err != nil {
				t.Errorf("cloud_fallback=%v: err = %v", cloud, err)
			}
		}
	})
	t.Run("cloud_fallback true allows a remote model", func(t *testing.T) {
		cfg := config.Default()
		cfg.Providers.Ollama.BaseURL = "http://192.168.64.1:11434"
		cfg.Model.CloudFallback = true
		set, err := Build(bg, cfg, Options{Registry: registry(t, fakes()), Resolver: names})
		if err != nil || !set.Remote {
			t.Errorf("set = %+v, err = %v", set, err)
		}
	})
	t.Run("one tier remote and gated stops the whole build, even if the other tier is local", func(t *testing.T) {
		cfg := config.Default()
		cfg.Model.Tier2 = "anthropic/claude-x"
		reg := registry(t, fakes())
		_ = reg.Register("anthropic", func(string, llm.Options) (llm.Provider, error) { return llmtest.NewFake(), nil })
		if _, err := Build(bg, cfg, Options{Registry: reg, Resolver: names}); !errors.Is(err, ErrCloudFallbackOff) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestBuildNamesTheSettingThatIsWrong(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Tier2 = "mistral/large"
	cfg.Model.CloudFallback = true // past the gate M6.8 adds, to reach the "no such provider" error below
	_, err := Build(bg, cfg, Options{})
	if err == nil || !strings.Contains(err.Error(), "model.tier2") || !strings.Contains(err.Error(), `"mistral"`) || !strings.Contains(err.Error(), "ollama") {
		t.Errorf("err = %v", err)
	}
	cfg = config.Default()
	cfg.Model.Tier1 = "nonsense"
	if _, err := Build(bg, cfg, Options{}); err == nil || !strings.Contains(err.Error(), "model.tier1") {
		t.Errorf("err = %v", err)
	}
	cfg = config.Default()
	cfg.Providers.Ollama.BaseURL = "not a url"
	if _, err := Build(bg, cfg, Options{}); err == nil || !strings.Contains(err.Error(), "ollama") {
		t.Errorf("err = %v", err)
	}
}

func TestBuildDoesNotContactAServer(t *testing.T) {
	// the real providers, at an address where nothing listens
	cfg := config.Default()
	cfg.Providers.Ollama.BaseURL = "http://127.0.0.1:1"
	set, err := Build(bg, cfg, Options{})
	if err != nil || set.Tier1.Name() != "ollama/llama3.1:8b" || set.Tier2.Name() != "ollama/llama3.1:8b" || set.Remote {
		t.Errorf("%v, %v", set, err)
	}
}

func TestOpenRouterIsACloudProviderBehindTheSameGateAndTheKeyComesFromTheEnvironment(t *testing.T) {
	names := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	const key = "sk-or-test-NOT-A-REAL-KEY"
	cfg := config.Default()
	cfg.Model.Tier1 = "ollama/llama3.1:8b"
	cfg.Model.Tier2 = "openrouter/vendor/model-x"
	env := map[string]string{}
	opts := Options{Resolver: names, Getenv: func(k string) string { return env[k] }, Retry: fast()}

	if _, err := Build(bg, cfg, opts); !errors.Is(err, ErrCloudFallbackOff) || !strings.Contains(err.Error(), "model.tier2") {
		t.Fatalf("with cloud_fallback off OpenRouter is refused before anything is built: %v", err)
	}

	cfg.Model.CloudFallback = true
	if _, err := Build(bg, cfg, opts); err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("without a key the error says which variable to set: %v", err)
	}

	env["OPENROUTER_API_KEY"] = key
	set, err := Build(bg, cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Remote {
		t.Error("OpenRouter is a remote model: whatever is sent leaves the machine, and the caller must say so")
	}
	if got := set.Tier2.Name(); got != "openrouter/vendor/model-x" {
		t.Errorf("tier 2 is %q", got)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", set, set.Tier2, set.Tier2), key) {
		t.Error("the key is in what the models print")
	}
}
