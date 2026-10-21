package terminal

import (
	"io"
	"strings"
	"unicode/utf8"
)

const (
	// maxLineRunes bounds one line while it is being built, so that endless output without a
	// newline cannot use up the memory. What does not fit is dropped.
	maxLineRunes = 64 << 10
	tabWidth     = 8
)

// Cleaner turns the raw bytes a command wrote to a terminal into the text a person saw on it.
// It is a small line-oriented terminal: colours and other escape sequences are dropped,
// carriage returns and "erase line" rewrite the current line (so a progress bar that redraws
// itself leaves nothing behind but its last state), backspaces and tabs are applied, and line
// endings become "\n". Only finished lines are passed on, with trailing spaces removed.
//
// The result does not depend on how the bytes are split into Write calls.
type Cleaner struct {
	out io.Writer

	line   []rune
	cursor int

	state  escState
	params []byte
	csiQ   bool    // private-mode prefix seen (ESC [ ?)
	osc    oscStep // inside OSC: waiting for BEL or ST
	rest   []byte  // the start of a UTF-8 sequence cut by a chunk boundary
	err    error
}

type escState int

const (
	stGround escState = iota
	stEsc             // after ESC
	stCSI             // after ESC [
	stOSC             // after ESC ]
	stSkip1           // ESC ( ) # etc: one more byte belongs to the sequence
)

type oscStep int

const (
	oscBody oscStep = iota
	oscEsc          // saw ESC inside OSC; a following backslash ends it
)

// NewCleaner returns a Cleaner that writes finished lines to out.
func NewCleaner(out io.Writer) *Cleaner { return &Cleaner{out: out} }

// CleanString cleans a whole text at once.
func CleanString(raw string) string {
	var b strings.Builder
	c := NewCleaner(&b)
	_, _ = c.Write([]byte(raw))
	_ = c.Close()
	return b.String()
}

// Write feeds raw bytes. It returns an error only if the underlying writer failed.
func (c *Cleaner) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	data := p
	if len(c.rest) > 0 {
		data = append(c.rest, p...)
		c.rest = nil
	}
	for i := 0; i < len(data); {
		b := data[i]
		if b >= utf8.RuneSelf && c.state == stGround {
			if !utf8.FullRune(data[i:]) {
				c.rest = append([]byte(nil), data[i:]...)
				return len(p), c.err
			}
			r, n := utf8.DecodeRune(data[i:])
			c.put(r) // an invalid byte becomes U+FFFD
			i += n
			continue
		}
		c.step(b)
		i++
		if c.err != nil {
			return len(p), c.err
		}
	}
	return len(p), c.err
}

// Close ends the text: an unfinished line is passed on as it is.
func (c *Cleaner) Close() error {
	if len(c.rest) > 0 {
		c.put(utf8.RuneError)
		c.rest = nil
	}
	if len(c.line) > 0 {
		c.commit()
	}
	return c.err
}

func (c *Cleaner) step(b byte) {
	switch c.state {
	case stEsc:
		switch b {
		case '[':
			c.state, c.params, c.csiQ = stCSI, c.params[:0], false
		case ']':
			c.state, c.osc = stOSC, oscBody
		case '(', ')', '*', '+', '#', '%':
			c.state = stSkip1
		case 0x1b:
			// ESC ESC: stay
		default:
			c.state = stGround // a two-byte sequence such as ESC = or ESC c
		}
	case stSkip1:
		c.state = stGround
	case stCSI:
		switch {
		case b >= 0x40 && b <= 0x7e:
			c.csi(b)
			c.state = stGround
		case b == '?' || b == '>' || b == '<' || b == '=':
			c.csiQ = true
		case b == 0x1b:
			c.state = stEsc // a broken sequence: start over
		case b < 0x20:
			c.control(b) // controls inside a sequence still act
		default:
			if len(c.params) < 32 {
				c.params = append(c.params, b)
			}
		}
	case stOSC:
		switch {
		case c.osc == oscEsc && b == '\\':
			c.state = stGround
		case c.osc == oscEsc:
			c.osc = oscBody
			if b == 0x1b {
				c.osc = oscEsc
			}
		case b == 0x07:
			c.state = stGround
		case b == 0x1b:
			c.osc = oscEsc
		}
	default:
		if b == 0x1b {
			c.state = stEsc
			return
		}
		if b < 0x20 || b == 0x7f {
			c.control(b)
			return
		}
		c.put(rune(b))
	}
}

func (c *Cleaner) control(b byte) {
	switch b {
	case '\n':
		c.commit()
	case '\r':
		c.cursor = 0
	case '\b':
		if c.cursor > 0 {
			c.cursor--
		}
	case '\t':
		next := (c.cursor/tabWidth + 1) * tabWidth
		for c.cursor < next {
			c.put(' ')
		}
	}
	// everything else (NUL, BEL, ...) is dropped
}

// csi handles a finished control sequence. Only what changes the text of the current line
// matters; colours and modes are dropped.
func (c *Cleaner) csi(final byte) {
	if c.csiQ {
		return
	}
	n := c.param(0)
	switch final {
	case 'K': // erase in line
		switch n {
		case 0:
			if c.cursor < len(c.line) {
				c.line = c.line[:c.cursor]
			}
		case 1:
			for i := 0; i <= c.cursor && i < len(c.line); i++ { // up to and including the cursor
				c.line[i] = ' '
			}
		case 2:
			c.line = c.line[:0]
		}
	case 'J': // erase in display: all that is left of the line is what comes next
		if n == 2 || n == 3 || n == 0 && c.cursor == 0 {
			c.line = c.line[:0]
			c.cursor = 0
		}
	case 'G': // cursor to column
		c.cursor = min(max(n, 1)-1, maxLineRunes)
	case 'C': // right
		c.cursor = min(c.cursor+max(n, 1), maxLineRunes)
	case 'D': // left
		c.cursor = max(c.cursor-max(n, 1), 0)
	case 'P': // delete characters
		k := max(n, 1)
		if c.cursor < len(c.line) {
			end := min(c.cursor+k, len(c.line))
			c.line = append(c.line[:c.cursor], c.line[end:]...)
		}
	}
}

// param returns the first numeric parameter of the sequence, or 0.
func (c *Cleaner) param(i int) int {
	n, idx := 0, 0
	for _, b := range c.params {
		if b == ';' {
			if idx == i {
				return n
			}
			idx++
			n = 0
			continue
		}
		if b >= '0' && b <= '9' && idx == i && n < 1<<20 {
			n = n*10 + int(b-'0')
		}
	}
	if idx == i {
		return n
	}
	return 0
}

// put writes a character at the cursor, over what is there.
func (c *Cleaner) put(r rune) {
	if c.cursor >= maxLineRunes {
		return
	}
	for len(c.line) < c.cursor {
		c.line = append(c.line, ' ')
	}
	if c.cursor < len(c.line) {
		c.line[c.cursor] = r
	} else {
		c.line = append(c.line, r)
	}
	c.cursor++
}

func (c *Cleaner) commit() {
	text := strings.TrimRight(string(c.line), " ")
	c.line, c.cursor = c.line[:0], 0
	if _, err := io.WriteString(c.out, text+"\n"); err != nil {
		c.err = err
	}
}
