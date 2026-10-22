package scope

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseAndContains(t *testing.T) {
	s, err := Parse([]string{"10.10.10.5", "192.168.0.0/24", "172.16.0.10-172.16.0.20", "2001:db8::/32", "fe80::1"})
	if err != nil {
		t.Fatal(err)
	}
	in := []string{
		"10.10.10.5", "192.168.0.0", "192.168.0.255", "192.168.0.77",
		"172.16.0.10", "172.16.0.15", "172.16.0.20", "2001:db8::1", "2001:db8:ffff::2", "fe80::1", "fe80::1%eth0", "::ffff:10.10.10.5", " 10.10.10.5 ",
	}
	out := []string{
		"::%0", "10.10.10.4", "10.10.10.6", "192.168.1.1", "192.167.255.255", "172.16.0.9", "172.16.0.21", "2001:db9::1", "fe80::2",
		"", "junk", "10.10.10", "10.10.10.5.1", "0.0.0.0", "::", "example.com",
	}
	for _, ip := range in {
		if !s.Contains(ip) {
			t.Errorf("%q should be in scope", ip)
		}
	}
	for _, ip := range out {
		if s.Contains(ip) {
			t.Errorf("%q should not be in scope", ip)
		}
	}
}

func TestEntriesAreWrittenCanonically(t *testing.T) {
	s, err := Parse([]string{"10.0.0.1, 10.0.0.1", "10.0.0.7/24 ::ffff:10.1.1.1\n2001:DB8::/32", "10.0.0.5-10.0.0.9", "::ffff:10.0.0.2-::ffff:10.0.0.3"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1", "10.0.0.0/24", "10.1.1.1", "2001:db8::/32", "10.0.0.5-10.0.0.9", "10.0.0.2-10.0.0.3"}
	if got := s.Entries(); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %q, want %q", got, want)
	}
	if s.String() != strings.Join(want, ", ") {
		t.Errorf("String = %q", s.String())
	}
	// the text form reads back to the same scope
	back, err := ParseText(s.String())
	if err != nil || !reflect.DeepEqual(back.Entries(), want) {
		t.Errorf("round trip: %v, %v", back.Entries(), err)
	}
}

func TestParseRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"everything v4":          "0.0.0.0/0",
		"everything v6":          "::/0",
		"a name":                 "box.htb",
		"a url":                  "http://10.0.0.1",
		"a bad network":          "10.0.0.0/33",
		"a bad v6 network":       "2001:db8::/129",
		"a prefix without bits":  "10.0.0.0/",
		"a backwards range":      "10.0.0.9-10.0.0.1",
		"a mixed range":          "10.0.0.1-::1",
		"a half range":           "10.0.0.1-",
		"a short range":          "10.0.0.1-20",
		"a wildcard host":        "0.0.0.0",
		"an unspecified v6":      "::",
		"a multicast":            "224.0.0.1",
		"a mapped network":       "::ffff:10.0.0.0/120",
		"a word":                 "all",
		"a star":                 "10.0.0.*",
		"a zone":                 "fe80::1%eth0",
		"a zone on the wildcard": "::%0",
		"a zone in a range":      "fe80::1%eth0-fe80::9",
	} {
		if _, err := Parse([]string{in}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s (%q): err = %v", name, in, err)
		}
	}
	// one bad entry spoils the list: nothing half-understood is used
	if _, err := Parse([]string{"10.0.0.1", "nope"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v", err)
	}
	var many []string
	for i := range maxEntries + 1 {
		many = append(many, "10.0."+strconv.Itoa(i/250)+"."+strconv.Itoa(i%250+1))
	}
	if _, err := Parse(many); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many entries: %v", err)
	}
}

func TestEmptyScope(t *testing.T) {
	var zero Scope
	if !zero.IsEmpty() || zero.Contains("10.0.0.1") || zero.String() != "" || len(zero.Entries()) != 0 {
		t.Error("the zero scope must be empty and contain nothing")
	}
	for _, in := range [][]string{nil, {}, {""}, {"  ", ",,"}} {
		s, err := Parse(in)
		if err != nil || !s.IsEmpty() {
			t.Errorf("Parse(%q) = %v, %v", in, s, err)
		}
	}
}

func TestAdd(t *testing.T) {
	s, _ := Parse([]string{"10.0.0.1"})
	more, err := s.Add("10.0.0.2", "10.0.0.1")
	if err != nil || !reflect.DeepEqual(more.Entries(), []string{"10.0.0.1", "10.0.0.2"}) {
		t.Errorf("%v, %v", more.Entries(), err)
	}
	if len(s.Entries()) != 1 {
		t.Error("Add changed its receiver")
	}
	if _, err := s.Add("bad"); err == nil {
		t.Error("a bad entry was added")
	}
	e, err := Scope{}.Add("10.1.1.1/24")
	if err != nil || e.String() != "10.1.1.0/24" {
		t.Errorf("%v, %v", e, err)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"10.0.0.0/8", "a-b", "::ffff:1.2.3.4/100", "1.1.1.1-1.1.1.0", "\x00", "10.0.0.1,,"} {
		f.Add(s, "10.0.0.1")
	}
	f.Fuzz(func(t *testing.T, entry, ip string) {
		s, err := Parse([]string{entry})
		if err != nil {
			return
		}
		back, err := ParseText(s.String())
		if err != nil || back.String() != s.String() {
			t.Fatalf("%q -> %q does not read back: %v", entry, s.String(), err)
		}
		if s.Contains(ip) != back.Contains(ip) {
			t.Fatalf("the scope and its text disagree about %q", ip)
		}
		if s.Contains("0.0.0.0") || s.Contains("::") {
			t.Fatalf("%q puts a wildcard address in scope", entry)
		}
	})
}

func TestAWideNetworkDoesNotMakeAWildcardAHost(t *testing.T) {
	s, err := Parse([]string{"0.0.0.0/1", "::/1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"0.0.0.0", "::", "224.0.0.1", "ff02::1"} {
		if s.Contains(ip) {
			t.Errorf("%q is in scope", ip)
		}
	}
	if !s.Contains("10.0.0.1") || !s.Contains("::1") {
		t.Error("the network itself must work")
	}
}
