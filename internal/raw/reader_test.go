package raw

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// small limits make every limit easy to reach in a test
var tiny = Limits{MaxLines: 5, MaxBytes: 60, MaxLineBytes: 20}

func reader(data string, limits Limits) *Reader {
	return NewReader(strings.NewReader(data), int64(len(data)), limits)
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func texts(s Slice) []string {
	out := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = l.Text
	}
	return out
}

func numbers(s Slice) []int {
	out := make([]int, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = l.No
	}
	return out
}

// countingReaderAt records how much is read through it.
type countingReaderAt struct {
	r     io.ReaderAt
	calls int
	bytes int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.calls++
	n, err := c.r.ReadAt(p, off)
	c.bytes += int64(n)
	return n, err
}

// failingReaderAt fails after a number of bytes were served.
type failingReaderAt struct {
	data     []byte
	failFrom int64
	healthy  bool // once set, reads succeed like a normal file
}

var errDisk = errors.New("disk on fire")

func (f *failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if f.healthy {
		return bytes.NewReader(f.data).ReadAt(p, off)
	}
	if off >= f.failFrom {
		return 0, errDisk
	}
	end := min(off+int64(len(p)), f.failFrom)
	n := copy(p, f.data[off:end])
	if end == f.failFrom && int64(n) < int64(len(p)) {
		return n, errDisk
	}
	return n, nil
}

// --- Limits -----------------------------------------------------------------------------

func TestLimitsDefaults(t *testing.T) {
	got := Limits{}.withDefaults()
	if got != DefaultLimits {
		t.Errorf("zero Limits = %+v, want %+v", got, DefaultLimits)
	}
	if DefaultLimits.MaxLines != 200 || DefaultLimits.MaxBytes != 64<<10 || DefaultLimits.MaxLineBytes != 8<<10 {
		t.Errorf("DefaultLimits changed: %+v", DefaultLimits)
	}

	partial := Limits{MaxLines: 7}.withDefaults()
	if partial.MaxLines != 7 || partial.MaxBytes != DefaultLimits.MaxBytes || partial.MaxLineBytes != DefaultLimits.MaxLineBytes {
		t.Errorf("a partial Limits must keep what was set and default the rest: %+v", partial)
	}

	clamped := Limits{MaxBytes: 100, MaxLineBytes: 5000}.withDefaults()
	if clamped.MaxLineBytes != 100 {
		t.Errorf("MaxLineBytes = %d, want it clamped to MaxBytes 100 so the first line always fits", clamped.MaxLineBytes)
	}
	if neg := (Limits{MaxLines: -3, MaxBytes: -1, MaxLineBytes: -9}).withDefaults(); neg != DefaultLimits {
		t.Errorf("negative limits must fall back to the defaults: %+v", neg)
	}
}

// --- Total ------------------------------------------------------------------------------

func TestTotal(t *testing.T) {
	block := 64 << 10
	tests := []struct {
		name string
		data string
		want int
	}{
		{"empty", "", 0},
		{"one char", "a", 1},
		{"one line", "a\n", 1},
		{"one empty line", "\n", 1},
		{"two empty lines", "\n\n", 2},
		{"empty line at the end", "a\n\n", 2},
		{"no final newline", "a\nb", 2},
		{"crlf", "a\r\nb\r\n", 2},
		{"only carriage returns", "a\rb\rc", 1},
		{"ten thousand lines", numbered(10_000), 10_000},
		{"ends exactly on a block boundary with a newline", strings.Repeat("x", block-1) + "\n", 1},
		{"ends exactly on a block boundary without one", strings.Repeat("x", block), 1},
		{"newline is the first byte past a block", strings.Repeat("x", block) + "\n", 1},
		{"spans several blocks", strings.Repeat("x\n", block*2+3), block*2 + 3},
		{"binary", "\x00\x01\n\xff\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reader(tt.data, Limits{}).Total()
			if err != nil || got != tt.want {
				t.Errorf("Total = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestTotalIsCountedOnce(t *testing.T) {
	c := &countingReaderAt{r: strings.NewReader(numbered(1_000))}
	rd := NewReader(c, int64(len(numbered(1_000))), Limits{})

	first, err := rd.Total()
	if err != nil || first != 1_000 {
		t.Fatalf("Total = %d, %v", first, err)
	}
	callsAfterFirst := c.calls
	second, _ := rd.Total()
	if second != first || c.calls != callsAfterFirst {
		t.Errorf("the second Total read the file again (%d extra calls)", c.calls-callsAfterFirst)
	}
}

// --- Head and Lines ---------------------------------------------------------------------

func TestHead(t *testing.T) {
	rd := reader(numbered(10), Limits{})

	got, err := rd.Head(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"line 1", "line 2", "line 3"}; !slices.Equal(texts(got), want) {
		t.Errorf("Head(3) = %v, want %v", texts(got), want)
	}
	if !slices.Equal(numbers(got), []int{1, 2, 3}) {
		t.Errorf("numbers = %v", numbers(got))
	}
	if got.Total != 10 {
		t.Errorf("Total = %d, want 10", got.Total)
	}
	if got.Truncated {
		t.Error("asking for fewer lines than exist is not a truncation")
	}

	all, _ := rd.Head(100)
	if len(all.Lines) != 10 || all.Truncated {
		t.Errorf("Head(100) of 10 lines = %d lines, truncated %v", len(all.Lines), all.Truncated)
	}
}

func TestLinesRanges(t *testing.T) {
	rd := reader(numbered(20), Limits{})

	tests := []struct {
		name     string
		from, to int
		want     []int
	}{
		{"single line", 5, 5, []int{5}},
		{"a range", 3, 6, []int{3, 4, 5, 6}},
		{"the first line", 1, 1, []int{1}},
		{"the last line", 20, 20, []int{20}},
		{"up to the end", 18, 20, []int{18, 19, 20}},
		{"past the end is cut at the end", 19, 500, []int{19, 20}},
		{"starts past the end", 21, 30, nil},
		{"starts far past the end", 100000, 100010, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rd.Lines(tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(numbers(got), tt.want) {
				t.Errorf("Lines(%d,%d) numbers = %v, want %v", tt.from, tt.to, numbers(got), tt.want)
			}
			for _, l := range got.Lines {
				if want := "line " + strconv.Itoa(l.No); l.Text != want {
					t.Errorf("line %d text = %q, want %q", l.No, l.Text, want)
				}
			}
			if got.Total != 20 || got.Truncated {
				t.Errorf("Total = %d, Truncated = %v; want 20 and false", got.Total, got.Truncated)
			}
		})
	}
}

func TestLinesWithoutAFinalNewline(t *testing.T) {
	got, err := reader("first\nsecond\nlast", Limits{}).Lines(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second", "last"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %v, want %v", texts(got), want)
	}
}

func TestEmptyLinesAreLines(t *testing.T) {
	got, _ := reader("a\n\n\nb\n", Limits{}).Lines(1, 10)
	if want := []string{"a", "", "", "b"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %q, want %q", texts(got), want)
	}
	if !slices.Equal(numbers(got), []int{1, 2, 3, 4}) {
		t.Errorf("numbers = %v", numbers(got))
	}
}

func TestEmptyOutputHasNoLines(t *testing.T) {
	rd := reader("", Limits{})
	for name, call := range map[string]func() (Slice, error){
		"head":  func() (Slice, error) { return rd.Head(5) },
		"lines": func() (Slice, error) { return rd.Lines(1, 5) },
		"grep":  func() (Slice, error) { return rd.Grep(".", 5) },
	} {
		got, err := call()
		if err != nil || len(got.Lines) != 0 || got.Total != 0 || got.Truncated {
			t.Errorf("%s on empty output = %+v, %v; want nothing", name, got, err)
		}
	}
}

func TestInvalidRequests(t *testing.T) {
	rd := reader(numbered(5), Limits{})
	tests := []struct {
		name string
		call func() (Slice, error)
	}{
		{"head zero", func() (Slice, error) { return rd.Head(0) }},
		{"head negative", func() (Slice, error) { return rd.Head(-1) }},
		{"lines from zero", func() (Slice, error) { return rd.Lines(0, 3) }},
		{"lines from negative", func() (Slice, error) { return rd.Lines(-5, 3) }},
		{"lines to before from", func() (Slice, error) { return rd.Lines(4, 3) }},
		{"grep empty pattern", func() (Slice, error) { return rd.Grep("", 3) }},
		{"grep zero limit", func() (Slice, error) { return rd.Grep("x", 0) }},
		{"grep negative limit", func() (Slice, error) { return rd.Grep("x", -2) }},
		{"grep bad regexp", func() (Slice, error) { return rd.Grep("([", 3) }},
		{"grep unbalanced group", func() (Slice, error) { return rd.Grep("a)", 3) }},
		{"grep pattern too long", func() (Slice, error) { return rd.Grep(strings.Repeat("a", maxPatternBytes+1), 3) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.call()
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
			if len(got.Lines) != 0 {
				t.Errorf("an invalid request returned lines: %+v", got)
			}
		})
	}

	if _, err := rd.Grep(strings.Repeat("a", maxPatternBytes), 3); err != nil {
		t.Errorf("a pattern of exactly the maximum length is valid: %v", err)
	}
}

// --- Limits on the result ---------------------------------------------------------------

func TestMaxLinesLimitsTheResult(t *testing.T) {
	rd := reader(numbered(100), Limits{MaxLines: 3, MaxBytes: 1 << 20})

	got, _ := rd.Lines(10, 90)
	if !slices.Equal(numbers(got), []int{10, 11, 12}) {
		t.Errorf("numbers = %v, want 3 lines starting at 10", numbers(got))
	}
	if !got.Truncated {
		t.Error("Truncated must be set when MaxLines cut the request short")
	}
	if got.Total != 100 {
		t.Errorf("Total = %d, want the real total 100", got.Total)
	}

	exact, _ := rd.Lines(10, 12)
	if exact.Truncated || len(exact.Lines) != 3 {
		t.Errorf("asking for exactly MaxLines is not a truncation: %+v", exact)
	}
}

func TestMaxBytesLimitsTheResult(t *testing.T) {
	// ten lines of 10 bytes each; the byte budget fits two of them
	rd := reader(strings.Repeat("0123456789\n", 10), Limits{MaxLines: 50, MaxBytes: 25, MaxLineBytes: 25})

	got, _ := rd.Head(10)
	if len(got.Lines) != 2 || !got.Truncated {
		t.Errorf("got %d lines, truncated %v; want 2 and true", len(got.Lines), got.Truncated)
	}

	// one more byte of room still fits only two; exactly three lines of room fits three
	three := reader(strings.Repeat("0123456789\n", 10), Limits{MaxLines: 50, MaxBytes: 30, MaxLineBytes: 30})
	got, _ = three.Head(10)
	if len(got.Lines) != 3 || !got.Truncated {
		t.Errorf("a budget of exactly three lines gave %d lines, truncated %v", len(got.Lines), got.Truncated)
	}
}

func TestTheFirstLineAlwaysFitsTheByteBudget(t *testing.T) {
	rd := reader(strings.Repeat("x", 500)+"\nsecond\n", Limits{MaxLines: 10, MaxBytes: 30, MaxLineBytes: 30})

	got, err := rd.Head(2)
	if err != nil || len(got.Lines) == 0 {
		t.Fatalf("a huge first line must still be returned: %+v, %v", got, err)
	}
	if !got.Truncated {
		t.Error("Truncated must be set")
	}
}

// --- Long lines -------------------------------------------------------------------------

func TestLongLinesAreCutAndCounted(t *testing.T) {
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 100}
	data := "short\n" + strings.Repeat("a", 20_000) + "\nafter\n"
	got, err := reader(data, limits).Head(10)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"short", strings.Repeat("a", 100) + " …[19900 more bytes]", "after"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %q", texts(got))
	}
	if !got.Truncated {
		t.Error("a shortened line must set Truncated")
	}
	if !slices.Equal(numbers(got), []int{1, 2, 3}) {
		t.Errorf("numbers = %v: a long line must not disturb the numbering", numbers(got))
	}
}

func TestLinesLongerThanTheInternalBuffer(t *testing.T) {
	// the reader buffers 64 KiB; a line several times larger takes the "keep reading" path
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 50}
	huge := strings.Repeat("z", 300_000)
	data := "head\n" + huge + "\n" + huge + "\ntail\n"

	got, err := reader(data, limits).Head(10)
	if err != nil {
		t.Fatal(err)
	}
	marker := " …[" + strconv.Itoa(300_000-50) + " more bytes]"
	if want := []string{"head", strings.Repeat("z", 50) + marker, strings.Repeat("z", 50) + marker, "tail"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %d lines: %q", len(got.Lines), texts(got))
	}

	// and when the huge lines are skipped on the way to a later range
	later, err := reader(data, limits).Lines(4, 4)
	if err != nil || !slices.Equal(texts(later), []string{"tail"}) || later.Lines[0].No != 4 {
		t.Errorf("Lines(4,4) = %+v, %v; want \"tail\" as line 4", later, err)
	}
}

func TestLineExactlyAtTheLimitIsNotCut(t *testing.T) {
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 10}
	for _, tt := range []struct {
		line      string
		truncated bool
	}{
		{strings.Repeat("a", 9), false},
		{strings.Repeat("a", 10), false},
		{strings.Repeat("a", 11), true},
	} {
		got, _ := reader(tt.line+"\n", limits).Head(1)
		if got.Truncated != tt.truncated {
			t.Errorf("%d-byte line: Truncated = %v, want %v (%q)", len(tt.line), got.Truncated, tt.truncated, texts(got))
		}
	}
}

func TestACutNeverSplitsARuneIntoGarbage(t *testing.T) {
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 5}
	got, _ := reader("ééééé\n", limits).Head(1) // each é is two bytes: byte 5 falls inside a rune

	if !utf8.ValidString(got.Lines[0].Text) {
		t.Errorf("the cut produced invalid UTF-8: %q", got.Lines[0].Text)
	}
	if !got.Truncated || !strings.Contains(got.Lines[0].Text, "more bytes]") {
		t.Errorf("got %q", got.Lines[0].Text)
	}
}

// --- Line endings and bad bytes ---------------------------------------------------------

func TestCarriageReturns(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []string
	}{
		{"crlf endings", "a\r\nb\r\n", []string{"a", "b"}},
		{"lone carriage return inside a line is kept", "a\rb\n", []string{"a\rb"}},
		{"carriage return at the very end", "a\r", []string{"a"}},
		{"only a carriage return on a line", "\r\n", []string{""}},
		{"two carriage returns keep one", "a\r\r\n", []string{"a\r"}},
		{"mixed endings", "a\nb\r\nc", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := reader(tt.data, Limits{}).Head(10)
			if !slices.Equal(texts(got), tt.want) {
				t.Errorf("got %q, want %q", texts(got), tt.want)
			}
		})
	}
}

func TestACutLineKeepsItsTrailingCarriageReturnOut(t *testing.T) {
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 4}
	got, _ := reader("abcdefgh\r\n", limits).Head(1)
	if want := "abcd …[5 more bytes]"; got.Lines[0].Text != want {
		t.Errorf("got %q, want %q", got.Lines[0].Text, want)
	}
}

// A carriage return that happens to be the last kept byte of a cut line is content, not a
// line ending: only an uncut line loses its trailing carriage return.
func TestCarriageReturnAtTheCutIsKept(t *testing.T) {
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 4}
	got, _ := reader("abc\rdefg\n", limits).Head(1)
	if want := "abc\r …[4 more bytes]"; got.Lines[0].Text != want {
		t.Errorf("got %q, want %q", got.Lines[0].Text, want)
	}
}

func TestBinaryOutputBecomesValidText(t *testing.T) {
	data := "a\x00b\nbad \xff\xfe\xc3\x28 bytes\n\x00\x00\n"
	got, err := reader(data, Limits{}).Head(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got.Lines {
		if !utf8.ValidString(l.Text) {
			t.Errorf("line %d is not valid UTF-8: %q", l.No, l.Text)
		}
		if strings.ContainsRune(l.Text, 0) {
			t.Errorf("line %d still has a NUL byte: %q", l.No, l.Text)
		}
	}
	if got.Lines[0].Text != "a\uFFFDb" {
		t.Errorf("NUL replacement = %q, want a\\uFFFDb", got.Lines[0].Text)
	}
	if got.Total != 3 {
		t.Errorf("Total = %d, want 3: bad bytes must not change the line count", got.Total)
	}
}

func TestValidUnicodeSurvives(t *testing.T) {
	got, _ := reader("héllo ✓ 世界 🚀\n", Limits{}).Head(1)
	if got.Lines[0].Text != "héllo ✓ 世界 🚀" {
		t.Errorf("got %q", got.Lines[0].Text)
	}
}

// --- Grep -------------------------------------------------------------------------------

func TestGrep(t *testing.T) {
	data := "PORT   STATE SERVICE\n22/tcp open  ssh\n80/tcp open  http\n443/tcp closed https\n8080/tcp open  http-proxy\n"
	rd := reader(data, Limits{})

	tests := []struct {
		name    string
		pattern string
		limit   int
		want    []int
	}{
		{"literal", "ssh", 10, []int{2}},
		{"several matches", "open", 10, []int{2, 3, 5}},
		{"regular expression", `^\d+/tcp open`, 10, []int{2, 3, 5}},
		{"alternation", "ssh|https", 10, []int{2, 4}},
		{"case sensitive by default", "port", 10, nil},
		{"case insensitive flag", "(?i)port", 10, []int{1}},
		{"anchored", "^80/", 10, []int{3}},
		{"matches every line", ".", 10, []int{1, 2, 3, 4, 5}},
		{"no match", "telnet", 10, nil},
		{"a dot matches any character", "o.en", 10, []int{2, 3, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rd.Grep(tt.pattern, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(numbers(got), tt.want) {
				t.Errorf("Grep(%q) numbers = %v, want %v", tt.pattern, numbers(got), tt.want)
			}
			if got.Total != 5 {
				t.Errorf("Total = %d, want 5", got.Total)
			}
			if got.Truncated {
				t.Error("Truncated must stay false when every match fits")
			}
		})
	}
}

func TestGrepReturnsTheMatchingText(t *testing.T) {
	got, _ := reader("alpha\nbeta\ngamma\n", Limits{}).Grep("a$", 10)
	if want := []string{"alpha", "beta", "gamma"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %q, want %q", texts(got), want)
	}
}

func TestGrepLimit(t *testing.T) {
	rd := reader(strings.Repeat("hit\n", 10), Limits{})

	got, _ := rd.Grep("hit", 3)
	if !slices.Equal(numbers(got), []int{1, 2, 3}) || !got.Truncated {
		t.Errorf("got %v truncated %v; want the first 3 matches and Truncated", numbers(got), got.Truncated)
	}

	exact, _ := reader(strings.Repeat("hit\n", 3), Limits{}).Grep("hit", 3)
	if len(exact.Lines) != 3 || exact.Truncated {
		t.Errorf("exactly 3 matches with a limit of 3 is not a truncation: %+v", exact)
	}

	one, _ := reader("hit\nmiss\nhit\n", Limits{}).Grep("hit", 1)
	if len(one.Lines) != 1 || !one.Truncated {
		t.Errorf("a second match beyond the limit must set Truncated: %+v", one)
	}
}

// Asking for more results than MaxLines allows is not a truncation when there are fewer
// matches than the cap: nothing was cut.
func TestGrepLimitAboveMaxLinesIsNotATruncation(t *testing.T) {
	rd := reader("hit\nmiss\nhit\n", Limits{MaxLines: 4})
	got, _ := rd.Grep("hit", 1000)
	if len(got.Lines) != 2 || got.Truncated {
		t.Errorf("got %d lines truncated %v; want 2 lines and no truncation", len(got.Lines), got.Truncated)
	}
}

// The same for Lines: a range far beyond the end, with a limit lower than the range but
// higher than the lines that exist.
func TestLinesBeyondTheLimitButBeyondTheEndToo(t *testing.T) {
	rd := reader(numbered(3), Limits{MaxLines: 5})
	got, _ := rd.Lines(2, 1000)
	if !slices.Equal(numbers(got), []int{2, 3}) || got.Truncated {
		t.Errorf("got %v truncated %v; want lines 2-3 and no truncation", numbers(got), got.Truncated)
	}
}

func TestGrepRespectsMaxLines(t *testing.T) {
	rd := reader(strings.Repeat("hit\n", 50), Limits{MaxLines: 4})
	got, _ := rd.Grep("hit", 1000)
	if len(got.Lines) != 4 || !got.Truncated {
		t.Errorf("got %d lines truncated %v; want MaxLines (4) and Truncated", len(got.Lines), got.Truncated)
	}
}

func TestGrepRespectsMaxBytes(t *testing.T) {
	rd := reader(strings.Repeat("0123456789 hit\n", 20), Limits{MaxLines: 100, MaxBytes: 40, MaxLineBytes: 40})
	got, _ := rd.Grep("hit", 100)
	if len(got.Lines) != 2 || !got.Truncated {
		t.Errorf("got %d lines truncated %v; want 2 lines (a 40 byte budget) and Truncated", len(got.Lines), got.Truncated)
	}
}

func TestGrepSeesTheCutText(t *testing.T) {
	// the needle is past MaxLineBytes: matching happens on what would be returned, which
	// is documented behaviour
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 10}
	data := strings.Repeat("a", 50) + "needle\nneedle at the start\n"
	got, _ := reader(data, limits).Grep("needle", 10)
	if !slices.Equal(numbers(got), []int{2}) {
		t.Errorf("numbers = %v, want only line 2 (line 1 holds the needle beyond the cut)", numbers(got))
	}
}

func TestGrepNeedsNoBackrefs(t *testing.T) {
	// RE2 has no backreferences; a pattern that needs one is rejected, not mis-run
	if _, err := reader("aa\n", Limits{}).Grep(`(a)\1`, 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

// A pattern that makes a backtracking engine take exponential time must finish quickly,
// because patterns may come from a model.
func TestGrepIsImmuneToCatastrophicPatterns(t *testing.T) {
	data := strings.Repeat("a", 5_000) + "b\n"
	rd := reader(strings.Repeat(data, 20), Limits{MaxLineBytes: 8 << 10})

	for _, pattern := range []string{`(a+)+$`, `(a*)*c`, `(a|aa)+$`, `^(a+)+$`} {
		start := time.Now()
		if _, err := rd.Grep(pattern, 5); err != nil {
			t.Fatalf("%s: %v", pattern, err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("%s took %v: the engine is not running in linear time", pattern, elapsed)
		}
	}
}

// --- Open, Close and read errors --------------------------------------------------------

func TestOpenReadsWhatTheWriterStored(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 3, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(numbered(10_000))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}

	rd, err := Open(dir, 3, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()

	got, err := rd.Lines(9_998, 10_005)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"line 9998", "line 9999", "line 10000"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %q, want %q", texts(got), want)
	}
	if got.Total != 10_000 {
		t.Errorf("Total = %d, want 10000", got.Total)
	}

	found, _ := rd.Grep(`^line 5000$`, 5)
	if len(found.Lines) != 1 || found.Lines[0].No != 5000 {
		t.Errorf("Grep = %+v, want line 5000", found)
	}
}

func TestOpenMissingFile(t *testing.T) {
	_, err := Open(t.TempDir(), 99, Limits{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want one wrapping fs.ErrNotExist", err)
	}
}

func TestClose(t *testing.T) {
	if err := reader("x", Limits{}).Close(); err != nil {
		t.Errorf("Close on a Reader without a file = %v, want nil", err)
	}

	dir := t.TempDir()
	w, _ := Create(dir, 1, Options{})
	_, _ = w.Write([]byte("x\n"))
	_, _ = w.Close()
	rd, err := Open(dir, 1, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	if _, err := rd.Head(1); err == nil {
		t.Error("reading after Close must fail")
	}
}

func TestReadErrorsArePropagated(t *testing.T) {
	data := []byte(numbered(5_000))
	half := int64(len(data) / 2)

	t.Run("Total", func(t *testing.T) {
		rd := NewReader(&failingReaderAt{data: data, failFrom: half}, int64(len(data)), Limits{})
		if _, err := rd.Total(); !errors.Is(err, errDisk) {
			t.Errorf("err = %v, want the disk error", err)
		}
	})
	t.Run("Lines when counting works but reading fails", func(t *testing.T) {
		// counting reads everything first, so a failure anywhere must surface
		rd := NewReader(&failingReaderAt{data: data, failFrom: half}, int64(len(data)), Limits{})
		if _, err := rd.Lines(1, 10); !errors.Is(err, errDisk) {
			t.Errorf("err = %v, want the disk error", err)
		}
	})
	t.Run("Grep", func(t *testing.T) {
		rd := NewReader(&failingReaderAt{data: data, failFrom: half}, int64(len(data)), Limits{})
		if _, err := rd.Grep("line", 5); !errors.Is(err, errDisk) {
			t.Errorf("err = %v, want the disk error", err)
		}
	})
}

func TestAFailedCountIsNotCached(t *testing.T) {
	data := []byte(numbered(100))
	f := &failingReaderAt{data: data, failFrom: 10}
	rd := NewReader(f, int64(len(data)), Limits{})

	if _, err := rd.Total(); err == nil {
		t.Fatal("expected an error")
	}
	f.healthy = true // the disk recovers
	if n, err := rd.Total(); err != nil || n != 100 {
		t.Errorf("Total after recovery = %d, %v; want 100", n, err)
	}
}

// --- Differential test ------------------------------------------------------------------

type naiveLine struct {
	text  []byte
	extra int
}

// naive re-implements the documented behaviour in the simplest possible way.
func naive(data []byte, maxLine int) []naiveLine {
	var lines []naiveLine
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		var raw []byte
		if i < 0 {
			raw, data = data, nil
		} else {
			raw, data = data[:i], data[i+1:]
		}
		l := naiveLine{text: raw}
		if len(raw) > maxLine {
			l = naiveLine{text: raw[:maxLine], extra: len(raw) - maxLine}
		} else if len(raw) > 0 && raw[len(raw)-1] == '\r' {
			l.text = raw[:len(raw)-1]
		}
		lines = append(lines, l)
	}
	return lines
}

func randomData(rng *rand.Rand) []byte {
	var b bytes.Buffer
	for range rng.Intn(60) {
		switch rng.Intn(10) {
		case 0:
			b.WriteString("\n")
		case 1:
			b.WriteString("\r\n")
		case 2:
			b.WriteString(strings.Repeat("L", rng.Intn(300)) + "\n")
		case 3:
			b.Write([]byte{0, 0xff, 0xc3, 'x'})
		case 4:
			b.WriteString("héllo ✓ ")
		default:
			b.WriteString("word" + strconv.Itoa(rng.Intn(5)) + " ")
		}
	}
	if rng.Intn(2) == 0 {
		b.WriteString("\n")
	}
	return b.Bytes()
}

// The reader must agree with a trivially correct implementation on thousands of random
// outputs, including awkward ones: CRLF, NUL, invalid UTF-8, long lines, no final newline.
func TestAgreesWithANaiveImplementation(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	limits := Limits{MaxLines: 7, MaxBytes: 400, MaxLineBytes: 40}
	re := regexp.MustCompile(`word1`)

	for iter := range 3000 {
		data := randomData(rng)
		rd := NewReader(bytes.NewReader(data), int64(len(data)), limits)
		all := naive(data, 40)

		total, err := rd.Total()
		if err != nil || total != len(all) {
			t.Fatalf("iter %d: Total = %d, %v; want %d for %q", iter, total, err, len(all), data)
		}

		from := 1 + rng.Intn(len(all)+3)
		to := from + rng.Intn(12)
		got, err := rd.Lines(from, to)
		if err != nil {
			t.Fatalf("iter %d: Lines(%d,%d): %v", iter, from, to, err)
		}

		var want []Line
		truncated := false
		wantN := min(to, len(all)) - from + 1
		if wantN > limits.MaxLines {
			wantN, truncated = limits.MaxLines, true
		}
		used := 0
		for i := from; i <= len(all) && len(want) < wantN; i++ {
			text := render(all[i-1].text, all[i-1].extra)
			if used+len(text) > limits.MaxBytes && len(want) > 0 {
				truncated = true
				break
			}
			used += len(text)
			want = append(want, Line{No: i, Text: text})
			truncated = truncated || all[i-1].extra > 0
		}
		if from > len(all) {
			want, truncated = nil, false
		}
		if !slices.Equal(got.Lines, want) || got.Truncated != truncated || got.Total != len(all) {
			t.Fatalf("iter %d: Lines(%d,%d) on %q:\n got  %+v truncated=%v total=%d\n want %+v truncated=%v total=%d",
				iter, from, to, data, got.Lines, got.Truncated, got.Total, want, truncated, len(all))
		}

		grepGot, err := rd.Grep("word1", 4)
		if err != nil {
			t.Fatal(err)
		}
		var grepWant []Line
		grepTruncated, grepUsed := false, 0
		for i, l := range all {
			text := render(l.text, l.extra)
			if !re.MatchString(text) {
				continue
			}
			if len(grepWant) >= 4 || (grepUsed+len(text) > limits.MaxBytes && len(grepWant) > 0) {
				grepTruncated = true
				break
			}
			grepUsed += len(text)
			grepWant = append(grepWant, Line{No: i + 1, Text: text})
			grepTruncated = grepTruncated || l.extra > 0
		}
		if !slices.Equal(grepGot.Lines, grepWant) || grepGot.Truncated != grepTruncated {
			t.Fatalf("iter %d: Grep on %q:\n got  %+v truncated=%v\n want %+v truncated=%v",
				iter, data, grepGot.Lines, grepGot.Truncated, grepWant, grepTruncated)
		}
	}
}

func TestReadingAnythingNeverReturnsInvalidText(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for range 500 {
		data := make([]byte, rng.Intn(400))
		for i := range data {
			data[i] = byte(rng.Intn(256))
		}
		rd := NewReader(bytes.NewReader(data), int64(len(data)), tiny)
		got, err := rd.Head(10)
		if err != nil {
			t.Fatalf("Head on random bytes: %v", err)
		}
		for _, l := range got.Lines {
			if !utf8.ValidString(l.Text) {
				t.Fatalf("invalid UTF-8 in line %d of %q: %q", l.No, data, l.Text)
			}
		}
	}
}

func ExampleReader_Lines() {
	rd := NewReader(strings.NewReader("one\ntwo\nthree\nfour\n"), 19, Limits{})
	s, _ := rd.Lines(2, 3)
	for _, l := range s.Lines {
		fmt.Printf("%d: %s\n", l.No, l.Text)
	}
	fmt.Println("total:", s.Total)
	// Output:
	// 2: two
	// 3: three
	// total: 4
}
