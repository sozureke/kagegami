// Package scope says which addresses an engagement is allowed to touch. It is the boundary that
// keeps what a target's own text mentions off the map, and the commands that are suggested inside
// the engagement.
package scope

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// ErrInvalid is wrapped by every error about an entry that cannot be used.
var ErrInvalid = errors.New("invalid scope")

const maxEntries = 256

type entry struct {
	text     string // canonical form
	prefix   netip.Prefix
	from, to netip.Addr // a range; prefix is invalid then
}

func (e entry) contains(a netip.Addr) bool {
	if e.prefix.IsValid() {
		return e.prefix.Contains(a)
	}
	return a.Compare(e.from) >= 0 && a.Compare(e.to) <= 0
}

// Scope is a list of addresses, networks and ranges. The zero value is empty.
type Scope struct {
	entries []entry
}

// Parse reads entries given one by one. Each entry is a single address ("10.0.0.5"), a network in
// CIDR form ("10.0.0.0/24") or a range of addresses of one family ("10.0.0.5-10.0.0.20"). A list that
// would allow every address (0.0.0.0/0, ::/0) is refused: that is no scope.
func Parse(items []string) (Scope, error) {
	var s Scope
	for _, raw := range items {
		for _, part := range split(raw) {
			e, err := parseEntry(part)
			if err != nil {
				return Scope{}, err
			}
			if !slices.ContainsFunc(s.entries, func(x entry) bool { return x.text == e.text }) {
				s.entries = append(s.entries, e)
			}
		}
	}
	if len(s.entries) > maxEntries {
		return Scope{}, fmt.Errorf("%w: %d entries, the limit is %d", ErrInvalid, len(s.entries), maxEntries)
	}
	return s, nil
}

// ParseText reads the form String writes: entries separated by commas, spaces or new lines.
func ParseText(text string) (Scope, error) { return Parse([]string{text}) }

func split(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}

func parseEntry(text string) (entry, error) {
	bad := func(why string) (entry, error) {
		return entry{}, fmt.Errorf("%w: %q %s", ErrInvalid, truncate(text), why)
	}
	if from, to, isRange := strings.Cut(text, "-"); isRange {
		a, errA := netip.ParseAddr(from)
		b, errB := netip.ParseAddr(to)
		if errA != nil || errB != nil || a.Zone() != "" || b.Zone() != "" {
			return bad("is not a range of two addresses")
		}
		a, b = a.Unmap(), b.Unmap()
		if a.Is4() != b.Is4() {
			return bad("mixes IPv4 and IPv6")
		}
		if b.Less(a) {
			return bad("ends before it starts")
		}
		return entry{text: a.String() + "-" + b.String(), from: a, to: b}, nil
	}
	if strings.Contains(text, "/") {
		p, err := netip.ParsePrefix(text)
		if err != nil {
			return bad("is not a network in CIDR form")
		}
		if p.Addr().Is4In6() {
			return bad("is an IPv4 network written as IPv6: write it as IPv4")
		}
		p = p.Masked()
		if p.Bits() == 0 {
			return bad("allows every address, which is no scope")
		}
		return entry{text: p.String(), prefix: p}, nil
	}
	a, err := netip.ParseAddr(text)
	if err != nil {
		return bad("is not an address, a network or a range (names are not accepted)")
	}
	if a.Zone() != "" {
		return bad("has a zone (%eth0): write the address alone")
	}
	a = a.Unmap()
	if a.IsUnspecified() || a.IsMulticast() {
		return bad("is not the address of a host")
	}
	return entry{text: a.String(), prefix: netip.PrefixFrom(a, a.BitLen())}, nil
}

func truncate(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "..."
	}
	return s
}

// IsEmpty reports whether nothing is in scope.
func (s Scope) IsEmpty() bool { return len(s.entries) == 0 }

// Contains reports whether the address is in scope. An address that cannot be read is not.
func (s Scope) Contains(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.WithZone("").Unmap()
	if a.IsUnspecified() || a.IsMulticast() {
		return false // not the address of a host, whatever network is written around it
	}
	return slices.ContainsFunc(s.entries, func(e entry) bool { return e.contains(a) })
}

// Add returns the scope with the entries added.
func (s Scope) Add(items ...string) (Scope, error) {
	more, err := Parse(items)
	if err != nil {
		return Scope{}, err
	}
	return Parse([]string{s.String(), more.String()})
}

// Entries returns the entries in their canonical form.
func (s Scope) Entries() []string {
	out := make([]string, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.text
	}
	return out
}

// String writes the entries separated by commas, the form ParseText reads.
func (s Scope) String() string { return strings.Join(s.Entries(), ", ") }
