package llm

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Spec names a model as "provider/model", for example "ollama/llama3.1:8b".
type Spec struct {
	Provider string
	Model    string
}

var providerRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ParseSpec reads "provider/model".
func ParseSpec(s string) (Spec, error) {
	provider, model, ok := strings.Cut(s, "/")
	if !ok || !providerRE.MatchString(provider) || model == "" {
		return Spec{}, fmt.Errorf("%w: %q is not provider/model", ErrInvalidRequest, s)
	}
	for _, r := range model {
		if r <= ' ' || r == 0x7f {
			return Spec{}, fmt.Errorf("%w: %q has a space or a control character", ErrInvalidRequest, s)
		}
	}
	return Spec{Provider: provider, Model: model}, nil
}

// String returns the spec in the form ParseSpec reads.
func (s Spec) String() string { return s.Provider + "/" + s.Model }

// Options are what a factory needs besides the model name.
type Options struct {
	// BaseURL is where the provider's API is. Empty means the provider's default.
	BaseURL string
}

// Factory makes a provider for a model.
type Factory func(model string, opts Options) (Provider, error)

// Registry maps provider names to factories.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

// Register adds a factory. A name can be registered once.
func (r *Registry) Register(name string, f Factory) error {
	if !providerRE.MatchString(name) || f == nil {
		return fmt.Errorf("%w: cannot register provider %q", ErrInvalidRequest, name)
	}
	if _, dup := r.factories[name]; dup {
		return fmt.Errorf("%w: provider %q is registered twice", ErrInvalidRequest, name)
	}
	r.factories[name] = f
	return nil
}

// New makes the provider a spec names.
func (r *Registry) New(spec string, opts Options) (Provider, error) {
	s, err := ParseSpec(spec)
	if err != nil {
		return nil, err
	}
	f, ok := r.factories[s.Provider]
	if !ok {
		hint := ""
		if _, has := r.factories["openrouter"]; has {
			hint = "; a model at OpenRouter is written openrouter/<vendor>/<model>, for example openrouter/" + spec
		}
		return nil, fmt.Errorf("%w: there is no provider %q (known: %s%s)", ErrInvalidRequest, s.Provider, strings.Join(r.names(), ", "), hint)
	}
	return f(s.Model, opts)
}

func (r *Registry) names() []string {
	var out []string
	for n := range r.factories {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Resolver looks up the addresses of a host name.
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// SystemResolver resolves names with the system's resolver.
func SystemResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// IsLocal reports whether a base URL points at this very machine.
func IsLocal(ctx context.Context, baseURL string, resolve Resolver) (bool, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return false, fmt.Errorf("%w: %q is not an http or https URL", ErrInvalidRequest, baseURL)
	}
	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().IsLoopback(), nil
	}
	if resolve == nil {
		resolve = SystemResolver
	}
	addrs, err := resolve(ctx, host)
	if err != nil || len(addrs) == 0 {
		return false, nil //nolint:nilerr // a name that does not resolve is treated as remote
	}
	for _, a := range addrs {
		if !a.Unmap().IsLoopback() {
			return false, nil
		}
	}
	return true, nil
}
