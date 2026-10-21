package raw

import (
	"bytes"
	"errors"
	"io"
)

// tailBlock is how much of the end of the output is read at a time.
var tailBlock int64 = 64 << 10

// span is the byte range [start, end) of one line, without its line ending.
type span struct{ start, end int64 }

// Tail returns the last n lines, numbered as they are in the whole output. It reads the
// file from the end in blocks, so the cost depends on the size of those lines and not on
// the size of the output; only the first call has to count the lines of the whole output.
//
// Limits apply as they do for Lines, but when the byte budget is reached it is the
// oldest of the requested lines that are dropped, so the newest output is always kept.
func (r *Reader) Tail(n int) (Slice, error) {
	if n < 1 {
		return Slice{}, invalidf("tail needs at least 1 line, got %d", n)
	}
	total, err := r.Total()
	if err != nil {
		return Slice{}, err
	}
	s := Slice{Total: total}
	if total == 0 {
		return s, nil
	}

	want := min(n, total)
	if want > r.limits.MaxLines {
		want = r.limits.MaxLines
		s.Truncated = true
	}

	spans, err := r.lastSpans(want)
	if err != nil {
		return Slice{}, err
	}

	// Newest first, so that when the byte budget runs out it is the oldest lines that go.
	used := 0
	for i, sp := range spans {
		text, extra, err := r.readSpan(sp)
		if err != nil {
			return Slice{}, err
		}
		line := render(text, extra)
		if used+len(line) > r.limits.MaxBytes && len(s.Lines) > 0 {
			s.Truncated = true
			break
		}
		used += len(line)
		s.Lines = append(s.Lines, Line{No: total - i, Text: line})
		s.Truncated = s.Truncated || extra > 0
	}

	// back to reading order
	for i, j := 0, len(s.Lines)-1; i < j; i, j = i+1, j-1 {
		s.Lines[i], s.Lines[j] = s.Lines[j], s.Lines[i]
	}
	return s, nil
}

// lastSpans finds the byte ranges of the last want lines by scanning backwards from the
// end of the output. It returns them newest first. It keeps no line text, only offsets,
// so a huge line costs reading time but not memory.
func (r *Reader) lastSpans(want int) ([]span, error) {
	spans := make([]span, 0, want)

	// The line ending of the last line is not part of it.
	end := r.size
	last := make([]byte, 1)
	if _, err := r.r.ReadAt(last, r.size-1); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if last[0] == '\n' {
		end--
	}
	pos := end // everything at or after pos is already scanned
	lineEnd := end

	buf := make([]byte, tailBlock)
	for pos > 0 && len(spans) < want {
		start := max(pos-tailBlock, 0)
		block := buf[:pos-start]
		if _, err := r.r.ReadAt(block, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}

		for len(spans) < want {
			i := bytes.LastIndexByte(block, '\n')
			if i < 0 {
				break
			}
			nl := start + int64(i)
			spans = append(spans, span{start: nl + 1, end: lineEnd})
			lineEnd = nl
			block = block[:i]
		}
		pos = start
	}

	// reached the beginning of the output: what is left is the first line.
	if pos == 0 && len(spans) < want {
		spans = append(spans, span{start: 0, end: lineEnd})
	}
	return spans, nil
}

// readSpan reads at most MaxLineBytes of a line and reports how many bytes were left out.
func (r *Reader) readSpan(sp span) (text []byte, extra int, err error) {
	length := sp.end - sp.start
	keep := min(length, int64(r.limits.MaxLineBytes))
	text = make([]byte, keep)
	if keep > 0 {
		if _, err := r.r.ReadAt(text, sp.start); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
	}
	extra = int(length - keep)
	return trimCR(text, extra), extra, nil
}
