package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Checked wraps a provider so that a successful call always has text. A provider that answers
// with nothing (HTTP 200 and an empty body) is an error, not a success.
func Checked(p Provider) Provider { return checked{p} }

type checked struct{ Provider }

func (c checked) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := c.Provider.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if strings.TrimSpace(resp.Text) == "" {
		return Response{Usage: resp.Usage}, fmt.Errorf("%s: %w", c.Name(), ErrEmptyResponse)
	}
	return resp, nil
}

// Budget is a ceiling on the tokens a session may use, shared by every provider wrapped with it.
type Budget struct {
	mu      sync.Mutex
	ceiling int
	used    Usage
}

// NewBudget returns a budget of ceiling tokens in all. Zero or less means no ceiling.
func NewBudget(ceiling int) *Budget { return &Budget{ceiling: ceiling} }

// Used returns what has been spent.
func (b *Budget) Used() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Left returns the tokens left, or -1 if there is no ceiling.
func (b *Budget) Left() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ceiling <= 0 {
		return -1
	}
	return max(b.ceiling-b.used.Total(), 0)
}

func (b *Budget) exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ceiling > 0 && b.used.Total() >= b.ceiling
}

func (b *Budget) spend(u Usage) {
	b.mu.Lock()
	b.used = b.used.Add(u)
	b.mu.Unlock()
}

// WithBudget wraps a provider so that it stops once the budget is used up. A call that is
// already running is not cut: the ceiling is checked before a call and the cost added after it,
// so one call can go over it. What a call cost is counted even when it failed after the model had worked.
func WithBudget(p Provider, b *Budget) Provider { return budgeted{p, b} }

type budgeted struct {
	Provider
	b *Budget
}

func (w budgeted) Complete(ctx context.Context, req Request) (Response, error) {
	if w.b.exhausted() {
		return Response{}, fmt.Errorf("%s: %w", w.Name(), ErrBudgetExceeded)
	}
	resp, err := w.Provider.Complete(ctx, req)
	w.b.spend(resp.Usage)
	return resp, err
}

// RetryOptions configure WithRetry.
type RetryOptions struct {
	// Attempts is the number of calls in all, the first included. Less than 1 means 1.
	Attempts int
	// Delay is the wait before the first retry; it doubles after each.
	Delay time.Duration
	// Sleep waits for d or until ctx is done; nil uses a timer. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// WithRetry wraps a provider so that a temporary failure of the service (it is starting up, the
// network dropped, a server error) is tried again. Nothing else is: a refusal, an empty
// answer, a missing model and a cancelled context come back at once.
func WithRetry(p Provider, o RetryOptions) Provider {
	if o.Attempts < 1 {
		o.Attempts = 1
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	return retrying{p, o}
}

type retrying struct {
	Provider
	o RetryOptions
}

func (r retrying) Complete(ctx context.Context, req Request) (Response, error) {
	delay := r.o.Delay
	for attempt := 1; ; attempt++ {
		resp, err := r.Provider.Complete(ctx, req)
		if err == nil || attempt >= r.o.Attempts || !Retryable(err) || ctx.Err() != nil {
			return resp, err
		}
		if serr := r.o.Sleep(ctx, delay); serr != nil {
			return Response{}, err
		}
		delay *= 2
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Call describes one call to a provider for an observer. It holds no prompt and no answer: what
// was said to a model, and what it said, is not for logs.
type Call struct {
	Provider string
	Duration time.Duration
	Usage    Usage
	Finish   FinishReason
	Err      error
}

// WithObserver wraps a provider so that observe is called after every call.
func WithObserver(p Provider, observe func(Call)) Provider { return observed{p, observe} }

type observed struct {
	Provider
	observe func(Call)
}

func (o observed) Complete(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := o.Provider.Complete(ctx, req)
	o.observe(Call{Provider: o.Name(), Duration: time.Since(start), Usage: resp.Usage, Finish: resp.Finish, Err: err})
	return resp, err
}
