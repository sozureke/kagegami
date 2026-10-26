// Package models builds the models Kagegami uses from the configuration: the providers for the
// two tiers, with the checks that belong to every model put around them.
package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/pkg/llm"
	"github.com/sozureke/kagegami/pkg/llm/ollama"
	"github.com/sozureke/kagegami/pkg/llm/openrouter"
)

// Tier names for usage records.
const (
	Tier1 = "tier1"
	Tier2 = "tier2"
)

// ErrCloudFallbackOff is returned when a tier's model is not on this machine and
// model.cloud_fallback is false. It is never possible to reach such a provider with the
// setting off: Build refuses to even construct it (M6.8).
var ErrCloudFallbackOff = errors.New("the model is not on this machine and model.cloud_fallback is false")

// Options adjust Build; the zero value is right for normal use.
type Options struct {
	// Record is told about every call to a model: which tier, which model, what it cost, whether
	// it gave a usable answer. It never sees a prompt or an answer.
	Record func(tier, model string, usage llm.Usage, ok bool)
	// Resolver looks up host names to decide whether Ollama is on this machine; nil uses the system's.
	Resolver llm.Resolver
	// Retry configures the retries of temporary failures; the zero value is three attempts a second apart.
	Retry llm.RetryOptions
	// Registry lets a test supply the providers; nil uses the real ones.
	Registry *llm.Registry
	// Getenv reads the environment, which is where the key of a cloud provider comes from; nil
	// means the environment of the process.
	Getenv func(string) string
}

// Set holds the models of a session.
type Set struct {
	// Tier1 reads the output of commands that no parser knows.
	Tier1 llm.Provider
	// Tier2 answers `next` and `pipeline`; it is the model of Tier1 when the configuration says so.
	Tier2 llm.Provider
	// Budget is the token ceiling of the session, shared by both tiers.
	Budget *llm.Budget
	// Remote says that a model is reached over an address that is not this machine's own, so
	// whatever is sent leaves the machine and must be masked.
	Remote bool
}

// Build makes the models the configuration names. It does not contact any server.
func Build(ctx context.Context, cfg config.Config, o Options) (*Set, error) {
	reg := o.Registry
	if reg == nil {
		reg = llm.NewRegistry()
		if err := ollama.Register(reg, ollama.Config{BaseURL: cfg.Providers.Ollama.BaseURL}); err != nil {
			return nil, err
		}
		getenv := o.Getenv
		if getenv == nil {
			getenv = os.Getenv
		}
		if err := openrouter.Register(reg, getenv); err != nil {
			return nil, err
		}
	}
	retry := o.Retry
	if retry.Attempts == 0 {
		retry.Attempts, retry.Delay = 3, time.Second
	}
	set := &Set{Budget: llm.NewBudget(cfg.Limits.SessionTokens)}

	build := func(tier, spec string) (llm.Provider, error) {
		parsed, err := llm.ParseSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("model.%s: %w", tier, err)
		}
		// the address first: a bad one is the setting's fault, not the model's
		remote, err := isRemote(ctx, cfg, parsed.Provider, o.Resolver)
		if err != nil {
			return nil, err
		}
		if remote && !cfg.Model.CloudFallback {
			return nil, fmt.Errorf("model.%s: %w (%s): set model.cloud_fallback: true in the config to allow it", tier, ErrCloudFallbackOff, spec)
		}
		set.Remote = set.Remote || remote
		p, err := reg.New(spec, llm.Options{BaseURL: baseURL(cfg, parsed.Provider)})
		if err != nil {
			return nil, fmt.Errorf("model.%s: %w", tier, err)
		}

		// From the inside out: an answer with no text is an error; the budget is checked before
		// a call and every attempt is counted; a call is reported; a temporary failure is retried.
		p = llm.Checked(p)
		p = llm.WithBudget(p, set.Budget)
		if o.Record != nil {
			p = llm.WithObserver(p, func(c llm.Call) { o.Record(tier, c.Provider, c.Usage, c.Err == nil) })
		}
		return llm.WithRetry(p, retry), nil
	}

	var err error
	if set.Tier1, err = build(Tier1, cfg.Model.Tier1); err != nil {
		return nil, err
	}
	// Even when both tiers use the same model each gets its own provider, so that the use of
	// each is counted apart; they share one budget.
	if set.Tier2, err = build(Tier2, cfg.Model.Tier2Spec()); err != nil {
		return nil, err
	}
	return set, nil
}

// baseURL is the address the configuration gives for a provider.
func baseURL(cfg config.Config, provider string) string {
	if provider == "ollama" {
		return cfg.Providers.Ollama.BaseURL
	}
	return ""
}

// isRemote says whether data sent to the provider leaves this machine. Ollama is local if its
// address is; every other provider is a service on the internet.
func isRemote(ctx context.Context, cfg config.Config, provider string, resolve llm.Resolver) (bool, error) {
	if provider != "ollama" {
		return true, nil
	}
	local, err := llm.IsLocal(ctx, cfg.Providers.Ollama.BaseURL, resolve)
	if err != nil {
		return false, errors.New("providers.ollama.base_url: " + err.Error())
	}
	return !local, nil
}

// RecordTo returns a Record function that keeps the calls in the case database, where
// `kagegami map --stats` reads them. A failure to write is reported to onError and does not
// disturb the call.
func RecordTo(st *store.Store, onError func(error)) func(tier, model string, usage llm.Usage, ok bool) {
	return func(tier, model string, usage llm.Usage, ok bool) {
		err := st.Repo().AddUsage(context.Background(), store.Usage{
			Tier: tier, Model: model, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, OK: ok,
		})
		if err != nil && onError != nil {
			onError(err)
		}
	}
}
