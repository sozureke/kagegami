package recon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"
)

// StateDelta is a change to the map of the target: what a command taught Kagegami. It is the
// one format in which parsers report, and in which the model (tier 1) is asked to answer.
//
// Entries are identified by natural keys (an address, a port and a protocol), never by
// database ids, so a delta can be built anywhere and applied later. Credentials are not part
// of it: a secret is never taken from output automatically.
//
// A delta may come from a model, so it is untrusted: Validate it before use.
type StateDelta struct {
	Hosts    []HostDelta    `json:"hosts,omitempty"`
	Services []ServiceDelta `json:"services,omitempty"`
	WebPaths []WebPathDelta `json:"web_paths,omitempty"`
	Findings []FindingDelta `json:"findings,omitempty"`
	Notes    []NoteDelta    `json:"notes,omitempty"`
}

// HostDelta describes a host. Empty fields mean "nothing new about this".
type HostDelta struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Status   string `json:"status,omitempty"` // up or down
}

// ServiceDelta describes a service on a host.
type ServiceDelta struct {
	HostIP  string `json:"host_ip"`
	Port    int    `json:"port"`
	Proto   string `json:"proto"` // tcp or udp
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	State   string `json:"state,omitempty"` // open, closed or filtered
}

// WebPathDelta describes a path found on a web service. The host is given by its address, or,
// when only a name is known (a scan of http://box.htb/), by Host: the path is then stored if
// the map already has a host with that name.
type WebPathDelta struct {
	HostIP     string `json:"host_ip,omitempty"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port"`
	Proto      string `json:"proto"`
	Path       string `json:"path"`
	StatusCode *int   `json:"status_code,omitempty"`
	Note       string `json:"note,omitempty"`
}

// FindingDelta is something worth reporting.
type FindingDelta struct {
	Severity    string `json:"severity"` // info, low, medium, high or critical
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// NoteDelta is a remark that belongs in the journal.
type NoteDelta struct {
	Text string `json:"text"`
}

// Limits on a delta. They keep a hostile or confused source from filling the map.
const (
	MaxEntries     = 20000
	maxShortText   = 512
	maxLongText    = 8 << 10
	maxPathLen     = 2048
	maxHostnameLen = 253
)

var (
	statuses   = []string{"up", "down"}
	protocols  = []string{"tcp", "udp"}
	states     = []string{"open", "closed", "filtered", "open|filtered", "closed|filtered", "unfiltered"}
	severities = []string{"info", "low", "medium", "high", "critical"}
)

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid state delta")

// errNotAHost marks an address that is well formed but cannot be the address of a host that
// is tested: the wildcard 0.0.0.0 and ::, multicast, the broadcast address.
var errNotAHost = errors.New("not the address of a host")

// IsEmpty reports whether the delta changes nothing.
func (d StateDelta) IsEmpty() bool {
	return len(d.Hosts)+len(d.Services)+len(d.WebPaths)+len(d.Findings)+len(d.Notes) == 0
}

// Merge returns the delta with the entries of both, those of d first.
func (d StateDelta) Merge(o StateDelta) StateDelta {
	return StateDelta{
		Hosts:    slices.Concat(d.Hosts, o.Hosts),
		Services: slices.Concat(d.Services, o.Services),
		WebPaths: slices.Concat(d.WebPaths, o.WebPaths),
		Findings: slices.Concat(d.Findings, o.Findings),
		Notes:    slices.Concat(d.Notes, o.Notes),
	}
}

// Validate checks the delta and returns a normalized copy: addresses in their canonical
// form, protocols and states in lower case, text trimmed. The first problem found is
// reported, with the entry it concerns.
func (d StateDelta) Validate() (StateDelta, error) {
	if n := len(d.Hosts) + len(d.Services) + len(d.WebPaths) + len(d.Findings) + len(d.Notes); n > MaxEntries {
		return StateDelta{}, fmt.Errorf("%w: %d entries, the limit is %d", ErrInvalid, n, MaxEntries)
	}
	var out StateDelta
	for i, h := range d.Hosts {
		ip, err := address(h.IP)
		if err != nil {
			return StateDelta{}, fmt.Errorf("%w: host %d: %w", ErrInvalid, i, err)
		}
		h.IP = ip
		h.Hostname = strings.ToLower(strings.TrimSpace(h.Hostname))
		h.OS = strings.TrimSpace(h.OS)
		h.Status = strings.ToLower(strings.TrimSpace(h.Status))
		if err := firstError(
			size("hostname", h.Hostname, maxHostnameLen), size("os", h.OS, maxShortText),
			oneOf("status", h.Status, statuses, true),
		); err != nil {
			return StateDelta{}, fmt.Errorf("%w: host %d: %w", ErrInvalid, i, err)
		}
		out.Hosts = append(out.Hosts, h)
	}
	for i, s := range d.Services {
		ip, err := address(s.HostIP)
		if err == nil {
			s.HostIP = ip
			s.Proto = strings.ToLower(strings.TrimSpace(s.Proto))
			s.Name = strings.TrimSpace(s.Name)
			s.Version = strings.TrimSpace(s.Version)
			s.State = strings.ToLower(strings.TrimSpace(s.State))
			err = firstError(
				portError(s.Port), oneOf("proto", s.Proto, protocols, false),
				size("name", s.Name, maxShortText), size("version", s.Version, maxShortText),
				oneOf("state", s.State, states, true),
			)
		}
		if err != nil {
			return StateDelta{}, fmt.Errorf("%w: service %d: %w", ErrInvalid, i, err)
		}
		out.Services = append(out.Services, s)
	}
	for i, w := range d.WebPaths {
		var err error
		w.Host = strings.ToLower(strings.TrimSpace(w.Host))
		switch {
		case w.HostIP != "" && w.Host != "":
			err = errors.New("give the host as an address or as a name, not both")
		case w.HostIP == "":
			err = firstError(required("host", w.Host), size("host", w.Host, maxHostnameLen))
		default:
			w.HostIP, err = address(w.HostIP)
		}
		if err == nil {
			w.Proto = strings.ToLower(strings.TrimSpace(w.Proto))
			w.Path = strings.TrimSpace(w.Path)
			w.Note = strings.TrimSpace(w.Note)
			err = firstError(
				portError(w.Port), oneOf("proto", w.Proto, protocols, false), pathError(w.Path),
				statusCodeError(w.StatusCode), size("note", w.Note, maxShortText),
			)
		}
		if err != nil {
			return StateDelta{}, fmt.Errorf("%w: web path %d: %w", ErrInvalid, i, err)
		}
		out.WebPaths = append(out.WebPaths, w)
	}
	for i, f := range d.Findings {
		f.Severity = strings.ToLower(strings.TrimSpace(f.Severity))
		f.Title = strings.TrimSpace(f.Title)
		f.Description = strings.TrimSpace(f.Description)
		if err := firstError(
			oneOf("severity", f.Severity, severities, false), required("title", f.Title),
			size("title", f.Title, maxShortText), size("description", f.Description, maxLongText),
		); err != nil {
			return StateDelta{}, fmt.Errorf("%w: finding %d: %w", ErrInvalid, i, err)
		}
		out.Findings = append(out.Findings, f)
	}
	for i, n := range d.Notes {
		n.Text = strings.TrimSpace(n.Text)
		if err := firstError(required("text", n.Text), size("text", n.Text, maxLongText)); err != nil {
			return StateDelta{}, fmt.Errorf("%w: note %d: %w", ErrInvalid, i, err)
		}
		out.Notes = append(out.Notes, n)
	}
	return out, nil
}

// ParseDelta reads a delta from JSON, as a model returns it: unknown fields, trailing text
// and invalid values are errors. The result is validated and normalized.
func ParseDelta(data []byte) (StateDelta, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d StateDelta
	if err := dec.Decode(&d); err != nil {
		return StateDelta{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return StateDelta{}, fmt.Errorf("%w: text after the JSON value", ErrInvalid)
	}
	return d.Validate()
}

func address(s string) (string, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("address %q is not an IP address", truncate(s))
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsMulticast() || addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return "", fmt.Errorf("%w: %s", errNotAHost, addr)
	}
	return addr.String(), nil
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

func portError(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d is out of range", p)
	}
	return nil
}

func statusCodeError(c *int) error {
	if c != nil && (*c < 100 || *c > 599) {
		return fmt.Errorf("status code %d is out of range", *c)
	}
	return nil
}

func pathError(p string) error {
	switch {
	case !strings.HasPrefix(p, "/"):
		return fmt.Errorf("path %q must start with /", truncate(p))
	case len(p) > maxPathLen:
		return fmt.Errorf("path is %d bytes long, the limit is %d", len(p), maxPathLen)
	case strings.ContainsAny(p, "\x00\r\n"):
		return errors.New("path contains a control character")
	}
	return nil
}

func oneOf(field, v string, allowed []string, optional bool) error {
	if v == "" && optional {
		return nil
	}
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%s %q is not one of %v", field, truncate(v), allowed)
	}
	return nil
}

func required(field, v string) error {
	if v == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	return nil
}

func size(field, v string, limit int) error {
	if len(v) > limit {
		return fmt.Errorf("%s is %d bytes long, the limit is %d", field, len(v), limit)
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// DropKind says why an entry of a delta was left out. The kinds are different things: an entry
// that is not well formed is noise, one that is well formed but is not what is wanted is a
// decision, and some (an address outside the scope) are worth telling the person about.
type DropKind string

// The reasons an entry is dropped.
const (
	// Malformed: the entry is not valid (an empty path, a port out of range, a word where an address belongs).
	Malformed DropKind = "malformed"
	// NotAHost: the address is well formed but is not a host (0.0.0.0, ::, multicast, broadcast).
	NotAHost DropKind = "not a host"
	// Ungrounded: nothing in the command or the output supports the entry.
	Ungrounded DropKind = "ungrounded"
	// OutOfScope: the address is not one that the engagement tests.
	OutOfScope DropKind = "outside the scope"
	// Policy: the source may not provide this kind of entry (findings from a model).
	Policy DropKind = "not allowed from this source"
)

// Drop is one entry that was left out of a delta.
type Drop struct {
	Kind   DropKind
	Detail string
}

func (d Drop) String() string { return string(d.Kind) + ": " + d.Detail }

// Sanitize keeps the entries of the delta that are valid and reports the others. A delta from a
// model is not rejected as a whole for one bad line: models fill a list with an empty entry, or
// write an address that is not one. Each entry is checked alone and normalized.
func (d StateDelta) Sanitize() (StateDelta, []Drop) {
	var out StateDelta
	var dropped []Drop
	keep := func(kind string, i int, single StateDelta, add func(StateDelta)) {
		checked, err := single.Validate()
		if err != nil {
			k := Malformed
			if errors.Is(err, errNotAHost) {
				k = NotAHost
			}
			dropped = append(dropped, Drop{Kind: k, Detail: fmt.Sprintf("%s %d: %v", kind, i, err)})
			return
		}
		add(checked)
	}
	for i, h := range d.Hosts {
		keep("host", i, StateDelta{Hosts: []HostDelta{h}}, func(c StateDelta) { out.Hosts = append(out.Hosts, c.Hosts...) })
	}
	for i, s := range d.Services {
		keep("service", i, StateDelta{Services: []ServiceDelta{s}}, func(c StateDelta) { out.Services = append(out.Services, c.Services...) })
	}
	for i, w := range d.WebPaths {
		keep("web path", i, StateDelta{WebPaths: []WebPathDelta{w}}, func(c StateDelta) { out.WebPaths = append(out.WebPaths, c.WebPaths...) })
	}
	for i, f := range d.Findings {
		keep("finding", i, StateDelta{Findings: []FindingDelta{f}}, func(c StateDelta) { out.Findings = append(out.Findings, c.Findings...) })
	}
	for i, n := range d.Notes {
		keep("note", i, StateDelta{Notes: []NoteDelta{n}}, func(c StateDelta) { out.Notes = append(out.Notes, c.Notes...) })
	}
	return out, dropped
}

// ParseDeltaLenient reads a delta from JSON like ParseDelta (unknown fields, text after the value
// and broken JSON are errors) but drops invalid entries instead of failing, and says which.
func ParseDeltaLenient(data []byte) (StateDelta, []Drop, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d StateDelta
	if err := dec.Decode(&d); err != nil {
		return StateDelta{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return StateDelta{}, nil, fmt.Errorf("%w: text after the JSON value", ErrInvalid)
	}
	if n := len(d.Hosts) + len(d.Services) + len(d.WebPaths) + len(d.Findings) + len(d.Notes); n > MaxEntries {
		return StateDelta{}, nil, fmt.Errorf("%w: %d entries, the limit is %d", ErrInvalid, n, MaxEntries)
	}
	out, dropped := d.Sanitize()
	return out, dropped, nil
}
