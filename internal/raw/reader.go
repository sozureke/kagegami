package raw

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalid is returned for a request that makes no sense: a line number below 1, an empty range, a bad regular expression.
var ErrInvalid = errors.New("invalid request")

// Limits bound what one call returns, so a huge output cannot flood a model's context and a zero field means its default.
type Limits struct {
	MaxLines     int // lines per call
	MaxBytes     int // text bytes per call
	MaxLineBytes int // text bytes kept from one line; the rest is dropped and counted
}

// DefaultLimits are used for every zero field of Limits.
var DefaultLimits = Limits{MaxLines: 200, MaxBytes: 64 << 10, MaxLineBytes: 8 << 10}

func (l Limits) withDefaults() Limits {
	if l.MaxLines <= 0 {
		l.MaxLines = DefaultLimits.MaxLines
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultLimits.MaxBytes
	}
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = DefaultLimits.MaxLineBytes
	}
	l.MaxLineBytes = min(l.MaxLineBytes, l.MaxBytes) // the first line must always fit
	return l
}

const replacement = "\uFFFD"

// maxPatternBytes bounds the size of a search pattern.
const maxPatternBytes = 1024

// Line is one line of output with its 1-based number.
type Line struct {
	No   int
	Text string
}

// Slice is the result of a read. Total is the number of lines in the whole output.
// Truncated is true when a limit cut the result short or shortened a line; it is not then set merely because the output has more lines than were asked for.
type Slice struct {
	Lines     []Line
	Total     int
	Truncated bool
}

// Reader returns bounded slices of one stored output. A line ends at "\n", so a final line without "\n" still counts,
// and an empty output has no lines. Text is made valid UTF-8, with NUL and invalid bytes.
type Reader struct {
	r      io.ReaderAt
	size   int64
	limits Limits
	closer io.Closer
	total  int // -1 until counted
}

// NewReader reads from r, whose total size is size.
func NewReader(r io.ReaderAt, size int64, limits Limits) *Reader {
	return &Reader{r: r, size: size, limits: limits.withDefaults(), total: -1}
}

// Open opens the stored output of blob id in dir. Close it when done.
func Open(dir string, id int64, limits Limits) (*Reader, error) {
	f, err := os.Open(Path(dir, id)) //nolint:gosec // the name is derived from a numeric id
	if err != nil {
		return nil, fmt.Errorf("open raw output %d: %w", id, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("open raw output %d: %w", id, err)
	}
	rd := NewReader(f, st.Size(), limits)
	rd.closer = f
	return rd, nil
}

// Close releases the file opened by Open. It does nothing for a Reader made by NewReader.
func (r *Reader) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

// Total returns the number of lines, counting once and remembering the result.
func (r *Reader) Total() (int, error) {
	if r.total >= 0 {
		return r.total, nil
	}
	buf := make([]byte, 64<<10)
	count := 0
	var last byte
	for off := int64(0); off < r.size; {
		n, err := r.r.ReadAt(buf, off)
		chunk := buf[:n]
		count += bytes.Count(chunk, []byte{'\n'})
		if len(chunk) > 0 {
			last = chunk[len(chunk)-1]
		}
		off += int64(n)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
	}
	if r.size > 0 && last != '\n' {
		count++
	}
	r.total = count
	return count, nil
}

// Head returns the first n lines.
func (r *Reader) Head(n int) (Slice, error) {
	if n < 1 {
		return Slice{}, invalidf("head needs at least 1 line, got %d", n)
	}
	return r.Lines(1, n)
}

// Lines returns lines from through to, both 1-based and inclusive. A range that starts
// past the end gives an empty result.
func (r *Reader) Lines(from, to int) (Slice, error) {
	if from < 1 || to < from {
		return Slice{}, invalidf("line range %d-%d", from, to)
	}
	total, err := r.Total()
	if err != nil {
		return Slice{}, err
	}
	s := Slice{Total: total}
	if from > total {
		return s, nil
	}

	want := min(to, total) - from + 1
	if want > r.limits.MaxLines {
		want = r.limits.MaxLines
		s.Truncated = true
	}

	lr := r.lineReader()
	for range from - 1 {
		if err := lr.skip(); err != nil {
			return Slice{}, err
		}
	}

	used := 0
	for no := from; len(s.Lines) < want; no++ {
		text, extra, err := lr.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Slice{}, err
		}
		line := render(text, extra)
		if used+len(line) > r.limits.MaxBytes && len(s.Lines) > 0 {
			s.Truncated = true
			break
		}
		used += len(line)
		s.Lines = append(s.Lines, Line{No: no, Text: line})
		s.Truncated = s.Truncated || extra > 0
	}
	return s, nil
}

// Grep returns up to limit lines that match the regular expression. Matching is done on the line text as returned, that is after it was cut to MaxLineBytes.
func (r *Reader) Grep(pattern string, limit int) (Slice, error) {
	if pattern == "" || len(pattern) > maxPatternBytes {
		return Slice{}, invalidf("pattern must be 1 to %d bytes", maxPatternBytes)
	}
	if limit < 1 {
		return Slice{}, invalidf("grep needs at least 1 result, got %d", limit)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Slice{}, invalidf("pattern: %v", err)
	}
	total, err := r.Total()
	if err != nil {
		return Slice{}, err
	}
	s := Slice{Total: total}
	limit = min(limit, r.limits.MaxLines)

	lr := r.lineReader()
	used := 0
	for no := 1; ; no++ {
		text, extra, err := lr.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Slice{}, err
		}
		line := render(text, extra)
		if !re.MatchString(line) {
			continue
		}
		// A match beyond the cap means there are more than were returned.
		if len(s.Lines) >= limit || (used+len(line) > r.limits.MaxBytes && len(s.Lines) > 0) {
			s.Truncated = true
			break
		}
		used += len(line)
		s.Lines = append(s.Lines, Line{No: no, Text: line})
		s.Truncated = s.Truncated || extra > 0
	}
	return s, nil
}

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// render turns raw line bytes into valid UTF-8 text and notes how much was cut off.
func render(text []byte, extra int) string {
	s := strings.ToValidUTF8(strings.ReplaceAll(string(text), "\x00", replacement), replacement)
	if extra > 0 {
		s += " …[" + strconv.Itoa(extra) + " more bytes]"
	}
	return s
}

// lineReader reads lines from the start, keeping at most maxLine bytes of each.
type lineReader struct {
	br      *bufio.Reader
	maxLine int
}

func (r *Reader) lineReader() *lineReader {
	return &lineReader{
		br:      bufio.NewReaderSize(io.NewSectionReader(r.r, 0, r.size), 64<<10),
		maxLine: r.limits.MaxLineBytes,
	}
}

// next returns the next line without its line ending, the number of bytes dropped from
// its end because it was too long, and io.EOF after the last line.
func (lr *lineReader) next() (text []byte, extra int, err error) {
	sawData := false
	for {
		chunk, err := lr.br.ReadSlice('\n')
		if len(chunk) > 0 {
			sawData = true
			terminated := chunk[len(chunk)-1] == '\n'
			if terminated {
				chunk = chunk[:len(chunk)-1]
			}
			room := lr.maxLine - len(text)
			if len(chunk) <= room {
				text = append(text, chunk...)
			} else {
				text = append(text, chunk[:room]...)
				extra += len(chunk) - room
			}
		}
		switch {
		case err == nil:
			return trimCR(text, extra), extra, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue // a long line: keep reading until its end
		case errors.Is(err, io.EOF):
			if !sawData {
				return nil, 0, io.EOF
			}
			return trimCR(text, extra), extra, nil
		default:
			return nil, 0, err
		}
	}
}

// skip discards one line.
func (lr *lineReader) skip() error {
	for {
		_, err := lr.br.ReadSlice('\n')
		switch {
		case err == nil, errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return err
		}
	}
}

// trimCR drops the "\r" of a "\r\n" ending; it is only visible when the line was not cut.
func trimCR(text []byte, extra int) []byte {
	if extra == 0 && len(text) > 0 && text[len(text)-1] == '\r' {
		return text[:len(text)-1]
	}
	return text
}
