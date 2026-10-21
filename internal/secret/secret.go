package secret

import (
	"fmt"
	"io"
	"log/slog"
)

// Redacted is what a non-empty String prints instead of its value.
const Redacted = "•••"

type String struct {
	v string
}

// New wraps a plaintext value.
func New(s string) String { return String{v: s} }

// Reveal returns the plaintext value. Use it only where the real value is needed.
func (s String) Reveal() string { return s.v }

// IsEmpty reports whether there is no secret at all.
func (s String) IsEmpty() bool { return s.v == "" }

// String implements fmt.Stringer.
func (s String) String() string { return s.redacted() }

// Format implements fmt.Formatter, which takes over every fmt verb.
func (s String) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('#'):
		_, _ = io.WriteString(f, s.GoString())
	case verb == 'q':
		_, _ = io.WriteString(f, `"`+s.redacted()+`"`)
	default:
		_, _ = io.WriteString(f, s.redacted())
	}
}

// GoString implements fmt.GoStringer, used by %#v.
func (s String) GoString() string {
	if s.v == "" {
		return `secret.String("")`
	}
	return `secret.String("` + Redacted + `")`
}

// MarshalText implements encoding.TextMarshaler.
func (s String) MarshalText() ([]byte, error) { return []byte(s.redacted()), nil }

// MarshalJSON implements json.Marshaler. A secret is never decoded back from JSON.
func (s String) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.redacted() + `"`), nil
}

// LogValue implements slog.LogValuer.
func (s String) LogValue() slog.Value { return slog.StringValue(s.redacted()) }

// An empty secret prints as empty, so "no secret" stays distinguishable from "hidden".
func (s String) redacted() string {
	if s.v == "" {
		return ""
	}
	return Redacted
}
