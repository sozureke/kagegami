package terminal

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const nonce = "0123456789abcdef0123456789abcdef"

func start(id int, cmd string) string {
	return fmt.Sprintf("\x1b]7771;C;kg=%s;id=%d;cmd=%s\a", nonce, id, cmd)
}

func end(id, rc int) string {
	return fmt.Sprintf("\x1b]7771;D;kg=%s;id=%d;rc=%d\a", nonce, id, rc)
}

// rec records what the parser reports. Output is merged into one entry per command, so the
// way the stream was cut into chunks cannot show in the result.
type rec struct {
	events []string
	out    *bytes.Buffer
}

func newRec() *rec { return &rec{} }

func (r *rec) flush() {
	if r.out != nil && r.out.Len() > 0 {
		r.events = append(r.events, fmt.Sprintf("out %q", r.out.String()))
	}
	r.out = nil
}

func (r *rec) Started(id int, cmd string) {
	r.flush()
	r.events = append(r.events, fmt.Sprintf("start %d %q", id, cmd))
	r.out = &bytes.Buffer{}
}

func (r *rec) Output(data []byte) {
	if r.out == nil {
		r.out = &bytes.Buffer{}
	}
	r.out.Write(data)
}

func (r *rec) Ended(id int, e End) {
	r.flush()
	r.events = append(r.events, fmt.Sprintf("end %d rc=%d aborted=%v alt=%v", id, e.ExitCode, e.Aborted, e.AltScreen))
}

// parseAll feeds the stream in chunks of the given sizes (cycled) and closes the parser.
func parseAll(stream string, sizes ...int) []string {
	r := newRec()
	p := NewParser(nonce, r)
	data := []byte(stream)
	for i := 0; len(data) > 0; i++ {
		n := min(sizes[i%len(sizes)], len(data))
		_, _ = p.Write(data[:n])
		data = data[n:]
	}
	p.Close()
	r.flush()
	return r.events
}

func whole(stream string) []string { return parseAll(stream, len(stream)+1) }

func TestParserBasics(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		want   []string
	}{
		{
			"one command", start(1, "echo hi") + "hi\r\n" + end(1, 0),
			[]string{`start 1 "echo hi"`, `out "hi\r\n"`, `end 1 rc=0 aborted=false alt=false`},
		},
		{
			"no output", start(1, "false") + end(1, 1),
			[]string{`start 1 "false"`, `end 1 rc=1 aborted=false alt=false`},
		},
		{
			"text outside commands is dropped", "prompt% " + start(1, "x") + "o" + end(1, 0) + "prompt% ",
			[]string{`start 1 "x"`, `out "o"`, `end 1 rc=0 aborted=false alt=false`},
		},
		{
			"two commands", start(1, "a") + "A" + end(1, 0) + "% " + start(2, "b") + "B" + end(2, 3),
			[]string{`start 1 "a"`, `out "A"`, `end 1 rc=0 aborted=false alt=false`, `start 2 "b"`, `out "B"`, `end 2 rc=3 aborted=false alt=false`},
		},
		{
			"the command text may hold ; and =", start(1, "echo a;b=c;id=9") + end(1, 0),
			[]string{`start 1 "echo a;b=c;id=9"`, `end 1 rc=0 aborted=false alt=false`},
		},
		{
			"empty command text", start(1, "") + end(1, 0),
			[]string{`start 1 ""`, `end 1 rc=0 aborted=false alt=false`},
		},
		{
			"exit code 255", start(1, "x") + end(1, 255),
			[]string{`start 1 "x"`, `end 1 rc=255 aborted=false alt=false`},
		},
		{
			"a start while one is open aborts the first", start(1, "a") + "A" + start(2, "b") + "B" + end(2, 0),
			[]string{`start 1 "a"`, `out "A"`, `end 1 rc=0 aborted=true alt=false`, `start 2 "b"`, `out "B"`, `end 2 rc=0 aborted=false alt=false`},
		},
		{
			"the stream ends inside a command", start(1, "sleep 99") + "partial",
			[]string{`start 1 "sleep 99"`, `out "partial"`, `end 1 rc=0 aborted=true alt=false`},
		},
		{
			"colour sequences and CR stay in the output", start(1, "x") + "\x1b[31mred\x1b[0m\rprogress\r\n" + end(1, 0),
			[]string{`start 1 "x"`, `out "\x1b[31mred\x1b[0m\rprogress\r\n"`, `end 1 rc=0 aborted=false alt=false`},
		},
		{
			"NUL and invalid UTF-8", start(1, "x") + "a\x00b\xff\xfe" + end(1, 0),
			[]string{`start 1 "x"`, `out "a\x00b\xff\xfe"`, `end 1 rc=0 aborted=false alt=false`},
		},
		{"an empty stream", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := whole(tt.stream); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("events:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// Output of a command is untrusted: bytes that look like markers but lack the nonce are
// ordinary output (ADR-0006).
func TestParserIgnoresForgedMarkers(t *testing.T) {
	forged := []string{
		"\x1b]7771;D;kg=ffffffffffffffffffffffffffffffff;id=1;rc=0\a",              // wrong nonce
		"\x1b]7771;D;id=1;rc=0\a",                                                  // no nonce
		"\x1b]7771;C;kg=" + nonce[:31] + ";id=9;cmd=x\a",                           // nonce too short
		"\x1b]7771;C;kg=" + nonce + "0;id=9;cmd=x\a",                               // nonce too long
		"\x1b]7771;C;kg=" + strings.ToUpper(nonce) + ";id=9;cmd=x\a",               // other case
		"\x1b]7771;C;kg=" + nonce[:8] + strings.Repeat("f", 24) + ";id=9;cmd=x\a",  // right start of the nonce only
		"\x1b]7771;C;kg=" + strings.Repeat("f", 24) + nonce[24:] + ";id=9;cmd=x\a", // right end of the nonce only
		"\x1b]7771;X;kg=" + nonce + ";id=1;cmd=x\a",                                // unknown kind, otherwise well formed
		"\x1b]133;D;0\a", // another protocol
		"\x1b]7771;X;kg=" + nonce + ";id=1;rc=0\a",                        // unknown kind
		"\x1b]7771;C;kg=" + nonce + ";id=0;cmd=x\a",                       // id zero
		"\x1b]7771;C;kg=" + nonce + ";id=-1;cmd=x\a",                      // negative id
		"\x1b]7771;C;kg=" + nonce + ";id=abc;cmd=x\a",                     // id not a number
		"\x1b]7771;C;kg=" + nonce + ";id=99999999999999999999;cmd=x\a",    // id overflows
		"\x1b]7771;C;kg=" + nonce + ";cmd=x\a",                            // no id
		"\x1b]7771;C;id=9;kg=" + nonce + ";cmd=x\a",                       // fields out of order
		"\x1b]7771;C;kg=" + nonce + ";id=9;rc=0\a",                        // a start with an end's field
		"\x1b]7771;D;kg=" + nonce + ";id=1;cmd=x\a",                       // an end with a start's field
		"\x1b]7771;C;kg=" + strings.Repeat("f", 32) + ";id=9;cmd=x\x1b\\", // terminated by ST, not BEL
		"\x1b]7771;C;kg=" + strings.Repeat("f", 32) + ";id=9;cmd=x",       // never terminated
	}
	for _, f := range forged {
		t.Run(fmt.Sprintf("%q", f), func(t *testing.T) {
			got := whole(start(1, "cat banner") + "before " + f + " after" + end(1, 0))
			want := []string{`start 1 "cat banner"`, fmt.Sprintf("out %q", "before "+f+" after"), `end 1 rc=0 aborted=false alt=false`}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("a forged marker changed the result:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestParserDropsEndsThatFitNothing(t *testing.T) {
	tests := map[string]string{
		"end without a start":      end(1, 0) + "x",
		"end of another command":   start(2, "a") + "A" + end(1, 0) + "B" + end(2, 4),
		"end with an invalid code": start(1, "a") + "A" + "\x1b]7771;D;kg=" + nonce + ";id=1;rc=256\a" + "B" + end(1, 5),
		"end with a text code":     start(1, "a") + "A" + "\x1b]7771;D;kg=" + nonce + ";id=1;rc=ok\a" + "B" + end(1, 5),
		"end with a negative code": start(1, "a") + "A" + "\x1b]7771;D;kg=" + nonce + ";id=1;rc=-1\a" + "B" + end(1, 5),
	}
	wants := map[string][]string{
		"end without a start":      nil,
		"end of another command":   {`start 2 "a"`, `out "AB"`, `end 2 rc=4 aborted=false alt=false`},
		"end with an invalid code": {`start 1 "a"`, `out "AB"`, `end 1 rc=5 aborted=false alt=false`},
		"end with a text code":     {`start 1 "a"`, `out "AB"`, `end 1 rc=5 aborted=false alt=false`},
		"end with a negative code": {`start 1 "a"`, `out "AB"`, `end 1 rc=5 aborted=false alt=false`},
	}
	for name, stream := range tests {
		t.Run(name, func(t *testing.T) {
			if got := whole(stream); !reflect.DeepEqual(got, wants[name]) {
				t.Errorf("events:\n got %q\nwant %q", got, wants[name])
			}
		})
	}
}

func TestParserFindsFullScreenPrograms(t *testing.T) {
	for _, seq := range []string{"\x1b[?1049h", "\x1b[?1047h", "\x1b[?47h"} {
		t.Run(fmt.Sprintf("%q", seq), func(t *testing.T) {
			got := whole(start(1, "less f") + "intro " + seq + strings.Repeat("redraw", 50) + end(1, 0))
			if n := len(got); n != 3 || got[2] != `end 1 rc=0 aborted=false alt=true` {
				t.Fatalf("events = %q, want the command flagged as full-screen", got)
			}
			if got[1] != fmt.Sprintf("out %q", "intro "+seq) {
				t.Errorf("output = %s, want everything up to and including the switch, and nothing after", got[1])
			}
		})
	}
	t.Run("a command after it is normal again", func(t *testing.T) {
		got := whole(start(1, "less") + "\x1b[?1049h" + end(1, 0) + start(2, "ls") + "files" + end(2, 0))
		if last := got[len(got)-1]; last != `end 2 rc=0 aborted=false alt=false` {
			t.Errorf("the flag leaked into the next command: %q", got)
		}
	})
	t.Run("leaving the alternate screen is not entering it", func(t *testing.T) {
		got := whole(start(1, "x") + "\x1b[?1049l" + end(1, 0))
		if last := got[len(got)-1]; strings.Contains(last, "alt=true") {
			t.Errorf("%q", got)
		}
	})
}

// The result must not depend on where the stream was cut into chunks.
func TestParserIsIndependentOfChunking(t *testing.T) {
	streams := []string{
		start(1, "echo hi") + "hi\r\n" + end(1, 0) + "% " + start(2, "a;b") + "x\x1b[31my" + end(2, 9),
		start(1, "x") + "\x1b]7771;D;kg=ffffffffffffffffffffffffffffffff;id=1;rc=0\a" + end(1, 0),
		start(1, "less") + "a\x1b[?1049hb" + end(1, 0),
		start(1, "x") + "\x1b\x1b\x1b]\x1b]7\x1b]7771\x1b]7771;" + end(1, 0) + "\x1b]777",
		start(1, "x") + "tail with a lone ESC\x1b",
		start(1, "a") + start(2, "b") + end(1, 0) + end(2, 0),
	}
	for i, s := range streams {
		want := whole(s)
		for _, sizes := range [][]int{{1}, {2}, {3}, {5}, {7}, {1, 2, 3}, {13, 1}} {
			if got := parseAll(s, sizes...); !reflect.DeepEqual(got, want) {
				t.Errorf("stream %d, chunks %v:\n got %q\nwant %q", i, sizes, got, want)
			}
		}
	}
}

// piece generators for random streams
func randomStream(r *rand.Rand) string {
	var b strings.Builder
	id := 0
	for range r.IntN(12) {
		switch r.IntN(14) {
		case 0:
			id++
			b.WriteString(start(id, randomText(r)))
		case 1:
			b.WriteString(end(max(id, 1), r.IntN(256)))
		case 2:
			b.WriteString(end(r.IntN(5), r.IntN(300)-20)) // often wrong
		case 3:
			b.WriteString("\x1b]7771;C;kg=" + strings.Repeat("a", 32) + ";id=3;cmd=forged\a")
		case 4:
			b.WriteString("\x1b]7771;")
		case 5:
			b.WriteString("\x1b]7771;C;kg=" + nonce) // cut marker
		case 6:
			b.WriteString("\x1b[?1049h")
		case 7:
			b.WriteString("\x1b")
		case 8:
			b.WriteString("\a")
		case 9:
			b.WriteString("\x1b]7771;D;kg=" + nonce + ";id=1;rc=")
		default:
			b.WriteString(randomText(r))
		}
	}
	return b.String()
}

func randomText(r *rand.Rand) string {
	const alphabet = "abc ;=\r\n\x1b[]7?19h\x00\xff"
	var b strings.Builder
	for range r.IntN(10) {
		b.WriteByte(alphabet[r.IntN(len(alphabet))])
	}
	return b.String()
}

func TestParserIsIndependentOfChunkingOnRandomStreams(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 3000 {
		s := randomStream(r)
		want := whole(s)
		for _, sizes := range [][]int{{1}, {2}, {3}, {r.IntN(9) + 1, r.IntN(9) + 1}} {
			if got := parseAll(s, sizes...); !reflect.DeepEqual(got, want) {
				t.Fatalf("case %d (%q), chunks %v:\n got %q\nwant %q", i, s, sizes, got, want)
			}
		}
	}
}

// model is a deliberately naive second implementation: it looks at the whole stream at once
// and applies the rules of ADR-0006 as plainly as possible.
func model(stream string) []string {
	bodyRE := regexp.MustCompile(`(?s)^([CD]);kg=([^;]*);id=([0-9]+);(cmd|rc)=(.*)$`)
	var events []string
	var out strings.Builder
	open, openID, alt := false, 0, false
	flush := func() {
		if out.Len() > 0 {
			events = append(events, fmt.Sprintf("out %q", out.String()))
			out.Reset()
		}
	}
	finish := func(rc int, aborted bool) {
		if open {
			flush()
			events = append(events, fmt.Sprintf("end %d rc=%d aborted=%v alt=%v", openID, rc, aborted, alt))
			open = false
		}
	}
	altRE := regexp.MustCompile(`\x1b\[\?(1049|1047|47)h`)
	data := func(s string) {
		if open && !alt {
			out.WriteString(s)
			if loc := altRE.FindStringIndex(out.String()); loc != nil {
				kept := out.String()[:loc[1]]
				out.Reset()
				out.WriteString(kept)
				alt = true
			}
		}
	}

	i := 0
	for i < len(stream) {
		if !strings.HasPrefix(stream[i:], markerPrefix) {
			// copy up to the next candidate
			next := strings.IndexByte(stream[i+1:], 0x1b)
			j := len(stream)
			if next >= 0 {
				j = i + 1 + next
			}
			data(stream[i:j])
			i = j
			continue
		}
		window := stream[i:]
		if len(window) > maxMarker {
			window = window[:maxMarker]
		}
		bel := strings.IndexByte(window, bel)
		if bel < 0 {
			data(stream[i : i+1])
			i++
			continue
		}
		m := bodyRE.FindStringSubmatch(stream[i+len(markerPrefix) : i+bel])
		if m == nil || m[2] != nonce || (m[1] == "C") != (m[4] == "cmd") {
			data(stream[i : i+1])
			i++
			continue
		}
		id, err := strconv.Atoi(m[3])
		if err != nil || id <= 0 {
			data(stream[i : i+1])
			i++
			continue
		}
		last := m[5]
		if m[1] == "C" {
			finish(0, true)
			flush()
			open, openID, alt = true, id, false
			events = append(events, fmt.Sprintf("start %d %q", id, last))
		} else if rc, err := strconv.Atoi(last); err == nil && rc >= 0 && rc <= 255 && open && id == openID {
			finish(rc, false)
		}
		i += bel + 1
	}
	finish(0, true)
	flush()
	return events
}

// The parser and the naive model must agree on every random stream.
func TestParserAgreesWithTheNaiveModel(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for i := range 4000 {
		s := randomStream(r)
		if got, want := whole(s), model(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d (%q):\n parser %q\n model  %q", i, s, got, want)
		}
	}
}

func TestParserHoldsOnlyAShortPrefixAndLosesNothing(t *testing.T) {
	r := newRec()
	p := NewParser(nonce, r)
	_, _ = p.Write([]byte(start(1, "x")))

	// a million bytes that all look like the beginning of a marker
	junk := bytes.Repeat([]byte("\x1b]7771;C;kg="), 1<<16)
	for i := 0; i < len(junk); i += 1000 {
		_, _ = p.Write(junk[i:min(i+1000, len(junk))])
		if len(p.pend) > maxMarker {
			t.Fatalf("%d bytes held, more than the limit %d", len(p.pend), maxMarker)
		}
	}
	p.Close()
	r.flush()
	if len(r.events) != 3 || r.events[1] != fmt.Sprintf("out %q", junk) {
		t.Errorf("bytes were lost or altered (%d events)", len(r.events))
	}
}

func TestParserAcceptsALongCommandUpToTheLimit(t *testing.T) {
	long := strings.Repeat("x", maxMarker-len(markerPrefix)-len("C;kg="+nonce+";id=1;cmd=")-1)
	if got := whole(start(1, long) + end(1, 0)); len(got) != 2 {
		t.Errorf("a marker of exactly the limit was not accepted: %d events", len(got))
	}
	tooLong := long + "x"
	if got := whole(start(1, tooLong) + end(1, 0)); len(got) != 0 {
		t.Errorf("a marker over the limit was accepted: %d events", len(got))
	}
}

func TestParserDoesNotAllocateForPlainOutput(t *testing.T) {
	p := NewParser(nonce, discard{})
	_, _ = p.Write([]byte(start(1, "x")))
	chunk := bytes.Repeat([]byte("plain output line\r\n"), 100)
	if n := testing.AllocsPerRun(100, func() { _, _ = p.Write(chunk) }); n != 0 {
		t.Errorf("%v allocations per Write of plain output, want 0", n)
	}
}

type discard struct{}

func (discard) Started(int, string) {}
func (discard) Output([]byte)       {}
func (discard) Ended(int, End)      {}

func FuzzParser(f *testing.F) {
	for _, s := range []string{start(1, "x") + "o" + end(1, 0), "\x1b]7771;", "\x1b]7771;C;kg=" + nonce + ";id=1;cmd=a\a\x1b[?1049h"} {
		f.Add(s, 3)
	}
	f.Fuzz(func(t *testing.T, s string, chunk int) {
		chunk = max(chunk, 1)
		if chunk > 64 {
			chunk = 64
		}
		want := whole(s)
		if got := parseAll(s, chunk); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunking changed the result:\n got %q\nwant %q", got, want)
		}
		if got := model(s); !reflect.DeepEqual(want, got) {
			t.Fatalf("model disagrees:\n parser %q\n model  %q", want, got)
		}
	})
}

// Bytes that cannot be the start of a marker are passed on at once, not held back.
func TestParserDoesNotHoldWhatIsNotAMarker(t *testing.T) {
	r := newRec()
	p := NewParser(nonce, r)
	_, _ = p.Write([]byte(start(1, "x")))
	_, _ = p.Write([]byte("\x1b]7771Xabc"))
	if r.out.String() != "\x1b]7771Xabc" || len(p.pend) != 0 {
		t.Errorf("delivered %q, held %d bytes: text that is not a marker prefix must not wait", r.out.String(), len(p.pend))
	}
}

// A candidate that reaches the size limit without an end is output, not held for ever.
func TestParserReleasesACandidateAtTheLimit(t *testing.T) {
	r := newRec()
	p := NewParser(nonce, r)
	_, _ = p.Write([]byte(start(1, "x")))
	candidate := markerPrefix + strings.Repeat("y", maxMarker-len(markerPrefix))
	_, _ = p.Write([]byte(candidate))
	if got := r.out.String(); got != candidate || len(p.pend) != 0 {
		t.Errorf("delivered %d of %d bytes, held %d: a candidate of exactly the limit has no room for its end", len(got), len(candidate), len(p.pend))
	}
}
