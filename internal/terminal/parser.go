package terminal

import (
	"bytes"
	"crypto/subtle"
	"strconv"
)

// Sink receives what the Parser finds in the stream.
type Sink interface {
	// Started is called when a command begins. cmd is the command line as the hook saw it.
	Started(id int, cmd string)
	// Output carries the bytes the command printed, in order. The slice is only valid during
	// the call.
	Output(data []byte)
	// Ended is called when the command is over.
	Ended(id int, end End)
}

// End describes how a command finished.
type End struct {
	ExitCode int
	// Aborted is set when no end marker came: another command started, or the stream ended.
	// ExitCode is meaningless then.
	Aborted bool
	// AltScreen is set when the command switched to the alternate screen (a full-screen
	// program). Its output is screen redraws, not text, and the parser stops passing it on
	// once it has noticed, so what was delivered must be ignored.
	AltScreen bool
}

const (
	// markerPrefix starts every marker of ADR-0006.
	markerPrefix = "\x1b]7771;"
	// maxMarker bounds how many bytes the parser holds back while it waits for the BEL that
	// ends a marker. The hook limits the command text, so a real marker is far shorter.
	maxMarker = 20 << 10
	bel       = '\a'
)

// altScreenOn lists the sequences with which a program takes over the whole screen.
var altScreenOn = [][]byte{[]byte("\x1b[?1049h"), []byte("\x1b[?1047h"), []byte("\x1b[?47h")}

// altTail is how many bytes of output are kept to find a sequence cut by a chunk boundary.
const altTail = 7

// Parser cuts the raw stream of a pane at the markers the shell hook prints. Bytes that look
// like a marker but do not carry the right nonce are ordinary output.
//
// The result does not depend on how the stream is split into Write calls.
type Parser struct {
	nonce string
	sink  Sink

	open  bool
	id    int
	alt   bool
	tail  [altTail]byte // last bytes of the open command's output
	tailN int
	pend  []byte // bytes that may be the start of a marker, held until it is clear
}

// NewParser returns a parser that accepts markers carrying nonce.
func NewParser(nonce string, sink Sink) *Parser {
	return &Parser{nonce: nonce, sink: sink}
}

// Write feeds the parser. It never fails.
func (p *Parser) Write(b []byte) (int, error) {
	data := b
	if len(p.pend) > 0 {
		data = append(p.pend, b...)
		p.pend = nil
	}

	run := 0 // data[run:i] is output not yet passed on
	for i := 0; i < len(data); {
		esc := bytes.IndexByte(data[i:], 0x1b)
		if esc < 0 {
			break
		}
		i += esc
		rest := data[i:]

		if len(rest) < len(markerPrefix) {
			if markerPrefix[:len(rest)] == string(rest) {
				p.output(data[run:i])
				p.pend = append([]byte(nil), rest...)
				return len(b), nil
			}
			i++
			continue
		}
		if string(rest[:len(markerPrefix)]) != markerPrefix {
			i++
			continue
		}

		window := rest
		if len(window) > maxMarker {
			window = window[:maxMarker]
		}
		end := bytes.IndexByte(window, bel)
		if end < 0 {
			if len(rest) >= maxMarker {
				i++ // too long to be a marker
				continue
			}
			p.output(data[run:i])
			p.pend = append([]byte(nil), rest...)
			return len(b), nil
		}

		if body := rest[len(markerPrefix):end]; p.marker(body, data[run:i]) {
			i += end + 1
			run = i
			continue
		}
		i++
	}
	p.output(data[run:])
	return len(b), nil
}

// Close ends the stream: bytes held back are output after all, and an open command is
// ended as aborted.
func (p *Parser) Close() {
	if len(p.pend) > 0 {
		held := p.pend
		p.pend = nil
		p.output(held)
	}
	p.finish(End{Aborted: true})
}

// marker handles the text of one marker, and reports whether it was a valid one. before is
// the output that precedes the marker; it is passed on first, but only for a valid marker.
func (p *Parser) marker(body, before []byte) bool {
	kind, id, last, ok := p.fields(body)
	if !ok {
		return false
	}
	p.output(before)
	switch kind {
	case 'C':
		p.finish(End{Aborted: true})
		p.open, p.id, p.alt, p.tailN = true, id, false, 0
		p.sink.Started(id, last)
	case 'D':
		rc, err := strconv.Atoi(last)
		if err != nil || rc < 0 || rc > 255 || !p.open || id != p.id {
			return true // a genuine marker (it has the nonce) that fits nothing: dropped
		}
		p.finish(End{ExitCode: rc})
	}
	return true
}

// fields splits "C;kg=<nonce>;id=<n>;cmd=<text>" and "D;kg=<nonce>;id=<n>;rc=<n>". last is the
// command text or the exit code. The command text is last because it may contain ";".
func (p *Parser) fields(body []byte) (kind byte, id int, last string, ok bool) {
	if len(body) < 2 || body[1] != ';' || (body[0] != 'C' && body[0] != 'D') {
		return 0, 0, "", false
	}
	kind = body[0]
	rest := string(body[2:])

	nonce, rest, ok := cutField(rest, "kg=")
	if !ok || subtle.ConstantTimeCompare([]byte(nonce), []byte(p.nonce)) != 1 {
		return 0, 0, "", false
	}
	idText, rest, ok := cutField(rest, "id=")
	if !ok {
		return 0, 0, "", false
	}
	id, err := strconv.Atoi(idText)
	if err != nil || id <= 0 {
		return 0, 0, "", false
	}
	key := "cmd="
	if kind == 'D' {
		key = "rc="
	}
	if len(rest) < len(key) || rest[:len(key)] != key {
		return 0, 0, "", false
	}
	return kind, id, rest[len(key):], true
}

// cutField takes "key=value;" off the front of s.
func cutField(s, key string) (value, rest string, ok bool) {
	if len(s) < len(key) || s[:len(key)] != key {
		return "", "", false
	}
	s = s[len(key):]
	end := bytes.IndexByte([]byte(s), ';')
	if end < 0 {
		return "", "", false
	}
	return s[:end], s[end+1:], true
}

// output passes bytes to the sink if a command is open and not full-screen. Output ends with
// the sequence that switches to the alternate screen: nothing after it is passed on.
func (p *Parser) output(data []byte) {
	if len(data) == 0 || !p.open || p.alt {
		return
	}

	// The earliest end of such a sequence, counted in data: either inside data, or one that
	// began in the previous output and is completed by data.
	cut := -1
	var buf [2 * altTail]byte
	head := data[:min(len(data), altTail)]
	joined := append(append(buf[:0], p.tail[:p.tailN]...), head...)
	for _, seq := range altScreenOn {
		if i := bytes.Index(data, seq); i >= 0 && (cut < 0 || i+len(seq) < cut) {
			cut = i + len(seq)
		}
		if i := bytes.Index(joined, seq); i >= 0 && i < p.tailN && (cut < 0 || i+len(seq)-p.tailN < cut) {
			cut = i + len(seq) - p.tailN
		}
	}
	if cut >= 0 {
		p.sink.Output(data[:cut])
		p.alt = true
		return
	}
	p.sink.Output(data)

	if len(data) >= altTail {
		p.tailN = copy(p.tail[:], data[len(data)-altTail:])
		return
	}
	keep := append(append(buf[:0], p.tail[:p.tailN]...), data...)
	if len(keep) > altTail {
		keep = keep[len(keep)-altTail:]
	}
	p.tailN = copy(p.tail[:], keep)
}

// finish ends the open command, if any.
func (p *Parser) finish(end End) {
	if !p.open {
		return
	}
	end.AltScreen = p.alt
	p.open = false
	p.sink.Ended(p.id, end)
}
