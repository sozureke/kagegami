package recon

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ptr(n int) *int { return &n }

func TestValidateNormalizes(t *testing.T) {
	in := StateDelta{
		Hosts:    []HostDelta{{IP: " 10.0.0.1 ", Hostname: " Box.HTB ", OS: " Linux ", Status: "UP"}, {IP: "::ffff:10.0.0.2"}, {IP: "FE80::1"}},
		Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "TCP", Name: " ssh ", Version: " OpenSSH 8.2 ", State: "Open"}},
		WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: " /admin ", StatusCode: ptr(301), Note: " redirect "}},
		Findings: []FindingDelta{{Severity: "HIGH", Title: " Anonymous FTP ", Description: " allowed "}},
		Notes:    []NoteDelta{{Text: " robots.txt lists /secret "}},
	}
	got, err := in.Validate()
	if err != nil {
		t.Fatal(err)
	}
	want := StateDelta{
		Hosts:    []HostDelta{{IP: "10.0.0.1", Hostname: "box.htb", OS: "Linux", Status: "up"}, {IP: "10.0.0.2"}, {IP: "fe80::1"}},
		Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp", Name: "ssh", Version: "OpenSSH 8.2", State: "open"}},
		WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/admin", StatusCode: ptr(301), Note: "redirect"}},
		Findings: []FindingDelta{{Severity: "high", Title: "Anonymous FTP", Description: "allowed"}},
		Notes:    []NoteDelta{{Text: "robots.txt lists /secret"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalized delta:\n got %+v\nwant %+v", got, want)
	}
}

func TestValidateRejects(t *testing.T) {
	long := strings.Repeat("x", maxShortText+1)
	tests := map[string]StateDelta{
		"host without address":        {Hosts: []HostDelta{{}}},
		"host with a name as address": {Hosts: []HostDelta{{IP: "example.com"}}},
		"host with a CIDR":            {Hosts: []HostDelta{{IP: "10.0.0.0/24"}}},
		"host with a bad status":      {Hosts: []HostDelta{{IP: "10.0.0.1", Status: "maybe"}}},
		"host with a long hostname":   {Hosts: []HostDelta{{IP: "10.0.0.1", Hostname: strings.Repeat("a", 254)}}},
		"host with a long OS":         {Hosts: []HostDelta{{IP: "10.0.0.1", OS: long}}},
		"service port 0":              {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 0, Proto: "tcp"}}},
		"service port 65536":          {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 65536, Proto: "tcp"}}},
		"service negative port":       {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: -1, Proto: "tcp"}}},
		"service without protocol":    {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22}}},
		"service with sctp":           {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "sctp"}}},
		"service with a bad state":    {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp", State: "listening"}}},
		"service with a bad host":     {Services: []ServiceDelta{{HostIP: "nope", Port: 22, Proto: "tcp"}}},
		"service with a long banner":  {Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp", Version: long}}},
		"path without a slash":        {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "admin"}}},
		"empty path":                  {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: ""}}},
		"path with a newline":         {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a\nb"}}},
		"path too long":               {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/" + strings.Repeat("a", maxPathLen)}}},
		"status code 99":              {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/", StatusCode: ptr(99)}}},
		"status code 600":             {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/", StatusCode: ptr(600)}}},
		"web path with a bad port":    {WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 0, Proto: "tcp", Path: "/"}}},
		"finding without a title":     {Findings: []FindingDelta{{Severity: "low", Title: "  "}}},
		"finding with a bad severity": {Findings: []FindingDelta{{Severity: "urgent", Title: "x"}}},
		"finding without a severity":  {Findings: []FindingDelta{{Title: "x"}}},
		"finding with a long title":   {Findings: []FindingDelta{{Severity: "low", Title: long}}},
		"empty note":                  {Notes: []NoteDelta{{Text: " "}}},
		"note too long":               {Notes: []NoteDelta{{Text: strings.Repeat("n", maxLongText+1)}}},
	}
	for name, d := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := d.Validate(); !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
	t.Run("too many entries", func(t *testing.T) {
		var d StateDelta
		for range MaxEntries + 1 {
			d.Notes = append(d.Notes, NoteDelta{Text: "x"})
		}
		if _, err := d.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate = %v", err)
		}
	})
	t.Run("the limit itself is fine", func(t *testing.T) {
		var d StateDelta
		for range MaxEntries {
			d.Notes = append(d.Notes, NoteDelta{Text: "x"})
		}
		if _, err := d.Validate(); err != nil {
			t.Errorf("Validate = %v", err)
		}
	})
}

func TestValidateAcceptsTheEdges(t *testing.T) {
	d := StateDelta{
		Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 1, Proto: "udp"}, {HostIP: "10.0.0.1", Port: 65535, Proto: "tcp", State: "open|filtered"}},
		WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/", StatusCode: ptr(100)}, {HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/" + strings.Repeat("a", maxPathLen-1), StatusCode: ptr(599)}},
		Hosts:    []HostDelta{{IP: "10.0.0.1", Hostname: strings.Repeat("a", maxHostnameLen)}},
	}
	if _, err := d.Validate(); err != nil {
		t.Error(err)
	}
}

func TestValidateDoesNotChangeTheOriginal(t *testing.T) {
	d := StateDelta{Hosts: []HostDelta{{IP: " 10.0.0.1 "}}}
	if _, err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Hosts[0].IP != " 10.0.0.1 " {
		t.Error("Validate changed its receiver")
	}
}

func TestMergeAndIsEmpty(t *testing.T) {
	a := StateDelta{Hosts: []HostDelta{{IP: "10.0.0.1"}}, Notes: []NoteDelta{{Text: "a"}}}
	b := StateDelta{Hosts: []HostDelta{{IP: "10.0.0.2"}}, Services: []ServiceDelta{{HostIP: "10.0.0.2", Port: 22, Proto: "tcp"}}}
	m := a.Merge(b)
	if len(m.Hosts) != 2 || m.Hosts[0].IP != "10.0.0.1" || len(m.Services) != 1 || len(m.Notes) != 1 {
		t.Errorf("merged = %+v", m)
	}
	if len(a.Hosts) != 1 || len(b.Hosts) != 1 {
		t.Error("Merge changed its inputs")
	}
	if (StateDelta{}).Merge(StateDelta{}).IsEmpty() != true || a.IsEmpty() {
		t.Error("IsEmpty is wrong")
	}
	for _, d := range []StateDelta{{Hosts: []HostDelta{{}}}, {Services: []ServiceDelta{{}}}, {WebPaths: []WebPathDelta{{}}}, {Findings: []FindingDelta{{}}}, {Notes: []NoteDelta{{}}}} {
		if d.IsEmpty() {
			t.Errorf("%+v reported empty", d)
		}
	}
}

func TestParseDelta(t *testing.T) {
	t.Run("a good document", func(t *testing.T) {
		d, err := ParseDelta([]byte(` {"hosts":[{"ip":"10.0.0.1","status":"UP"}],"services":[{"host_ip":"10.0.0.1","port":22,"proto":"tcp","name":"ssh"}]} `))
		if err != nil || len(d.Hosts) != 1 || d.Hosts[0].Status != "up" || d.Services[0].Name != "ssh" {
			t.Errorf("ParseDelta = %+v, %v", d, err)
		}
	})
	t.Run("an empty object", func(t *testing.T) {
		if d, err := ParseDelta([]byte(`{}`)); err != nil || !d.IsEmpty() {
			t.Errorf("ParseDelta = %+v, %v", d, err)
		}
	})
	bad := map[string]string{
		"unknown field":     `{"hosts":[],"cmd":"rm -rf /"}`,
		"unknown nested":    `{"hosts":[{"ip":"10.0.0.1","password":"x"}]}`,
		"credentials field": `{"credentials":[{"user":"a","secret":"b"}]}`,
		"trailing text":     `{} extra`,
		"two documents":     `{}{}`,
		"not an object":     `[]`,
		"a string":          `"x"`,
		"truncated":         `{"hosts":[`,
		"empty input":       ``,
		"wrong type":        `{"services":[{"host_ip":"10.0.0.1","port":"22","proto":"tcp"}]}`,
		"invalid value":     `{"hosts":[{"ip":"not-an-ip"}]}`,
		"port as float":     `{"services":[{"host_ip":"10.0.0.1","port":22.5,"proto":"tcp"}]}`,
		"null in a list":    `{"hosts":[null]}`,
	}
	for name, doc := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDelta([]byte(doc)); !errors.Is(err, ErrInvalid) {
				t.Errorf("ParseDelta(%s) error = %v, want ErrInvalid", doc, err)
			}
		})
	}
}

// The JSON names are a contract with the model; they must not change by accident.
func TestDeltaJSONNames(t *testing.T) {
	d := StateDelta{
		Hosts:    []HostDelta{{IP: "1", Hostname: "h", OS: "o", Status: "s"}},
		Services: []ServiceDelta{{HostIP: "1", Port: 2, Proto: "p", Name: "n", Version: "v", State: "s"}},
		WebPaths: []WebPathDelta{{HostIP: "1", Port: 2, Proto: "p", Path: "/", StatusCode: ptr(200), Note: "n"}},
		Findings: []FindingDelta{{Severity: "s", Title: "t", Description: "d"}},
		Notes:    []NoteDelta{{Text: "t"}},
	}
	data, _ := json.Marshal(d)
	for _, name := range []string{
		`"hosts"`, `"services"`, `"web_paths"`, `"findings"`, `"notes"`, `"ip"`, `"hostname"`, `"os"`, `"status"`,
		`"host_ip"`, `"port"`, `"proto"`, `"name"`, `"version"`, `"state"`, `"path"`, `"status_code"`, `"note"`, `"severity"`, `"title"`, `"description"`, `"text"`,
	} {
		if !strings.Contains(string(data), name) {
			t.Errorf("%s is missing from %s", name, data)
		}
	}
}

func FuzzParseDelta(f *testing.F) {
	for _, s := range []string{`{}`, `{"hosts":[{"ip":"10.0.0.1"}]}`, `{"services":[{"host_ip":"::1","port":1,"proto":"udp"}]}`, `[`, `{"hosts":[null]}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := ParseDelta(data)
		if err != nil {
			return
		}
		again, err := d.Validate()
		if err != nil || !reflect.DeepEqual(d, again) {
			t.Fatalf("a validated delta must validate to itself: %v", err)
		}
	})
}

func TestWebPathHostByNameOrAddress(t *testing.T) {
	ok := []WebPathDelta{
		{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/"},
		{Host: " Box.HTB ", Port: 80, Proto: "tcp", Path: "/"},
	}
	got, err := StateDelta{WebPaths: ok}.Validate()
	if err != nil || got.WebPaths[1].Host != "box.htb" {
		t.Fatalf("Validate = %+v, %v", got.WebPaths, err)
	}
	for name, w := range map[string]WebPathDelta{
		"neither":       {Port: 80, Proto: "tcp", Path: "/"},
		"both":          {HostIP: "10.0.0.1", Host: "box.htb", Port: 80, Proto: "tcp", Path: "/"},
		"a bad address": {HostIP: "x", Port: 80, Proto: "tcp", Path: "/"},
		"a long name":   {Host: strings.Repeat("a", 254), Port: 80, Proto: "tcp", Path: "/"},
	} {
		if _, err := (StateDelta{WebPaths: []WebPathDelta{w}}).Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSanitizeDropsOnlyTheBadEntries(t *testing.T) {
	d := StateDelta{
		Hosts:    []HostDelta{{IP: " 10.0.0.1 "}, {IP: "[::]"}, {IP: ""}},
		Services: []ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "TCP"}, {HostIP: "10.0.0.1", Port: 0, Proto: "tcp"}},
		WebPaths: []WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: ""}, {HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a"}},
		Findings: []FindingDelta{{Severity: "nope", Title: "x"}, {Severity: "low", Title: "ok"}},
		Notes:    []NoteDelta{{Text: " "}, {Text: "n"}},
	}
	got, dropped := d.Sanitize()
	if len(got.Hosts) != 1 || got.Hosts[0].IP != "10.0.0.1" || len(got.Services) != 1 || got.Services[0].Proto != "tcp" || len(got.WebPaths) != 1 || len(got.Findings) != 1 || len(got.Notes) != 1 {
		t.Errorf("kept = %+v", got)
	}
	if len(dropped) != 6 {
		t.Errorf("dropped = %v", dropped)
	}
	for _, dr := range dropped {
		if dr.Kind != Malformed {
			t.Errorf("%v: expected malformed", dr)
		}
	}
	if len(d.Hosts) != 3 || d.Hosts[0].IP != " 10.0.0.1 " {
		t.Error("Sanitize changed its receiver")
	}
	if _, dropped := (StateDelta{}).Sanitize(); len(dropped) != 0 {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestParseDeltaLenient(t *testing.T) {
	d, dropped, err := ParseDeltaLenient([]byte(`{"hosts":[{"ip":"10.0.0.1"},{"ip":"junk"}],"services":[{"host_ip":"10.0.0.1","port":22,"proto":"tcp"}]}`))
	if err != nil || len(d.Hosts) != 1 || len(d.Services) != 1 || len(dropped) != 1 {
		t.Errorf("%+v, %v, %v", d, dropped, err)
	}
	for _, doc := range []string{`{"hosts":[`, `{"credentials":[]}`, `{} x`, `[]`, ``, `{"hosts":[{"ip":"10.0.0.1","password":"x"}]}`} {
		if _, _, err := ParseDeltaLenient([]byte(doc)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", doc, err)
		}
	}
	var big StateDelta
	for range MaxEntries + 1 {
		big.Notes = append(big.Notes, NoteDelta{Text: "x"})
	}
	data, _ := json.Marshal(big)
	if _, _, err := ParseDeltaLenient(data); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many entries: %v", err)
	}
}

func TestSanitizeTellsAWildcardFromANonsense(t *testing.T) {
	d := StateDelta{Hosts: []HostDelta{{IP: "0.0.0.0"}, {IP: "::"}, {IP: "[::]"}, {IP: "224.0.0.251"}, {IP: "ff02::1"}, {IP: "255.255.255.255"}, {IP: "::ffff:0.0.0.0"}, {IP: "10.0.0.1"}}}
	got, dropped := d.Sanitize()
	if len(got.Hosts) != 1 || got.Hosts[0].IP != "10.0.0.1" {
		t.Errorf("hosts = %+v", got.Hosts)
	}
	kinds := map[DropKind]int{}
	for _, dr := range dropped {
		kinds[dr.Kind]++
	}
	if kinds[NotAHost] != 6 || kinds[Malformed] != 1 {
		t.Errorf("kinds = %v (%v)", kinds, dropped)
	}
	// a service on a wildcard address is dropped as well
	if _, dropped := (StateDelta{Services: []ServiceDelta{{HostIP: "0.0.0.0", Port: 22, Proto: "tcp"}}}).Sanitize(); len(dropped) != 1 || dropped[0].Kind != NotAHost {
		t.Errorf("dropped = %v", dropped)
	}
	if _, err := (StateDelta{Hosts: []HostDelta{{IP: "0.0.0.0"}}}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("Validate must refuse a wildcard host too: %v", err)
	}
	if got := (Drop{Kind: Ungrounded, Detail: "x"}).String(); got != "ungrounded: x" {
		t.Errorf("String = %q", got)
	}
}
