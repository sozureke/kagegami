// Package agent runs the event loop: filter, tier-1/tier-2 orchestration.
package agent

import (
	"context"
	"crypto/rand"
	_ "embed" // the prompts are part of the binary
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/pkg/llm"
)

//go:embed prompts/tier1.system.txt
var tier1System string

//go:embed prompts/tier1.example.input.txt
var tier1ExampleInput string

//go:embed prompts/tier1.example.output.json
var tier1ExampleOutput string

// Tier1Options configure the first tier.
type Tier1Options struct {
	// Head and Tail are how many bytes of the start and of the end of the output the model sees.
	Head, Tail int
	// FewShot adds a worked example to the request. Whether it helps is measured, not assumed.
	FewShot bool
	// MaxTokens limits the answer.
	MaxTokens int
}

// DefaultTier1Options are the starting values (a hypothesis to be measured, see the plan).
func DefaultTier1Options() Tier1Options {
	return Tier1Options{Head: 6 << 10, Tail: 2 << 10, MaxTokens: 1500}
}

// Tier1 reads the output of a command with a model and returns what it found as a state delta.
type Tier1 struct {
	Provider llm.Provider
	Options  Tier1Options
	// Random supplies the bytes of the boundary; nil means crypto/rand.
	Random func([]byte) error
	// InScope says whether an address may be put on the map. The model reads text written by
	// the target, which can name any address; the scope of the engagement is what keeps an
	// address that nobody is testing off the map. Nil allows every address.
	InScope func(ip string) bool
}

// ErrUnparseable means the model did not return a delta that can be used, also on the second try.
var ErrUnparseable = errors.New("the model's answer could not be used")

// Tier1Result is what the model found.
type Tier1Result struct {
	// Delta holds only entries that passed the checks.
	Delta recon.StateDelta
	// Dropped says what was left out and why. Everything belongs in the debug log; ScopeNote
	// turns the entries about addresses outside the scope into a note for the person.
	Dropped []recon.Drop
	Usage   llm.Usage
}

const (
	maxCommand = 1000
	maxNotes   = 5
	maxNote    = 300
	attempts   = 2
)

// Extract asks the model what the output says. A refusal or an unavailable model comes back as
// the error of the provider. An answer that is not a valid delta is asked for once more.
func (t *Tier1) Extract(ctx context.Context, command string, exitCode int, output string) (Tier1Result, error) {
	opts := t.Options
	if opts.Head <= 0 && opts.Tail <= 0 {
		def := DefaultTier1Options()
		opts.Head, opts.Tail = def.Head, def.Tail
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = DefaultTier1Options().MaxTokens
	}
	shown := output
	if len(shown) > opts.Head+opts.Tail {
		// Fold the runs of near-identical lines before cutting: the line that matters in a long
		// log is rarely in the first or the last few kilobytes, but it is not like its neighbours.
		shown = headTail(collapse(shown), opts.Head, opts.Tail)
	}
	cmd := cleanCommand(command)

	var res Tier1Result
	var lastErr error
	for range attempts {
		req, err := t.request(cmd, exitCode, shown, opts)
		if err != nil {
			return res, err
		}
		resp, err := t.Provider.Complete(ctx, req)
		res.Usage = res.Usage.Add(resp.Usage)
		if err != nil {
			return res, err
		}
		delta, invalid, perr := recon.ParseDeltaLenient([]byte(extractJSON(resp.Text)))
		if perr != nil {
			lastErr = perr
			continue
		}
		grounded, dropped := ground(delta, cmd+"\n"+shown)
		scoped, outside := t.scope(grounded)
		res.Delta = scoped
		res.Dropped = slices.Concat(invalid, dropped, outside)
		return res, nil
	}
	return res, fmt.Errorf("%w: %w", ErrUnparseable, lastErr)
}

// scope removes what is about an address outside the scope.
func (t *Tier1) scope(d recon.StateDelta) (recon.StateDelta, []recon.Drop) {
	if t.InScope == nil {
		return d, nil
	}
	var out recon.StateDelta
	var dropped []recon.Drop
	for _, h := range d.Hosts {
		if t.InScope(h.IP) {
			out.Hosts = append(out.Hosts, h)
		} else {
			dropped = append(dropped, recon.Drop{Kind: recon.OutOfScope, Detail: h.IP})
		}
	}
	for _, s := range d.Services {
		if t.InScope(s.HostIP) {
			out.Services = append(out.Services, s)
		} else {
			dropped = append(dropped, recon.Drop{Kind: recon.OutOfScope, Detail: s.HostIP})
		}
	}
	for _, w := range d.WebPaths {
		if w.HostIP == "" || t.InScope(w.HostIP) {
			out.WebPaths = append(out.WebPaths, w)
		} else {
			dropped = append(dropped, recon.Drop{Kind: recon.OutOfScope, Detail: w.HostIP})
		}
	}
	out.Findings, out.Notes = d.Findings, d.Notes
	return out, dropped
}

// request builds the request. The boundary is new for every request and is not found in the
// text it surrounds, so nothing in the output can close the block it sits in.
func (t *Tier1) request(command string, exitCode int, shown string, opts Tier1Options) (llm.Request, error) {
	schema, err := recon.ModelSchema()
	if err != nil {
		return llm.Request{}, err
	}
	zero := 0.0
	req := llm.Request{System: strings.TrimSpace(tier1System), Schema: schema, MaxTokens: opts.MaxTokens, Temperature: &zero}

	if opts.FewShot {
		b, err := t.boundary(tier1ExampleInput)
		if err != nil {
			return llm.Request{}, err
		}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.User, Content: userMessage("nmap -sV -sC web.lab", 0, tier1ExampleInput, b)},
			llm.Message{Role: llm.Assistant, Content: strings.TrimSpace(tier1ExampleOutput)},
		)
	}
	b, err := t.boundary(command + shown)
	if err != nil {
		return llm.Request{}, err
	}
	req.Messages = append(req.Messages, llm.Message{Role: llm.User, Content: userMessage(command, exitCode, shown, b)})
	return req, nil
}

func userMessage(command string, exitCode int, output, boundary string) string {
	return fmt.Sprintf("<command>%s</command>\n<exit_code>%d</exit_code>\n<output boundary=%q>\n%s\n</output boundary=%q>",
		command, exitCode, boundary, strings.TrimRight(output, "\n"), boundary)
}

// boundary returns a random token that does not occur in text.
func (t *Tier1) boundary(text string) (string, error) {
	fill := t.Random
	if fill == nil {
		fill = func(b []byte) error { _, err := rand.Read(b); return err }
	}
	for range 8 {
		raw := make([]byte, 12)
		if err := fill(raw); err != nil {
			return "", fmt.Errorf("cannot make a boundary: %w", err)
		}
		if token := hex.EncodeToString(raw); !strings.Contains(text, token) {
			return token, nil
		}
	}
	return "", errors.New("cannot make a boundary that is not in the text")
}

// headTail keeps the first head and the last tail bytes of s, cut at character boundaries, and
// says how much was left out.
func headTail(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	h, rest := cutHead(s, head)
	mid := len(rest)
	t := cutTail(rest, tail)
	return h + fmt.Sprintf("\n[... %d bytes omitted ...]\n", mid-len(t)) + t
}

func cutHead(s string, n int) (head, rest string) {
	if n <= 0 {
		return "", s
	}
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], s[n:]
}

func cutTail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// cleanCommand makes the command one safe line for the prompt.
func cleanCommand(c string) string {
	c = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, c)
	c = strings.NewReplacer("<", "‹", ">", "›").Replace(strings.TrimSpace(c))
	if len(c) > maxCommand {
		c, _ = cutHead(c, maxCommand)
		c += "..."
	}
	return c
}

// extractJSON returns the JSON object in a model's text. A model that was told to answer with
// JSON only still sometimes wraps it in a code fence or a sentence; this takes the outermost
// braces and leaves the validation to ParseDelta.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

// ground removes what the text does not support. A model can invent a host, a port or a path, or be
// led to; whatever it reports must be there in the command or the output. Findings are not taken
// from a model at all.
func ground(d recon.StateDelta, text string) (recon.StateDelta, []recon.Drop) {
	var out recon.StateDelta
	var dropped []recon.Drop
	drop := func(kind recon.DropKind, format string, a ...any) {
		dropped = append(dropped, recon.Drop{Kind: kind, Detail: fmt.Sprintf(format, a...)})
	}
	has := func(s string) bool { return s != "" && strings.Contains(text, s) }

	if len(d.Findings) > 0 {
		drop(recon.Policy, "%d findings (findings are not taken from a model)", len(d.Findings))
	}
	for _, h := range d.Hosts {
		if !has(h.IP) {
			drop(recon.Ungrounded, "host %s (not in the text)", h.IP)
			continue
		}
		if h.Hostname != "" && !containsFold(text, h.Hostname) {
			h.Hostname = ""
		}
		if h.OS != "" && !containsFold(text, h.OS) {
			h.OS = ""
		}
		out.Hosts = append(out.Hosts, h)
	}
	for _, s := range d.Services {
		switch {
		case !has(s.HostIP):
			drop(recon.Ungrounded, "service %s %d/%s (host not in the text)", s.HostIP, s.Port, s.Proto)
			continue
		case !hasNumber(text, s.Port):
			drop(recon.Ungrounded, "service %s %d/%s (port not in the text)", s.HostIP, s.Port, s.Proto)
			continue
		}
		if s.Name != "" && !containsFold(text, s.Name) {
			s.Name = ""
		}
		if s.Version != "" && !has(s.Version) {
			s.Version = ""
		}
		out.Services = append(out.Services, s)
	}
	for _, w := range d.WebPaths {
		switch {
		case w.HostIP != "" && !has(w.HostIP), w.Host != "" && !containsFold(text, w.Host):
			drop(recon.Ungrounded, "path %s (host not in the text)", w.Path)
			continue
		case !has(w.Path):
			drop(recon.Ungrounded, "path %s (not in the text)", w.Path)
			continue
		case !hasNumber(text, w.Port):
			drop(recon.Ungrounded, "path %s (port not in the text)", w.Path)
			continue
		}
		out.WebPaths = append(out.WebPaths, w)
	}
	for _, n := range d.Notes {
		if len(out.Notes) >= maxNotes {
			drop(recon.Policy, "note %q (more than %d notes)", short(n.Text), maxNotes)
			continue
		}
		if r := []rune(n.Text); len(r) > maxNote {
			n.Text = string(r[:maxNote]) + "..."
		}
		out.Notes = append(out.Notes, n)
	}
	return out, dropped
}

func short(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "..."
	}
	return s
}

func containsFold(text, s string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(s))
}

// hasNumber reports whether the number occurs in the text as a number of its own: 22 in "22/tcp"
// and "port 22", not in "2222" or "1.22".
func hasNumber(text string, n int) bool {
	re := regexp.MustCompile(`(^|[^0-9.])` + strconv.Itoa(n) + `([^0-9]|$)`)
	return re.MatchString(text)
}

// maxScopeNote is how many addresses a scope note names.
const maxScopeNote = 5

// ScopeNote makes a note for the journal when the output named addresses outside the scope and
// they were left out. An address that the target's own text points at, and nobody is testing, is
// something a person wants to know about; the entries themselves never reach the map.
func (r Tier1Result) ScopeNote(command string) (recon.NoteDelta, bool) {
	var addrs []string
	for _, d := range r.Dropped {
		if d.Kind == recon.OutOfScope && !slices.Contains(addrs, d.Detail) {
			addrs = append(addrs, d.Detail)
		}
	}
	if len(addrs) == 0 {
		return recon.NoteDelta{}, false
	}
	more := ""
	if len(addrs) > maxScopeNote {
		more = fmt.Sprintf(" and %d more", len(addrs)-maxScopeNote)
		addrs = addrs[:maxScopeNote]
	}
	return recon.NoteDelta{Text: fmt.Sprintf("the output of %q names addresses outside the scope, left off the map: %s%s", short(command), strings.Join(addrs, ", "), more)}, true
}
