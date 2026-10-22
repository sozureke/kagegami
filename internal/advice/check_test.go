package advice

import (
	"errors"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/scope"
)

func ctx(t *testing.T) Context {
	t.Helper()
	sc, err := scope.Parse([]string{"10.0.0.0/24", "192.168.1.5", "2001:db8::/64"})
	if err != nil {
		t.Fatal(err)
	}
	return Context{
		Scope:     sc,
		Hosts:     map[string]bool{"10.0.0.5": true, "10.0.0.6": true, "172.16.0.9": true},
		Hostnames: map[string]string{"box.htb": "10.0.0.5", "outside.htb": "172.16.0.9"},
		Services:  map[string]bool{"10.0.0.5:22/tcp": true, "10.0.0.5:8000/tcp": true, "172.16.0.9:80/tcp": true},
	}
}

func TestCheckCommandAccepts(t *testing.T) {
	c := ctx(t)
	for _, cmd := range []string{
		"nmap -sV -p- 10.0.0.5",
		"nmap -sV 10.0.0.0/24",
		"nmap -sV 10.0.0.0/25",
		"gobuster dir -u http://10.0.0.5:8000 -w /usr/share/wordlists/dirb/common.txt",
		"gobuster dir -u http://box.htb:8000/ -w common.txt",
		"ffuf -u http://box.htb/FUZZ -w /usr/share/seclists/Discovery/Web-Content/raft-small-words.txt -o out.json",
		"curl -s 'http://10.0.0.5:8000/?a=1;b=2'",
		"nmap -sC 10.0.0.5 | tee scan.txt",
		"nmap -sV 10.0.0.5 > scan.txt",
		"nmap -sV 10.0.0.5 &> scan.txt",
		"nmap -sV 10.0.0.5 2>&1",
		`curl -H "Host: box.htb" http://10.0.0.5/`,
		"smbclient -L //10.0.0.5 -N",
		"nmap -6 2001:db8::5",
		"curl http://[2001:db8::5]:80/",
		"ssh-audit 10.0.0.5",
		"ssh -i key.pem user@10.0.0.5",
		"nmap -sV 192.168.1.5",
		"echo 'a && b; c `d` $(e)'",
		"dig @10.0.0.5 box.htb axfr",
		"nmap --script http-enum -p 80 localhost-ish",
	} {
		if err := c.CheckCommand(cmd); err != nil {
			t.Errorf("%q refused: %v", cmd, err)
		}
	}
}

func TestCheckCommandRefuses(t *testing.T) {
	c := ctx(t)
	tests := []struct {
		cmd  string
		kind Kind
	}{
		{"nmap -sV 10.0.1.5", OutOfScope},
		{"nmap -sV 10.0.0.5 10.0.1.5", OutOfScope},
		{"nmap -sV 10.0.0.0/16", OutOfScope},
		{"nmap -sV 10.0.0.0/23", OutOfScope},
		{"nmap 0.0.0.0/0", OutOfScope},
		{"nmap -sV 999.1.1.1", OutOfScope},
		{"curl http://8.8.8.8/", OutOfScope},
		{"curl http://[2001:db9::1]/", OutOfScope},
		{"nmap -6 2001:db9::5", OutOfScope},
		{"nmap 192.168.1.6", OutOfScope},
		{"gobuster dir -u http://outside.htb", OutOfScope},
		{"nmap unknown.example.com", UnknownHost},
		{"curl http://evil.example/ -x 10.0.0.5", UnknownHost},
		{"curl http://intranet/", UnknownHost},
		{"nmap -sV 10.0.0.5; nmap 10.0.0.6", Unsafe},
		{"nmap 10.0.0.5 && curl http://10.0.0.5", Unsafe},
		{"nmap 10.0.0.5 || true", Unsafe},
		{"nmap 10.0.0.5 &", Unsafe},
		{"nmap 10.0.0.5\nrm -rf /", Unsafe},
		{"nmap 10.0.0.5\rrm", Unsafe},
		{"nmap `cat targets`", Unsafe},
		{"nmap $(cat targets)", Unsafe},
		{`nmap "$(cat targets)"`, Unsafe},
		{"diff <(nmap 10.0.0.5) <(nmap 10.0.0.6)", Unsafe},
		{"nmap 10.0.0.5 >(cat)", Unsafe},
		{"nmap '10.0.0.5", Unsafe},
		{`nmap "10.0.0.5`, Unsafe},
		{"nmap 10.0.0.5\x00", Unsafe},
		{"", Unsafe},
		{"   ", Unsafe},
	}
	for _, tt := range tests {
		err := c.CheckCommand(tt.cmd)
		var r *Refusal
		if !errors.As(err, &r) || r.Kind != tt.kind {
			t.Errorf("%q: err = %v, want %s", tt.cmd, err, tt.kind)
		}
	}
}

func TestNothingIsOfferedWithoutAScope(t *testing.T) {
	for _, c := range []Context{{}, {Scope: scope.Scope{}}} {
		if err := c.CheckCommand("nmap 10.0.0.5"); !errors.Is(err, ErrNoScope) {
			t.Errorf("err = %v", err)
		}
		if err := c.CheckSuggestion(Suggestion{NeedsRecon: true}); !errors.Is(err, ErrNoScope) {
			t.Errorf("err = %v", err)
		}
		if _, _, err := c.CheckPipeline(Pipeline{NeedsRecon: true}); !errors.Is(err, ErrNoScope) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestCheckSuggestion(t *testing.T) {
	c := ctx(t)
	if err := c.CheckSuggestion(Suggestion{NeedsRecon: true, Rationale: "r"}); err != nil {
		t.Errorf("a plea for more recon needs no command: %v", err)
	}
	if err := c.CheckSuggestion(Suggestion{Command: "nmap 10.0.0.5"}); err != nil {
		t.Error(err)
	}
	if err := c.CheckSuggestion(Suggestion{Command: "nmap 8.8.8.8"}); err == nil {
		t.Error("an out-of-scope suggestion was accepted")
	}
}

func TestCheckPipeline(t *testing.T) {
	c := ctx(t)
	p := Pipeline{Summary: "s", Steps: []Step{
		{Priority: 1, Service: "10.0.0.5:8000/tcp http", Vector: "web", Why: "w", Commands: []string{"gobuster dir -u http://10.0.0.5:8000 -w w.txt", "gobuster dir -u http://8.8.8.8", "nikto -h 10.0.0.5; id"}, Confidence: "high"},
		{Priority: 2, Service: "10.0.0.5:9999/tcp", Vector: "invented", Why: "w", Commands: []string{"nmap 10.0.0.5"}, Confidence: "high"},
		{Priority: 3, Service: "10.0.0.9", Vector: "unknown host", Why: "w", Commands: []string{"nmap 10.0.0.9"}, Confidence: "high"},
		{Priority: 4, Service: "172.16.0.9:80/tcp", Vector: "outside", Why: "w", Commands: []string{"nmap 172.16.0.9"}, Confidence: "high"},
		{Priority: 5, Service: "box.htb:22", Vector: "ssh by name", Why: "w", Commands: []string{"ssh-audit 10.0.0.5"}, Confidence: "medium"},
		{Priority: 6, Service: "somewhere", Vector: "no address", Why: "w", Commands: []string{}, Confidence: "low"},
		{Priority: 7, Service: "10.0.0.6", Vector: "only bad commands", Why: "w", Commands: []string{"nmap 1.1.1.1"}, Confidence: "high"},
	}}
	got, dropped, err := c.CheckPipeline(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("steps = %+v", got.Steps)
	}
	if got.Steps[0].Vector != "web" || len(got.Steps[0].Commands) != 1 || got.Steps[0].Confidence != "high" {
		t.Errorf("step 1 = %+v: only the good command stays", got.Steps[0])
	}
	if got.Steps[1].Vector != "ssh by name" || got.Steps[1].Priority != 2 {
		t.Errorf("step 2 = %+v: a service given by a host name on the map is fine, and priorities are renumbered", got.Steps[1])
	}
	if got.Steps[2].Vector != "only bad commands" || got.Steps[2].Confidence != "low" || got.Steps[2].Commands == nil || len(got.Steps[2].Commands) != 0 || got.Steps[2].Priority != 3 {
		t.Errorf("step 3 = %+v: a step that lost all its commands stays as low confidence advice", got.Steps[2])
	}
	kinds := map[Kind]int{}
	for _, d := range dropped {
		kinds[d.Kind]++
	}
	if kinds[OutOfScope] != 3 || kinds[Unsafe] != 1 || kinds[Ungrounded] != 3 {
		t.Errorf("dropped = %v", dropped)
	}
	if len(p.Steps) != 7 || len(p.Steps[0].Commands) != 3 {
		t.Error("CheckPipeline changed its input")
	}
}

func TestAPipelineWithNothingLeftIsAPleaForRecon(t *testing.T) {
	c := ctx(t)
	got, dropped, err := c.CheckPipeline(Pipeline{Summary: "s", Steps: []Step{{Priority: 1, Service: "8.8.8.8", Vector: "v", Why: "w", Commands: []string{"x"}, Confidence: "high"}}})
	if err != nil || !got.NeedsRecon || len(got.Steps) != 0 {
		t.Errorf("%+v, %v", got, err)
	}
	if len(dropped) != 2 || !strings.Contains(dropped[1].Detail, "too thin") {
		t.Errorf("dropped = %v", dropped)
	}
	// a plea that was a plea stays one without noise
	got, dropped, _ = c.CheckPipeline(Pipeline{Summary: "s", NeedsRecon: true})
	if !got.NeedsRecon || len(dropped) != 0 {
		t.Errorf("%+v, %v", got, dropped)
	}
}

func TestAddressesAndHostNames(t *testing.T) {
	tests := []struct {
		cmd       string
		addrs     []string
		hostnames []string
	}{
		{"nmap 10.0.0.5", []string{"10.0.0.5"}, nil},
		{"nmap 10.0.0.5/24 10.0.0.6", []string{"10.0.0.5/24", "10.0.0.6"}, nil},
		{"curl http://10.0.0.5:8080/a/1.2.3.4", []string{"10.0.0.5", "1.2.3.4"}, nil}, // whatever looks like an address counts: the safe side
		{"tool --version 1.10.0.0.1", nil, nil},
		{"tool 999.1.1.1", []string{"999.1.1.1"}, nil},
		{"nmap -6 ::1 fe80::1", []string{"::1", "fe80::1"}, nil},
		{"curl http://[2001:db8::5]:80/", []string{"2001:db8::5"}, nil},
		{"curl http://box.htb/x", nil, []string{"box.htb"}},
		{"nmap box.htb -oN out.txt scan.xml", nil, []string{"box.htb"}},
		{"ffuf -u https://Sub.Box.HTB:8443/FUZZ -w /usr/share/wordlists/x.txt", nil, []string{"sub.box.htb"}},
		{"ssh user@host.example.org", nil, []string{"host.example.org"}},
		{"curl http://localhost/", nil, []string{"localhost"}},
		{"cat notes.md config.php v1.2 192.168.1.5.bak", []string{"192.168.1.5"}, nil},
		{"echo 10:30:00 12:00", nil, nil},
	}
	for _, tt := range tests {
		if got := addresses(tt.cmd); !equalSets(got, tt.addrs) {
			t.Errorf("addresses(%q) = %q, want %q", tt.cmd, got, tt.addrs)
		}
		if got := hostNames(tt.cmd); !equalSets(got, tt.hostnames) {
			t.Errorf("hostNames(%q) = %q, want %q", tt.cmd, got, tt.hostnames)
		}
	}
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			found = found || x == y
		}
		if !found {
			return false
		}
	}
	return true
}

func FuzzCheckCommand(f *testing.F) {
	for _, s := range []string{"nmap 10.0.0.5", "curl 'a;b'", "x $(y)", "\"", "10.0.0.5/99", "http://[::1", "a@b.c.d"} {
		f.Add(s)
	}
	c := Context{Scope: mustScope("10.0.0.0/24"), Hosts: map[string]bool{"10.0.0.5": true}, Hostnames: map[string]string{"box.htb": "10.0.0.5"}}
	f.Fuzz(func(t *testing.T, cmd string) {
		err := c.CheckCommand(cmd)
		if err != nil {
			return
		}
		// whatever is accepted names no address outside the scope
		for _, a := range addresses(cmd) {
			if !c.inScope(a) {
				t.Fatalf("%q was accepted but names %q", cmd, a)
			}
		}
		if chained(cmd) != "" {
			t.Fatalf("%q was accepted but is chained", cmd)
		}
	})
}

func mustScope(entries ...string) scope.Scope {
	s, err := scope.Parse(entries)
	if err != nil {
		panic(err)
	}
	return s
}
