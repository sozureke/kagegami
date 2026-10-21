package recon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fake parses lines of the form "host <ip>" for commands that run one program.
type fake struct {
	name    string
	program string
	fail    error
	panics  bool
	bad     bool
	calls   int
}

func (f *fake) Name() string           { return f.name }
func (f *fake) Match(cmd Command) bool { return Runs(cmd.Line, f.program) }
func (f *fake) Parse(_ context.Context, _ Command, output string) (StateDelta, error) {
	f.calls++
	if f.panics {
		panic("hostile output")
	}
	var d StateDelta
	for _, line := range strings.Split(output, "\n") {
		if ip, ok := strings.CutPrefix(line, "host "); ok {
			d.Hosts = append(d.Hosts, HostDelta{IP: ip})
		}
	}
	if f.bad {
		d.Hosts = append(d.Hosts, HostDelta{IP: "not-an-ip"})
	}
	return d, f.fail
}

func TestPrograms(t *testing.T) {
	tests := map[string][]string{
		"nmap -sV 10.0.0.1":                      {"nmap"},
		"sudo nmap -sV 10.0.0.1":                 {"nmap"},
		"sudo -u kali /usr/bin/nmap x":           {"nmap"},
		"sudo -E nmap x":                         {"nmap"},
		`\nmap x`:                                {"nmap"},
		"/opt/tools/ffuf -u http://x/FUZZ":       {"ffuf"},
		"time nmap x":                            {"nmap"},
		"LANG=C nmap x":                          {"nmap"},
		"proxychains4 -q nmap x":                 {"nmap"},
		"env FOO=1 nmap x":                       {"nmap"},
		"cd /tmp && nmap x":                      {"cd", "nmap"},
		"nmap x | tee out.txt":                   {"nmap", "tee"},
		"nmap x; ffuf -u y":                      {"nmap", "ffuf"},
		"nmap x && gobuster dir -u y || echo no": {"nmap", "gobuster", "echo"},
		`echo "nmap; ffuf" | cat`:                {"echo", "cat"},
		"echo 'a && nmap'":                       {"echo"},
		"nmap $(cat targets.txt)":                {"nmap", "cat"}, // the substitution runs too
		"nmap `cat t`":                           {"nmap", "cat"},
		"(cd /tmp; nmap x)":                      {"cd", "nmap"},
		"":                                       nil,
		"   ":                                    nil,
		"A=1":                                    nil,
		"sudo":                                   nil,
	}
	for line, want := range tests {
		if got := Programs(line); !reflect.DeepEqual(got, want) {
			t.Errorf("Programs(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestRunsIsAboutProgramsNotWords(t *testing.T) {
	for line, want := range map[string]bool{
		"nmap x":           true,
		"sudo nmap x":      true,
		"cat nmap.txt":     false,
		"echo nmap":        false,
		"grep nmap log":    false,
		"nmapx y":          false,
		"xnmap y":          false,
		"ls /usr/bin/nmap": false,
	} {
		if got := Runs(line, "nmap"); got != want {
			t.Errorf("Runs(%q, nmap) = %v, want %v", line, got, want)
		}
	}
}

func TestRegistryMatchOrder(t *testing.T) {
	a, b, c := &fake{name: "a", program: "nmap"}, &fake{name: "b", program: "ffuf"}, &fake{name: "c", program: "nmap"}
	r := NewRegistry(a, b, c)
	got := r.Match(Command{Line: "nmap x; ffuf y"})
	if len(got) != 3 || got[0].Name() != "a" || got[1].Name() != "b" || got[2].Name() != "c" {
		t.Errorf("Match = %v, want a, b, c in registry order", got)
	}
	if got := r.Match(Command{Line: "ls"}); len(got) != 0 {
		t.Errorf("Match(ls) = %v", got)
	}
	if got := NewRegistry().Match(Command{Line: "nmap x"}); len(got) != 0 {
		t.Errorf("an empty registry matched: %v", got)
	}
}

func TestRegistryDoesNotShareItsSlice(t *testing.T) {
	list := []Parser{&fake{name: "a", program: "nmap"}}
	r := NewRegistry(list...)
	list[0] = &fake{name: "z", program: "nmap"}
	if got := r.Match(Command{Line: "nmap"}); got[0].Name() != "a" {
		t.Error("the registry was changed from outside")
	}
}

func TestRunMergesAndValidates(t *testing.T) {
	r := NewRegistry(&fake{name: "a", program: "nmap"}, &fake{name: "b", program: "ffuf"})
	res := r.Run(context.Background(), Command{Line: "nmap x; ffuf y"}, "host 10.0.0.1\nnoise\nhost 10.0.0.2\n")
	if res.Err != nil || len(res.Delta.Hosts) != 4 || !reflect.DeepEqual(res.Parsers, []string{"a", "b"}) {
		t.Errorf("Run = %+v", res)
	}
}

func TestRunKeepsGoingAfterAFailure(t *testing.T) {
	boom := errors.New("boom")
	good := &fake{name: "good", program: "nmap"}
	r := NewRegistry(
		&fake{name: "failing", program: "nmap", fail: boom},
		&fake{name: "panicking", program: "nmap", panics: true},
		&fake{name: "invalid", program: "nmap", bad: true},
		good,
	)
	res := r.Run(context.Background(), Command{Line: "nmap x"}, "host 10.0.0.9\n")
	if !errors.Is(res.Err, boom) || !errors.Is(res.Err, ErrInvalid) || !strings.Contains(res.Err.Error(), "panicked") {
		t.Errorf("Err = %v, want all three failures", res.Err)
	}
	for _, name := range []string{"failing:", "panicking:", "invalid:"} {
		if !strings.Contains(res.Err.Error(), name) {
			t.Errorf("the error does not say which parser failed (%s): %v", name, res.Err)
		}
	}
	// the failing parser's partial result counts, the invalid parser's does not, the good one's does
	if len(res.Delta.Hosts) != 2 {
		t.Errorf("hosts = %+v, want the partial result of 'failing' and the result of 'good'", res.Delta.Hosts)
	}
	if good.calls != 1 {
		t.Error("the parser after the failures did not run")
	}
	if len(res.Parsers) != 4 {
		t.Errorf("Parsers = %v", res.Parsers)
	}
}

func TestRunWithNothingToDo(t *testing.T) {
	res := NewRegistry(&fake{name: "a", program: "nmap"}).Run(context.Background(), Command{Line: "ls"}, "host 1.2.3.4")
	if res.Err != nil || !res.Delta.IsEmpty() || len(res.Parsers) != 0 {
		t.Errorf("Run = %+v", res)
	}
}
