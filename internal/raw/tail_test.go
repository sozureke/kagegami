package raw

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// withBlock runs the test with a different block size for Tail's backwards scan.
func withBlock(t *testing.T, size int64) {
	t.Helper()
	old := tailBlock
	tailBlock = size
	t.Cleanup(func() { tailBlock = old })
}

// --- Basics -----------------------------------------------------------------------------

func TestTail(t *testing.T) {
	rd := reader(numbered(10), Limits{})

	got, err := rd.Tail(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"line 8", "line 9", "line 10"}; !slices.Equal(texts(got), want) {
		t.Errorf("Tail(3) = %v, want %v", texts(got), want)
	}
	if !slices.Equal(numbers(got), []int{8, 9, 10}) {
		t.Errorf("numbers = %v, want them as in the whole output", numbers(got))
	}
	if got.Total != 10 {
		t.Errorf("Total = %d, want 10", got.Total)
	}
	if got.Truncated {
		t.Error("asking for fewer lines than exist is not a truncation")
	}
}

func TestTailAskingForMoreThanThereIs(t *testing.T) {
	rd := reader(numbered(4), Limits{})
	got, _ := rd.Tail(100)
	if !slices.Equal(numbers(got), []int{1, 2, 3, 4}) || got.Truncated {
		t.Errorf("got %v truncated %v; want all four lines and no truncation", numbers(got), got.Truncated)
	}
}

func TestTailShapes(t *testing.T) {
	tests := []struct {
		name string
		data string
		n    int
		want []string
		nums []int
	}{
		{"a single character", "a", 1, []string{"a"}, []int{1}},
		{"a single line", "a\n", 1, []string{"a"}, []int{1}},
		{"a single empty line", "\n", 1, []string{""}, []int{1}},
		{"empty lines at the end", "a\n\n\n", 2, []string{"", ""}, []int{2, 3}},
		{"empty lines only", "\n\n\n", 5, []string{"", "", ""}, []int{1, 2, 3}},
		{"no final newline", "x\ny\nz", 2, []string{"y", "z"}, []int{2, 3}},
		{"no final newline, one line", "x\ny\nz", 1, []string{"z"}, []int{3}},
		{"final newline", "x\ny\nz\n", 2, []string{"y", "z"}, []int{2, 3}},
		{"crlf", "a\r\nb\r\nc\r\n", 2, []string{"b", "c"}, []int{2, 3}},
		{"carriage return at the very end", "a\nb\r", 1, []string{"b"}, []int{2}},
		{"the first line is included", "first\nsecond\n", 2, []string{"first", "second"}, []int{1, 2}},
		{"only the first line of two", "first\nsecond\n", 1, []string{"second"}, []int{2}},
		{"binary", "a\x00b\n\xff\xfe\n", 2, []string{"a\uFFFDb", "\uFFFD"}, []int{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reader(tt.data, Limits{}).Tail(tt.n)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(texts(got), tt.want) || !slices.Equal(numbers(got), tt.nums) {
				t.Errorf("got %q numbered %v, want %q numbered %v", texts(got), numbers(got), tt.want, tt.nums)
			}
		})
	}
}

func TestTailOfAnEmptyOutput(t *testing.T) {
	got, err := reader("", Limits{}).Tail(5)
	if err != nil || len(got.Lines) != 0 || got.Total != 0 || got.Truncated {
		t.Errorf("got %+v, %v; want nothing", got, err)
	}
}

func TestTailInvalidRequests(t *testing.T) {
	rd := reader(numbered(5), Limits{})
	for _, n := range []int{0, -1, -100} {
		got, err := rd.Tail(n)
		if !errors.Is(err, ErrInvalid) || len(got.Lines) != 0 {
			t.Errorf("Tail(%d) = %+v, %v; want ErrInvalid", n, got, err)
		}
	}
}

// --- Limits -----------------------------------------------------------------------------

func TestTailMaxLines(t *testing.T) {
	rd := reader(numbered(100), Limits{MaxLines: 5, MaxBytes: 1 << 20})

	got, _ := rd.Tail(1000)
	if !slices.Equal(numbers(got), []int{96, 97, 98, 99, 100}) {
		t.Errorf("numbers = %v, want the last 5 lines", numbers(got))
	}
	if !got.Truncated {
		t.Error("Truncated must be set when MaxLines cut the request short")
	}

	exact, _ := rd.Tail(5)
	if exact.Truncated || len(exact.Lines) != 5 {
		t.Errorf("asking for exactly MaxLines is not a truncation: %+v", exact)
	}

	// Asking for more than the cap, but the output has fewer lines than the cap: nothing was cut.
	few, _ := reader(numbered(3), Limits{MaxLines: 5}).Tail(100)
	if len(few.Lines) != 3 || few.Truncated {
		t.Errorf("got %d lines truncated %v; want 3 and no truncation", len(few.Lines), few.Truncated)
	}
}

func TestTailByteBudgetDropsTheOldestLines(t *testing.T) {
	// ten lines of 10 bytes; the budget fits two
	rd := reader(strings.Repeat("0123456789\n", 10), Limits{MaxLines: 50, MaxBytes: 25, MaxLineBytes: 25})

	got, _ := rd.Tail(10)
	if !slices.Equal(numbers(got), []int{9, 10}) {
		t.Errorf("numbers = %v, want the two newest lines 9 and 10", numbers(got))
	}
	if !got.Truncated {
		t.Error("Truncated must be set when the byte budget cut lines")
	}
}

func TestTailKeepsTheNewestLineEvenIfItIsHuge(t *testing.T) {
	rd := reader("old\n"+strings.Repeat("x", 500)+"\n", Limits{MaxLines: 10, MaxBytes: 30, MaxLineBytes: 30})
	got, err := rd.Tail(2)
	if err != nil || len(got.Lines) == 0 {
		t.Fatalf("got %+v, %v; the newest line must always be returned", got, err)
	}
	if got.Lines[len(got.Lines)-1].No != 2 || !got.Truncated {
		t.Errorf("got %+v", got)
	}
}

func TestTailCutsLongLines(t *testing.T) {
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 50}
	data := "first\n" + strings.Repeat("a", 5_000) + "\nlast\n"
	got, err := reader(data, limits).Tail(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", strings.Repeat("a", 50) + " …[4950 more bytes]", "last"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %q", texts(got))
	}
	if !got.Truncated || !slices.Equal(numbers(got), []int{1, 2, 3}) {
		t.Errorf("truncated %v numbers %v", got.Truncated, numbers(got))
	}
}

func TestTailAcrossBlockBoundaries(t *testing.T) {
	// the same output read with every awkward block size gives the same answer
	data := "alpha\nbeta\n\ngamma delta\nepsilon\r\nzeta"
	want, err := reader(data, Limits{}).Tail(6)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []int64{1, 2, 3, 4, 5, 7, 11, 64} {
		withBlock(t, block)
		got, err := reader(data, Limits{}).Tail(6)
		if err != nil || !slices.Equal(got.Lines, want.Lines) {
			t.Errorf("block %d: %+v, %v; want %+v", block, got.Lines, err, want.Lines)
		}
	}
}

func TestTailLinesLongerThanAScanBlock(t *testing.T) {
	// with the default 64 KiB block, lines far larger than it must still be found
	huge := strings.Repeat("z", 300_000)
	data := "head\n" + huge + "\n" + huge + "\ntail"
	limits := Limits{MaxLines: 10, MaxBytes: 1 << 20, MaxLineBytes: 20}

	got, err := reader(data, limits).Tail(4)
	if err != nil {
		t.Fatal(err)
	}
	marker := " …[" + strconv.Itoa(300_000-20) + " more bytes]"
	want := []string{"head", strings.Repeat("z", 20) + marker, strings.Repeat("z", 20) + marker, "tail"}
	if !slices.Equal(texts(got), want) || !slices.Equal(numbers(got), []int{1, 2, 3, 4}) {
		t.Errorf("got %d lines: %q numbered %v", len(got.Lines), texts(got), numbers(got))
	}
}

// --- The point of Tail: it does not read the whole file ---------------------------------

func TestTailDoesNotReadTheWholeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 32 MB output")
	}
	data := strings.Repeat("0123456789abcde\n", 2<<20) // 32 MB, two million lines
	c := &countingReaderAt{r: strings.NewReader(data)}
	rd := NewReader(c, int64(len(data)), Limits{})

	total, err := rd.Total() // counting the lines is the one full pass, and it happens once
	if err != nil || total != 2<<20 {
		t.Fatalf("Total = %d, %v", total, err)
	}
	c.calls, c.bytes = 0, 0

	got, err := rd.Tail(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 20 || got.Lines[0].No != total-19 || got.Lines[19].No != total {
		t.Errorf("got %d lines numbered %d..%d", len(got.Lines), got.Lines[0].No, got.Lines[len(got.Lines)-1].No)
	}
	// The tail of a 32 MB file must cost about one block, not 32 MB.
	if limit := int64(1 << 20); c.bytes > limit {
		t.Errorf("Tail(20) read %d bytes of a %d byte file (limit %d): it is reading the file instead of its end",
			c.bytes, len(data), limit)
	}
	if c.bytes == 0 {
		t.Error("nothing was read: the counter is not wired up")
	}

	// Asking again costs the same: nothing is cached except the line count.
	c.calls, c.bytes = 0, 0
	if _, err := rd.Tail(20); err != nil || c.bytes > 1<<20 {
		t.Errorf("second Tail read %d bytes, %v", c.bytes, err)
	}
}

// Whatever the size of the file, the tail of ordinary lines costs about one block.
func TestTailCostDoesNotGrowWithTheFile(t *testing.T) {
	read := func(lines int) (cost, size int64) {
		data := strings.Repeat("0123456789abcde\n", lines)
		c := &countingReaderAt{r: strings.NewReader(data)}
		rd := NewReader(c, int64(len(data)), Limits{})
		if _, err := rd.Total(); err != nil {
			t.Fatal(err)
		}
		c.bytes = 0
		if _, err := rd.Tail(10); err != nil {
			t.Fatal(err)
		}
		return c.bytes, int64(len(data))
	}

	budget := tailBlock + 8<<10 // one scan block plus the small reads of the lines themselves
	var costs []int64
	for _, lines := range []int{1 << 8, 1 << 12, 1 << 16, 1 << 20} { // 4 KB up to 16 MB
		cost, size := read(lines)
		costs = append(costs, cost)
		if cost > budget {
			t.Errorf("tail of a %d byte file cost %d bytes, want at most about one block (%d)", size, cost, budget)
		}
	}
	// a file 4096 times larger must not cost anywhere near 4096 times more
	if first, last := costs[0], costs[len(costs)-1]; last > first*40 {
		t.Errorf("cost grew from %d to %d bytes while the file grew 4096 times", first, last)
	}
}

// A huge last line has to be scanned to find where it starts, but its text is not kept.
func TestTailOfAHugeLastLine(t *testing.T) {
	big := "a\nb\n" + strings.Repeat("x", 5<<20) + "\n"
	c := &countingReaderAt{r: strings.NewReader(big)}
	rd := NewReader(c, int64(len(big)), Limits{})
	if _, err := rd.Total(); err != nil {
		t.Fatal(err)
	}
	c.bytes = 0

	got, err := rd.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 1 || got.Lines[0].No != 3 || !got.Truncated {
		t.Fatalf("got %+v", got)
	}
	if text := got.Lines[0].Text; len(text) > DefaultLimits.MaxLineBytes+64 {
		t.Errorf("returned %d bytes of text for one line, want at most about %d", len(text), DefaultLimits.MaxLineBytes)
	}
}

// --- Consistency ------------------------------------------------------------------------

func naiveTail(all []naiveLine, n int, limits Limits) ([]Line, bool) {
	want := min(n, len(all))
	truncated := false
	if want > limits.MaxLines {
		want, truncated = limits.MaxLines, true
	}
	var out []Line
	used := 0
	for i := len(all); i > len(all)-want; i-- {
		text := render(all[i-1].text, all[i-1].extra)
		if used+len(text) > limits.MaxBytes && len(out) > 0 {
			truncated = true
			break
		}
		used += len(text)
		out = append(out, Line{No: i, Text: text})
		truncated = truncated || all[i-1].extra > 0
	}
	slices.Reverse(out)
	return out, truncated
}

// Tail must agree with a trivially correct implementation on thousands of random outputs,
// at every awkward block size, including one byte at a time.
func TestTailAgreesWithANaiveImplementation(t *testing.T) {
	limits := Limits{MaxLines: 7, MaxBytes: 400, MaxLineBytes: 40}

	for _, block := range []int64{1, 2, 3, 7, 64, 1 << 16} {
		withBlock(t, block)
		rng := rand.New(rand.NewSource(block))
		for iter := range 1500 {
			data := randomData(rng)
			rd := NewReader(bytes.NewReader(data), int64(len(data)), limits)
			all := naive(data, 40)
			n := 1 + rng.Intn(12)

			got, err := rd.Tail(n)
			if err != nil {
				t.Fatalf("block %d iter %d: %v", block, iter, err)
			}
			want, truncated := naiveTail(all, n, limits)
			if !slices.Equal(got.Lines, want) || got.Truncated != truncated || got.Total != len(all) {
				t.Fatalf("block %d iter %d: Tail(%d) on %q:\n got  %+v truncated=%v total=%d\n want %+v truncated=%v total=%d",
					block, iter, n, data, got.Lines, got.Truncated, got.Total, want, truncated, len(all))
			}
		}
	}
}

// Without limits in the way, the last n lines are exactly the range total-n+1..total.
func TestTailMatchesTheEquivalentLinesRange(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	big := Limits{MaxLines: 1000, MaxBytes: 1 << 20, MaxLineBytes: 1 << 10}

	for iter := range 1000 {
		data := randomData(rng)
		rd := NewReader(bytes.NewReader(data), int64(len(data)), big)
		total, _ := rd.Total()
		if total == 0 {
			continue
		}
		n := 1 + rng.Intn(total+3)

		tail, err := rd.Tail(n)
		if err != nil {
			t.Fatal(err)
		}
		rng2, err := rd.Lines(max(total-n+1, 1), total)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(tail.Lines, rng2.Lines) {
			t.Fatalf("iter %d: Tail(%d) and Lines(%d,%d) disagree on %q:\n tail  %+v\n lines %+v",
				iter, n, max(total-n+1, 1), total, data, tail.Lines, rng2.Lines)
		}
	}
}

func TestTailResultIsOrderedAndContiguous(t *testing.T) {
	rd := reader(numbered(500), Limits{MaxLines: 40})
	got, _ := rd.Tail(40)
	if len(got.Lines) != 40 {
		t.Fatalf("got %d lines", len(got.Lines))
	}
	for i := 1; i < len(got.Lines); i++ {
		if got.Lines[i].No != got.Lines[i-1].No+1 {
			t.Fatalf("lines %d and %d are not consecutive: %d then %d", i-1, i, got.Lines[i-1].No, got.Lines[i].No)
		}
	}
	if got.Lines[len(got.Lines)-1].No != 500 {
		t.Errorf("the last line is %d, want 500", got.Lines[len(got.Lines)-1].No)
	}
}

func TestTailNeverReturnsInvalidText(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for range 500 {
		data := make([]byte, rng.Intn(400))
		for i := range data {
			data[i] = byte(rng.Intn(256))
		}
		got, err := NewReader(bytes.NewReader(data), int64(len(data)), tiny).Tail(1 + rng.Intn(10))
		if err != nil {
			t.Fatalf("Tail on random bytes: %v", err)
		}
		for _, l := range got.Lines {
			if !utf8.ValidString(l.Text) {
				t.Fatalf("invalid UTF-8 in line %d of %q: %q", l.No, data, l.Text)
			}
		}
	}
}

func TestTailIsRepeatable(t *testing.T) {
	rd := reader(numbered(50), Limits{})
	a, _ := rd.Tail(5)
	_, _ = rd.Head(3) // other calls in between must not disturb it
	_, _ = rd.Grep("line 4", 3)
	b, _ := rd.Tail(5)
	if !slices.Equal(a.Lines, b.Lines) || a.Total != b.Total || a.Truncated != b.Truncated {
		t.Errorf("two identical Tail calls differ: %+v vs %+v", a, b)
	}
}

// --- Errors -----------------------------------------------------------------------------

// failAfterReaderAt succeeds for a number of calls and then fails every call.
type failAfterReaderAt struct {
	r     io.ReaderAt
	after int
	calls int
}

func (f *failAfterReaderAt) ReadAt(p []byte, off int64) (int, error) {
	f.calls++
	if f.calls > f.after {
		return 0, errDisk
	}
	return f.r.ReadAt(p, off)
}

// Wherever the disk fails (while counting, while locating the lines or while reading
// them), Tail returns the error. It never panics and never returns wrong lines.
func TestTailSurvivesAFailureAtEveryPoint(t *testing.T) {
	data := []byte(numbered(300))
	withBlock(t, 128)
	want, err := NewReader(bytes.NewReader(data), int64(len(data)), Limits{}).Tail(5)
	if err != nil {
		t.Fatal(err)
	}

	failures := 0
	for after := range 40 {
		rd := NewReader(&failAfterReaderAt{r: bytes.NewReader(data), after: after}, int64(len(data)), Limits{})
		got, err := rd.Tail(5)
		switch {
		case err == nil:
			if !slices.Equal(got.Lines, want.Lines) {
				t.Fatalf("after %d calls: wrong lines on success: %+v", after, got.Lines)
			}
		case errors.Is(err, errDisk):
			failures++
		default:
			t.Fatalf("after %d calls: unexpected error %v", after, err)
		}
	}
	if failures == 0 {
		t.Error("no failure was ever injected: the test is not exercising the error paths")
	}
}

// failOnceReaderAt fails exactly one call, the at-th, and works before and after it. A
// transient failure like this is not masked by a later failure, so a swallowed error
// shows up as a wrong result.
type failOnceReaderAt struct {
	r     io.ReaderAt
	at    int
	calls int
}

func (f *failOnceReaderAt) ReadAt(p []byte, off int64) (int, error) {
	f.calls++
	if f.calls == f.at {
		return 0, errDisk
	}
	return f.r.ReadAt(p, off)
}

func TestTailSurvivesASingleFailureAtEveryPoint(t *testing.T) {
	data := []byte(numbered(300))
	withBlock(t, 128)
	want, err := NewReader(bytes.NewReader(data), int64(len(data)), Limits{}).Tail(5)
	if err != nil {
		t.Fatal(err)
	}

	failures := 0
	for at := 1; at <= 60; at++ {
		rd := NewReader(&failOnceReaderAt{r: bytes.NewReader(data), at: at}, int64(len(data)), Limits{})
		got, err := rd.Tail(5)
		switch {
		case err == nil:
			// the failed call was never made (past the last read): the result must be right
			if !slices.Equal(got.Lines, want.Lines) {
				t.Fatalf("call %d failed but Tail returned wrong lines without an error: %+v", at, got.Lines)
			}
		case errors.Is(err, errDisk):
			failures++
		default:
			t.Fatalf("call %d: unexpected error %v", at, err)
		}
	}
	if failures == 0 {
		t.Error("no failure was ever injected")
	}
}

func TestTailReportsAnUnreadableEnd(t *testing.T) {
	data := []byte(numbered(50))
	rd := NewReader(&failingReaderAt{data: data, failFrom: int64(len(data)) - 3}, int64(len(data)), Limits{})
	if _, err := rd.Tail(3); !errors.Is(err, errDisk) {
		t.Errorf("err = %v, want the disk error", err)
	}
}

func TestTailOnAnOpenedFile(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 1, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(numbered(10_000))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}

	rd, err := Open(dir, 1, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()

	got, err := rd.Tail(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"line 9998", "line 9999", "line 10000"}; !slices.Equal(texts(got), want) {
		t.Errorf("got %q, want %q", texts(got), want)
	}
	if got.Total != 10_000 || !slices.Equal(numbers(got), []int{9998, 9999, 10000}) {
		t.Errorf("total %d numbers %v", got.Total, numbers(got))
	}
}
