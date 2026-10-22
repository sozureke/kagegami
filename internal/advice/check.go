package advice

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrNoScope is returned when the engagement has no scope: nothing can be suggested, because
// nothing is known to be allowed.
var ErrNoScope = errors.New("no scope is set")

// Kind says why a command or a step was refused.
type Kind string

// The reasons.
const (
	// Unsafe: the command chains other commands, substitutes or runs in the background.
	Unsafe Kind = "unsafe command"
	// OutOfScope: the command names an address that is not in scope.
	OutOfScope Kind = "outside the scope"
	// UnknownHost: the command names a host that is not on the map, so its address is not known.
	UnknownHost Kind = "host not on the map"
	// Ungrounded: the step is about a service that is not on the map.
	Ungrounded Kind = "not on the map"
)

// Refusal is the reason a command was not accepted.
type Refusal struct {
	Kind   Kind
	Detail string
}

func (r *Refusal) Error() string { return string(r.Kind) + ": " + r.Detail }

// Scope is the part of a scope the checks need.
type Scope interface {
	Contains(ip string) bool
	IsEmpty() bool
}

// Context is what the checks know: the scope and what is on the map.
type Context struct {
	Scope Scope
	// Hosts are the addresses of the hosts on the map.
	Hosts map[string]bool
	// Hostnames map a lower case host name on the map to its address.
	Hostnames map[string]string
	// Services are the services on the map, written <address>:<port>/<proto>.
	Services map[string]bool
}

// CheckCommand says whether a command may be offered as advice: it is one command (a pipe and a
// redirection are fine), it names no address outside the scope, and every host name in it is a host
// of the map whose address is in scope. It returns nil, or a *Refusal, or ErrNoScope.
func (c Context) CheckCommand(cmd string) error {
	if c.Scope == nil || c.Scope.IsEmpty() {
		return ErrNoScope
	}
	if strings.TrimSpace(cmd) == "" {
		return &Refusal{Kind: Unsafe, Detail: "an empty command"}
	}
	if why := chained(cmd); why != "" {
		return &Refusal{Kind: Unsafe, Detail: why}
	}
	for _, a := range addresses(cmd) {
		if !c.inScope(a) {
			return &Refusal{Kind: OutOfScope, Detail: a}
		}
	}
	for _, h := range hostNames(cmd) {
		ip, ok := c.Hostnames[strings.ToLower(h)]
		switch {
		case !ok:
			return &Refusal{Kind: UnknownHost, Detail: h}
		case !c.Scope.Contains(ip):
			return &Refusal{Kind: OutOfScope, Detail: h + " (" + ip + ")"}
		}
	}
	return nil
}

// inScope checks an address or a network written in a command. A network is accepted if its first
// and its last address are in scope.
func (c Context) inScope(a string) bool {
	if p, err := netip.ParsePrefix(a); err == nil {
		first := p.Masked().Addr()
		last := lastAddr(p)
		return c.Scope.Contains(first.String()) && c.Scope.Contains(last.String())
	}
	return c.Scope.Contains(a)
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

// chained finds what makes a line more than one command: a new line, a separator, a
// substitution or a background job, outside single quotes (inside them nothing has a meaning).
func chained(cmd string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\\' && !inSingle:
			i++
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle:
		case ch == '\n' || ch == '\r' || ch == 0:
			return "more than one line"
		case ch == '`':
			return "command substitution"
		case ch == '$' && i+1 < len(cmd) && cmd[i+1] == '(':
			return "command substitution"
		case inDouble:
		case ch == ';':
			return "several commands (;)"
		case ch == '&':
			if i+1 < len(cmd) && cmd[i+1] == '>' || i > 0 && cmd[i-1] == '>' {
				continue // &> and >&: a redirection
			}
			return "several commands or a background job (&)"
		case ch == '|' && i+1 < len(cmd) && cmd[i+1] == '|':
			return "several commands (||)"
		case (ch == '<' || ch == '>') && i+1 < len(cmd) && cmd[i+1] == '(':
			return "process substitution"
		}
	}
	if inSingle || inDouble {
		return "an unclosed quote"
	}
	return ""
}

var (
	ipv4Candidate = regexp.MustCompile(`\d{1,3}(?:\.\d{1,3}){3}(?:/\d{1,2})?`)
	ipv6Candidate = regexp.MustCompile(`[0-9A-Fa-f:]*:[0-9A-Fa-f:]*:[0-9A-Fa-f:.]*(?:/\d{1,3})?`)
	hostRE        = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)
	splitter      = regexp.MustCompile(`[\s'"=,@]+`)
)

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// addresses finds the IP addresses and networks written in a command. A group of four numbers
// that is not an address (999.1.1.1) is returned as well: it cannot be in scope, so the command
// is refused, which is the safe side.
func addresses(cmd string) []string {
	var out []string
	add := func(a string) {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	for _, loc := range ipv4Candidate.FindAllStringIndex(cmd, -1) {
		start, end := loc[0], loc[1]
		if start > 0 && (isDigit(cmd[start-1]) || cmd[start-1] == '.') {
			continue // the tail of something longer, such as a version 1.10.0.0.1
		}
		if end < len(cmd) && (isDigit(cmd[end]) || (cmd[end] == '.' && end+1 < len(cmd) && isDigit(cmd[end+1]))) {
			continue
		}
		add(cmd[start:end])
	}
	for _, loc := range ipv6Candidate.FindAllStringIndex(cmd, -1) {
		cand := strings.Trim(cmd[loc[0]:loc[1]], ".")
		if _, err := netip.ParseAddr(strings.SplitN(cand, "/", 2)[0]); err == nil {
			add(cand)
		} else if p, err := netip.ParsePrefix(cand); err == nil {
			add(p.String())
		}
	}
	return out
}

// fileSuffixes are endings of names that are files, not hosts.
var fileSuffixes = []string{
	"txt", "xml", "json", "csv", "html", "htm", "php", "js", "py", "sh", "conf", "log", "out", "lst", "list", "gz", "zip", "tar",
	"pdf", "md", "yaml", "yml", "cfg", "pcap", "bin", "exe", "jpg", "jpeg", "png", "gif", "db", "sql", "pem", "key", "crt", "ini",
	"nse", "rb", "pl", "bak", "old", "tmp", "dat", "gnmap", "nmap", "git", "env", "cap", "hccapx", "rule", "rules", "dic", "dict",
}

// hostNames finds the host names a command names: the host of every URL, and every dotted word
// that looks like a name and not like a file.
func hostNames(cmd string) []string {
	var out []string
	add := func(h string) {
		h = strings.TrimRight(strings.ToLower(h), ".")
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	for _, tok := range splitter.Split(cmd, -1) {
		if i := strings.Index(tok, "://"); i > 0 {
			if u, err := url.Parse(tok); err == nil && u.Hostname() != "" {
				if _, err := netip.ParseAddr(u.Hostname()); err != nil {
					add(u.Hostname())
				}
			}
			continue
		}
		tok = strings.Trim(tok, "()[]{}<>;:")
		if host, _, ok := strings.Cut(tok, "/"); ok {
			tok = host
		}
		if host, port, ok := strings.Cut(tok, ":"); ok {
			if _, err := strconv.Atoi(port); err == nil {
				tok = host
			}
		}
		if !hostRE.MatchString(tok) {
			continue
		}
		if _, err := netip.ParseAddr(tok); err == nil {
			continue
		}
		suffix := tok[strings.LastIndexByte(tok, '.')+1:]
		if slices.Contains(fileSuffixes, strings.ToLower(suffix)) || len(suffix) < 2 || strings.IndexFunc(suffix, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
			continue
		}
		add(tok)
	}
	return out
}

// Drop is a part of an answer that was left out.
type Drop struct {
	Kind   Kind
	Detail string
}

func (d Drop) String() string { return string(d.Kind) + ": " + d.Detail }

// CheckSuggestion accepts the suggestion, or says why not. A suggestion that only says the map is too
// thin needs no check of a command.
func (c Context) CheckSuggestion(s Suggestion) error {
	if c.Scope == nil || c.Scope.IsEmpty() {
		return ErrNoScope
	}
	if s.NeedsRecon {
		return nil
	}
	return c.CheckCommand(s.Command)
}

// CheckPipeline removes from a pipeline what cannot be offered: a step about a service that is
// not on the map or not in scope, and every command that fails CheckCommand. A step that loses all
// its commands stays, as advice with low confidence and no command, and the priorities are
// numbered again from 1.
func (c Context) CheckPipeline(p Pipeline) (Pipeline, []Drop, error) {
	if c.Scope == nil || c.Scope.IsEmpty() {
		return Pipeline{}, nil, ErrNoScope
	}
	out := Pipeline{Summary: p.Summary, NeedsRecon: p.NeedsRecon}
	var dropped []Drop
	for _, st := range p.Steps {
		if why := c.checkService(st.Service); why != nil {
			dropped = append(dropped, Drop{Kind: why.Kind, Detail: fmt.Sprintf("step %q: %s", st.Vector, why.Detail)})
			continue
		}
		var kept []string
		for _, cmd := range st.Commands {
			if err := c.CheckCommand(cmd); err != nil {
				var r *Refusal
				kind, detail := Unsafe, err.Error()
				if errors.As(err, &r) {
					kind, detail = r.Kind, r.Detail
				}
				dropped = append(dropped, Drop{Kind: kind, Detail: detail})
				continue
			}
			kept = append(kept, cmd)
		}
		if len(kept) == 0 && len(st.Commands) > 0 {
			st.Confidence = "low"
		}
		st.Commands = kept
		if st.Commands == nil {
			st.Commands = []string{}
		}
		out.Steps = append(out.Steps, st)
	}
	for i := range out.Steps {
		out.Steps[i].Priority = i + 1
	}
	if !out.NeedsRecon && len(out.Steps) == 0 {
		out.NeedsRecon = true
		dropped = append(dropped, Drop{Kind: Ungrounded, Detail: "no step was left, so the map is treated as too thin"})
	}
	return out, dropped, nil
}

// checkService checks that a step's service is on the map and in scope.
func (c Context) checkService(service string) *Refusal {
	word, _, _ := strings.Cut(service, " ")
	host, rest := word, ""
	switch {
	case strings.HasPrefix(word, "["):
		end := strings.Index(word, "]")
		if end < 0 {
			return &Refusal{Kind: Ungrounded, Detail: fmt.Sprintf("%q is not <address>:<port>/<proto>", truncate(service))}
		}
		host, rest = word[1:end], strings.TrimPrefix(word[end+1:], ":")
	case strings.Count(word, ":") == 1:
		host, rest, _ = strings.Cut(word, ":")
	}
	if ip, ok := c.Hostnames[strings.ToLower(host)]; ok {
		host = ip
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return &Refusal{Kind: Ungrounded, Detail: fmt.Sprintf("%q does not start with an address on the map", truncate(service))}
	}
	if !c.Hosts[host] {
		return &Refusal{Kind: Ungrounded, Detail: host + " is not on the map"}
	}
	if !c.Scope.Contains(host) {
		return &Refusal{Kind: OutOfScope, Detail: host}
	}
	if rest != "" {
		key := host + ":" + rest
		if !strings.Contains(rest, "/") {
			key += "/tcp"
		}
		if !c.Services[key] {
			return &Refusal{Kind: Ungrounded, Detail: key + " is not on the map"}
		}
	}
	return nil
}
