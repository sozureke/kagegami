package terminal

import (
	"context"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

var defaultIgnore = []string{"cd", "ls", "clear", "pwd"}

func TestJudgeOrderOfReasons(t *testing.T) {
	f := NewFilter(defaultIgnore)
	tests := []struct {
		name string
		cmd  string
		end  End
		size int64
		want Reason
	}{
		{"a normal command", "nmap -sV 10.0.0.1", End{}, 4000, ReasonNone},
		{"a failure with a short message is kept", "curl -s http://10.0.0.1:81", End{ExitCode: 7}, 38, ReasonNone},
		{"a failure with a long message is kept", "nmap bad", End{ExitCode: 1}, 9000, ReasonNone},
		{"exit status 127", "nmapp 10.0.0.1", End{ExitCode: 127}, 40, ReasonNotFound},
		{"exit status 126", "./script", End{ExitCode: 126}, 40, ReasonNotFound},
		{"exit status 128 is a real failure", "git x", End{ExitCode: 128}, 40, ReasonNone},
		{"interrupted with Ctrl-C keeps its output", "nmap -p- x", End{ExitCode: 130}, 700, ReasonNone},
		{"no output", "touch x", End{}, 0, ReasonNoOutput},
		{"no output and a failure", "false", End{ExitCode: 1}, 0, ReasonNoOutput},
		{"unfinished", "sleep 99", End{Aborted: true}, 10, ReasonUnfinished},
		{"full screen", "less f", End{AltScreen: true}, 10, ReasonFullScreen},
		{"ignored", "ls -la", End{}, 500, ReasonIgnored},
		{"unfinished before everything", "ls", End{Aborted: true, AltScreen: true, ExitCode: 127}, 0, ReasonUnfinished},
		{"full screen before ignored", "ls", End{AltScreen: true}, 0, ReasonFullScreen},
		{"not found before ignored", "ls", End{ExitCode: 127}, 0, ReasonNotFound},
		{"ignored before no output", "pwd", End{}, 0, ReasonIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := f.Judge(tt.cmd, tt.end, tt.size); got != tt.want {
				t.Errorf("Judge(%q, %+v, %d) = %q, want %q", tt.cmd, tt.end, tt.size, got, tt.want)
			}
		})
	}
}

func TestJudgeIgnoredCommands(t *testing.T) {
	f := NewFilter(defaultIgnore)
	ignored := []string{
		"ls", "ls -la", "ls   -la   /etc", "pwd", "clear", "cd", "cd /tmp", `cd "/path with spaces"`, "cd '/it'\\''s'",
		"/bin/ls", "/usr/bin/ls -l", `\ls`, "sudo ls", "time ls", "command ls", "builtin cd /", "LANG=C ls", "A=1 B=2 ls",
		"cd /tmp && ls", "cd /tmp; ls; pwd", "ls || pwd", "ls | ls", "ls &", "ls;", "ls ;; ", "ls\npwd",
		"ls > /dev/null", "ls 2>&1", "ls &> out", "ls 2>/dev/null; pwd",
		`ls "a;b"`, `ls 'a;b'`, `ls 'a|b'`, `ls 'a && b'`, `ls a\;b`, `ls "a|b"`,
		"\tls", "ls\t-l",
	}
	for _, cmd := range ignored {
		if got := f.Judge(cmd, End{}, 100); got != ReasonIgnored {
			t.Errorf("Judge(%q) = %q, want %q", cmd, got, ReasonIgnored)
		}
	}

	processed := []string{
		"nmap 10.0.0.1", "cat /etc/passwd", "ls | grep secret", "cd /tmp && nmap x", "ls; nmap x", "ls && curl x", "ls || curl x",
		"lsof -i", "ls2", "lss", "echo ls", "cat ls", "grep cd file", "sudo -u root ls", "sudo nmap x", "time nmap x", "env ls",
		"xargs ls", "bash -c ls", `ls "$(cat x)"`, "ls $(cat x)", "ls `cat x`", "ls $HOME", `ls "$HOME"`, "(ls)", "{ ls; }", "ls <(cat x)",
		"ls 'unbalanced", `ls "unbalanced`, `ls \`, "", "   ", "A=1", ";", "&&", "ls & nmap x", "ls &nmap", "a-b=c ls", "1x=2 ls", "a.b=1 ls", "=x ls",
	}
	for _, cmd := range processed {
		if got := f.Judge(cmd, End{}, 100); got == ReasonIgnored {
			t.Errorf("Judge(%q) = %q: a line that is not certainly trivial must be processed", cmd, got)
		}
	}
}

func TestJudgeWithOtherLists(t *testing.T) {
	t.Run("an empty list ignores nothing", func(t *testing.T) {
		for _, f := range []*Filter{NewFilter(nil), NewFilter([]string{}), NewFilter([]string{"", "  "})} {
			if got := f.Judge("ls", End{}, 10); got != ReasonNone {
				t.Errorf("Judge = %q", got)
			}
		}
	})
	t.Run("entries with arguments", func(t *testing.T) {
		f := NewFilter([]string{"git status", "git  log  --oneline", "  cd  "})
		for cmd, want := range map[string]Reason{
			"git status":             ReasonIgnored,
			"git status -sb":         ReasonIgnored,
			"git log --oneline -5":   ReasonIgnored,
			"git":                    ReasonNone,
			"git push":               ReasonNone,
			"git log":                ReasonNone,
			"git status && git push": ReasonNone,
			"cd /x":                  ReasonIgnored,
			"sudo git status":        ReasonIgnored,
			"/usr/bin/git status":    ReasonIgnored,
		} {
			if got := f.Judge(cmd, End{}, 10); got != want {
				t.Errorf("Judge(%q) = %q, want %q", cmd, got, want)
			}
		}
	})
	t.Run("the list is compared by base name and case-sensitively", func(t *testing.T) {
		f := NewFilter([]string{"LS"})
		if f.Judge("ls", End{}, 10) == ReasonIgnored || f.Judge("LS", End{}, 10) != ReasonIgnored {
			t.Error("case must matter")
		}
	})
}

func TestSplitWords(t *testing.T) {
	tests := map[string][]string{
		"":          nil,
		"a":         {"a"},
		"  a   b  ": {"a", "b"},
		`a "b c" d`: {"a", "b c", "d"},
		`a 'b c' d`: {"a", "b c", "d"},
		`a b\ c`:    {"a", "b c"},
		`"a"b'c'`:   {"abc"},
		`""`:        {""},
		`a "" b`:    {"a", "", "b"},
		`'a"b'`:     {`a"b`},
		`"a'b"`:     {"a'b"},
		`"a\"b"`:    {`a"b`},
		`"a\\b"`:    {`a\b`},
		`"a\xb"`:    {`a\xb`},
		`'a\b'`:     {`a\b`},
		"a\tb":      {"a", "b"},
		`\"`:        {`"`},
	}
	for in, want := range tests {
		got, ok := splitWords(in)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("splitWords(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{`'a`, `"a`, `a\`, `"a\"`} {
		if _, ok := splitWords(in); ok {
			t.Errorf("splitWords(%q) accepted unbalanced input", in)
		}
	}
}

// The word splitter must agree with a real shell on plain text.
func TestSplitWordsAgreesWithTheShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	// only characters that cannot make the shell run, expand or redirect anything
	const alphabet = "ab -\t'\"\\="
	r := rand.New(rand.NewPCG(7, 8))
	for i := range 600 {
		var b strings.Builder
		for range r.IntN(12) {
			b.WriteByte(alphabet[r.IntN(len(alphabet))])
		}
		in := b.String()

		ours, ok := splitWords(in)
		out, err := exec.CommandContext(context.Background(), "sh", "-c", `eval "set -- $1" 2>/dev/null || exit 3; for a; do printf '%s\0' "$a"; done`, "sh", in).Output()
		if err != nil {
			if ok {
				t.Fatalf("case %d (%q): the shell rejects it, splitWords accepted %q", i, in, ours)
			}
			continue
		}
		if !ok && strings.HasSuffix(in, `\`) {
			continue // a shell reads a trailing backslash as a line continuation; the filter refuses to guess
		}
		var theirs []string
		if len(out) > 0 {
			theirs = strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		}
		if !ok || !reflect.DeepEqual(ours, theirs) {
			t.Fatalf("case %d (%q): splitWords = %q (%v), the shell says %q", i, in, ours, ok, theirs)
		}
	}
}

func TestSplitSegments(t *testing.T) {
	tests := map[string][]string{
		"ls":         {"ls"},
		"a; b":       {"a", " b"},
		"a && b":     {"a ", "", " b"},
		"a || b":     {"a ", "", " b"},
		"a | b":      {"a ", " b"},
		`a "x;y" b`:  {`a "x;y" b`},
		`a 'x&&y' b`: {`a 'x&&y' b`},
		`a x\;y b`:   {`a x\;y b`},
		"a\nb":       {"a", "b"},
		"ls 2>&1":    {"ls 2>&1"},
		"ls &>f":     {"ls &>f"},
		"a &":        {"a ", ""},
	}
	for in, want := range tests {
		got, ok := splitSegments(in)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("splitSegments(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"$(x)", "`x`", `"$x"`, `"a` + "`b`" + `"`, "(a)", "{ a; }", "<(a)", "'a", `"a`, `a\`} {
		if _, ok := splitSegments(in); ok {
			t.Errorf("splitSegments(%q) accepted text it cannot read", in)
		}
	}
}

func FuzzJudge(f *testing.F) {
	for _, s := range []string{"ls", "cd /tmp && ls", `ls "$(x)"`, "ls 'a", "\\", "sudo", "A=", "ls | grep x"} {
		f.Add(s)
	}
	filter := NewFilter(append(defaultIgnore, "git status"))
	f.Fuzz(func(t *testing.T, cmd string) {
		got := filter.Judge(cmd, End{}, 10)
		// without any quoting, a substitution or a group cannot be hidden from the filter
		if got == ReasonIgnored && !strings.ContainsAny(cmd, `'"\`) && strings.ContainsAny(cmd, "$`(){}") {
			t.Fatalf("%q was ignored although it contains a substitution or a group", cmd)
		}
		if got == ReasonIgnored {
			// a segment that is not on the list must never hide in an ignored line
			segs, ok := splitSegments(cmd)
			if !ok {
				t.Fatalf("%q was ignored although it cannot be read", cmd)
			}
			for _, seg := range segs {
				words, ok := splitWords(seg)
				if !ok {
					t.Fatalf("%q was ignored with an unreadable part %q", cmd, seg)
				}
				if words = commandWords(words); len(words) > 0 && !filter.matches(words) {
					t.Fatalf("%q was ignored although %q is not on the list", cmd, words)
				}
			}
		}
	})
}

func TestCommandNames(t *testing.T) {
	tests := map[string][]string{
		"ls":                       {"ls"},
		"sudo -u root /bin/ls -la": {"ls"},
		"a | b && c ; d":           {"a", "b", "c", "d"},
		"":                         nil,
	}
	for line, want := range tests {
		if got := CommandNames(line); !reflect.DeepEqual(got, want) {
			t.Errorf("CommandNames(%q) = %q, want %q", line, got, want)
		}
	}
}

func FuzzCommandNames(f *testing.F) {
	for _, s := range []string{"sudo nmap x", `echo "a`, "$(x) | y", "\\", "'"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		for _, name := range CommandNames(line) {
			if name == "" || strings.ContainsAny(name, "/") && name != "/" {
				t.Fatalf("CommandNames(%q) has the name %q", line, name)
			}
		}
	})
}

func TestCommandArgs(t *testing.T) {
	tests := []struct {
		line, program string
		want          []string
	}{
		{"ffuf -u http://x/FUZZ -w w", "ffuf", []string{"-u", "http://x/FUZZ", "-w", "w"}},
		{"sudo -u kali /usr/bin/gobuster dir -q", "gobuster", []string{"dir", "-q"}},
		{"cd /tmp && ffuf -s -u 'http://a b/FUZZ'", "ffuf", []string{"-s", "-u", "http://a b/FUZZ"}},
		{"ffuf -a | tee out; ffuf -b", "ffuf", []string{"-a"}},
		{"echo ffuf -x", "ffuf", nil},
		{"ffuf", "ffuf", []string{}},
		{"ls", "ffuf", nil},
		{"", "ffuf", nil},
	}
	for _, tt := range tests {
		got := CommandArgs(tt.line, tt.program)
		if len(got) == 0 && len(tt.want) == 0 && (got == nil) == (tt.want == nil) {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("CommandArgs(%q, %q) = %q, want %q", tt.line, tt.program, got, tt.want)
		}
	}
}
