package secret_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/sozureke/kagegami/internal/secret"
)

const plain = "hunter2"

func leaks(out string) bool { return strings.Contains(out, plain) }

func assertNoLeak(t *testing.T, name, out string) {
	t.Helper()
	if leaks(out) {
		t.Errorf("%s leaked the secret: %q", name, out)
	}
}

// Exported is how our own types hold secrets: an exported field.
type Exported struct {
	User   string
	Secret secret.String
}

// --- Values -----------------------------------------------------------------------------

func TestRevealAndNew(t *testing.T) {
	s := secret.New(plain)
	if got := s.Reveal(); got != plain {
		t.Errorf("Reveal = %q, want %q", got, plain)
	}
	if s.IsEmpty() {
		t.Error("IsEmpty = true for a non-empty secret")
	}

	var zero secret.String
	if !zero.IsEmpty() || zero.Reveal() != "" {
		t.Errorf("zero value: IsEmpty=%v Reveal=%q, want an empty secret", zero.IsEmpty(), zero.Reveal())
	}
	if !secret.New("").IsEmpty() {
		t.Error("New(\"\") must be empty")
	}
	a1, a2, b := secret.New("a"), secret.New("a"), secret.New("b")
	if a1 != a2 || a1 == b {
		t.Error("equal values must compare equal and different values must not")
	}
}

// --- fmt --------------------------------------------------------------------------------

func TestFormatEveryVerb(t *testing.T) {
	s := secret.New(plain)

	// Verbs that suit a string, a number, a bool and nothing at all. Passing the format
	// through a variable keeps go vet from objecting to the deliberately wrong ones.
	verbs := []string{
		"%s", "%v", "%+v", "%#v", "%q", "%x", "%X", "%d", "%t", "%f", "%e", "%g", "%c", "%U",
		"%b", "%o", "%O", "%T", "%08v", "%10s", "%-10s", "%.2s", "%5.1v", "%[1]v", "%!", "%z", "%%",
	}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			assertNoLeak(t, "Sprintf("+verb+")", fmt.Sprintf(verb, s))
		})
	}
}

func TestFormatOutput(t *testing.T) {
	s := secret.New(plain)
	tests := []struct {
		format string
		want   string
	}{
		{"%s", "•••"},
		{"%v", "•••"},
		{"%+v", "•••"},
		{"%q", `"•••"`},
		{"%#v", `secret.String("•••")`},
		{"%d", "•••"},
		{"%x", "•••"},
	}
	for _, tt := range tests {
		if got := fmt.Sprintf(tt.format, s); got != tt.want {
			t.Errorf("Sprintf(%q) = %q, want %q", tt.format, got, tt.want)
		}
	}
	if got := fmt.Sprint(s); got != "•••" {
		t.Errorf("Sprint = %q", got)
	}
	if got := fmt.Sprintln(s); got != "•••\n" {
		t.Errorf("Sprintln = %q", got)
	}
	if got := s.String(); got != "•••" {
		t.Errorf("String() = %q", got)
	}
	if got := s.GoString(); got != `secret.String("•••")` {
		t.Errorf("GoString() = %q", got)
	}
	if secret.Redacted != "•••" {
		t.Errorf("Redacted = %q", secret.Redacted)
	}
}

func TestFormatThroughPointersAndContainers(t *testing.T) {
	s := secret.New(plain)
	e := Exported{User: "root", Secret: s}

	values := map[string]any{
		"pointer to secret":     &s,
		"struct":                e,
		"pointer to struct":     &e,
		"slice":                 []secret.String{s, s},
		"array":                 [1]secret.String{s},
		"map value":             map[string]secret.String{"k": s},
		"map key":               map[secret.String]int{s: 1},
		"slice of structs":      []Exported{e},
		"slice of pointers":     []*Exported{&e},
		"nested struct":         struct{ Inner Exported }{e},
		"interface holding it":  any(s),
		"error built from it":   fmt.Errorf("login failed for %v", s),
		"wrapped error":         fmt.Errorf("outer: %w", fmt.Errorf("inner %v", s)),
		"error joined":          errors.Join(errors.New("a"), fmt.Errorf("b %v", s)),
		"pointer in a struct":   struct{ P *secret.String }{&s},
		"secret and plain text": "user=root password=" + fmt.Sprint(s),
	}
	for name, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			assertNoLeak(t, name+" "+verb, fmt.Sprintf(verb, v))
		}
	}
}

// fmt cannot call methods on unexported struct fields, so it prints their contents
// through reflection. This documents the limitation described in the package comment;
// it is why our own types keep secret fields exported, and why the store has a test
// that checks for unexported ones. If this test ever fails, the limitation is gone and
// the documentation should be updated.
func TestUnexportedFieldLimitation(t *testing.T) {
	type holder struct {
		user   string
		secret secret.String
	}
	h := holder{user: "root", secret: secret.New(plain)}

	if !leaks(fmt.Sprintf("%v", h)) {
		t.Error("an unexported secret field no longer leaks when printed with the v verb: update the package comment")
	}
	if !leaks(fmt.Sprintf("%+v", h)) {
		t.Error("an unexported secret field no longer leaks when printed with the plus-v verb: update the package comment")
	}
}

// A verb that does not fit a pointer (here %s on a pointer to a struct inside a slice)
// makes fmt build a "%!s(...)" error text and, while doing that, it stops calling any
// methods, so the secret field is printed through reflection. Use %v or %+v for values
// that hold secrets, and slog for logging. go vet reports this misuse, so it does not get
// past make lint; it is documented here so the limitation is known.
func TestBadVerbOnPointerInsideContainerLimitation(t *testing.T) {
	e := Exported{User: "root", Secret: secret.New(plain)}

	wrongVerb := "%s"
	if !leaks(fmt.Sprintf(wrongVerb, []*Exported{&e})) { //nolint:staticcheck // deliberately the wrong verb, to document the limitation
		t.Error("the s verb on a slice of struct pointers no longer leaks: update the package comment")
	}
	// The same value with the verbs people actually use is safe.
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		assertNoLeak(t, "[]*Exported "+verb, fmt.Sprintf(verb, []*Exported{&e}))
	}
}

// --- Empty secrets ----------------------------------------------------------------------

func TestEmptySecretStaysDistinguishable(t *testing.T) {
	var s secret.String

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"%v", fmt.Sprintf("%v", s), ""},
		{"%s", fmt.Sprintf("%s", s), ""},
		{"%q", fmt.Sprintf("%q", s), `""`},
		{"%#v", fmt.Sprintf("%#v", s), `secret.String("")`},
		{"String", s.String(), ""},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s of an empty secret = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	text, err := s.MarshalText()
	if err != nil || len(text) != 0 {
		t.Errorf("MarshalText = %q, %v; want empty", text, err)
	}
	js, err := json.Marshal(s)
	if err != nil || string(js) != `""` {
		t.Errorf("json = %s, %v; want \"\"", js, err)
	}
	if got := s.LogValue().String(); got != "" {
		t.Errorf("LogValue = %q, want empty", got)
	}
}

// --- JSON, text, YAML -------------------------------------------------------------------

func TestJSON(t *testing.T) {
	s := secret.New(plain)

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"value", s, `"•••"`},
		{"pointer", &s, `"•••"`},
		{"struct", Exported{User: "root", Secret: s}, `{"User":"root","Secret":"•••"}`},
		{"map value", map[string]secret.String{"k": s}, `{"k":"•••"}`},
		{"slice", []secret.String{s}, `["•••"]`},
	}
	for _, tt := range tests {
		got, err := json.Marshal(tt.value)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if string(got) != tt.want {
			t.Errorf("%s: json = %s, want %s", tt.name, got, tt.want)
		}
	}

	indented, err := json.MarshalIndent(Exported{User: "root", Secret: s}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, "MarshalIndent", string(indented))

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(Exported{Secret: s}); err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, "Encoder", buf.String())
}

func TestSecretIsNeverRestoredFromJSON(t *testing.T) {
	var e Exported
	err := json.Unmarshal([]byte(`{"User":"root","Secret":"`+plain+`"}`), &e)

	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("Unmarshal err = %v, want an UnmarshalTypeError: a secret must not be accepted from JSON", err)
	}
	if !e.Secret.IsEmpty() {
		t.Errorf("a secret was decoded from JSON: %q; secrets must never round-trip through output formats", e.Secret.Reveal())
	}
}

func TestMarshalText(t *testing.T) {
	got, err := secret.New(plain).MarshalText()
	if err != nil || string(got) != "•••" {
		t.Errorf("MarshalText = %q, %v; want •••", got, err)
	}
}

func TestYAML(t *testing.T) {
	out, err := yaml.Marshal(Exported{User: "root", Secret: secret.New(plain)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, "yaml", string(out))
}

// --- slog -------------------------------------------------------------------------------

func TestSlog(t *testing.T) {
	s := secret.New(plain)
	e := Exported{User: "root", Secret: s}

	handlers := map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	}
	for name, newHandler := range handlers {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(newHandler(&buf))

			logger.Info("attribute", "cred", s)
			logger.Info("any", slog.Any("cred", s))
			logger.Info("struct", "cred", e)
			logger.Info("pointer", "cred", &e)
			logger.Info("group", slog.Group("auth", "user", "root", "secret", s))
			logger.Info("slice", "creds", []secret.String{s})
			logger.With("cred", s).Info("with")
			logger.WithGroup("g").With("cred", s).Info("with group")
			logger.Error("error value", "err", fmt.Errorf("bad login %v", s))

			assertNoLeak(t, "slog "+name, buf.String())
			if !strings.Contains(buf.String(), "•••") {
				t.Errorf("slog %s output does not show the redaction marker: %s", name, buf.String())
			}
		})
	}
}

func TestLogValue(t *testing.T) {
	if got := secret.New(plain).LogValue().String(); got != "•••" {
		t.Errorf("LogValue = %q, want •••", got)
	}
}

// --- Property ---------------------------------------------------------------------------

// render prints a secret through every output path and returns all the text. Time is
// dropped from the log lines so two renderings can be compared.
func render(t *testing.T, value string) string {
	t.Helper()
	s := secret.New(value)
	e := Exported{User: "u", Secret: s}

	var out strings.Builder
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%t"} {
		fmt.Fprintf(&out, format+"\n", s)
		fmt.Fprintf(&out, format+"\n", e)
		fmt.Fprintf(&out, format+"\n", []secret.String{s})
	}
	for _, v := range []any{s, e, []secret.String{s}, map[string]secret.String{"k": s}} {
		js, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(js)
		out.WriteByte('\n')
	}
	yml, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	out.Write(yml)

	noTime := &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}
	var buf bytes.Buffer
	for _, h := range []slog.Handler{slog.NewTextHandler(&buf, noTime), slog.NewJSONHandler(&buf, noTime)} {
		slog.New(h).Info("m", "cred", s, "struct", e, slog.Group("g", "s", s))
	}
	out.Write(buf.Bytes())
	return out.String()
}

// The output of a secret must not depend on its value at all: two different secrets
// print identically. Any leak, through any path, would make the renderings differ.
func FuzzOutputDoesNotDependOnTheValue(f *testing.F) {
	for _, seed := range []string{"hunter2", "p@ss w0rd", "%s%d", "\x00", "✓", "a\nb", `"quoted"`, "••••", "•"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if value == "" { // an empty secret is deliberately shown differently
			t.Skip()
		}
		other := "different-" + value
		if got, want := render(t, value), render(t, other); got != want {
			t.Fatalf("output depends on the secret value %q:\n%s\n---\n%s", value, got, want)
		}
	})
}
