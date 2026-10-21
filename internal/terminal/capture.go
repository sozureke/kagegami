package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Output receives the bytes one command printed, as they arrive.
type Output interface {
	io.Writer
	// Finish is called once when the command is over, also for an aborted one. Output that
	// belongs to a full-screen program (end.AltScreen) is not meant to be kept.
	Finish(end End) error
}

// Recorder decides where the output of commands goes.
type Recorder interface {
	// Begin is called when a command starts. Returning a nil Output records nothing for it.
	Begin(seq int, cmd string) (Output, error)
}

// EventKind says what an Event reports.
type EventKind int

const (
	// CommandStarted means a command began running.
	CommandStarted EventKind = iota + 1
	// CommandFinished means it ended.
	CommandFinished
	// HookSilent means a good deal of output went by and no command was ever marked: the
	// shell hook is probably not loaded in this pane. It is reported once, as a warning.
	HookSilent
)

// Event is what Capture reports about commands, in order.
type Event struct {
	Kind EventKind
	// Seq is the number the shell hook gave the command; it counts up within a shell.
	Seq int
	Cmd string
	At  time.Time
	// End says how the command finished. Only for CommandFinished.
	End End
	// Err is set if the output of the command could not be recorded completely. The
	// command itself was still seen, and capture goes on.
	Err error
}

const (
	// readSize is how much is read from the stream at a time.
	readSize = 64 << 10
	// defaultSilentAfter is how much output without a single marker makes Capture warn that
	// the hook is missing. A prompt redraw is a few kilobytes at most.
	defaultSilentAfter = 16 << 10
)

// Capture reads the raw stream of a pane from r and reports commands on events until r
// ends, fails, or ctx is done. Open commands are reported as aborted at the end. It does not
// close events.
//
// A failure to record output never stops the capture: a command that prints a lot must not
// be disturbed by it. The failure is reported in Event.Err.
//
// Capture blocks while events is full, so the receiver must keep reading until Capture
// returns. If r is an io.Closer, it is closed when ctx is done, which is what stops a read
// that waits for data.
func Capture(ctx context.Context, r io.Reader, nonce string, rec Recorder, events chan<- Event) error {
	return capture(ctx, r, nonce, rec, events, defaultSilentAfter)
}

func capture(ctx context.Context, r io.Reader, nonce string, rec Recorder, events chan<- Event, silentAfter int) error {
	if c, ok := r.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { _ = c.Close() })
		defer stop()
	}

	s := &recording{ctx: ctx, rec: rec, events: events, now: time.Now}
	p := NewParser(nonce, s)

	buf := make([]byte, readSize)
	total, warned := 0, false
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = p.Write(buf[:n])
			total += n
			if !warned && silentAfter > 0 && total >= silentAfter && !s.sawCommand {
				warned = true
				s.send(Event{Kind: HookSilent, At: s.now()})
			}
		}
		if s.stopped() {
			p.Close()
			return ctx.Err()
		}
		if err != nil {
			p.Close()
			if ctx.Err() != nil {
				return ctx.Err() // the stream ended because it was closed on purpose
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read the pane stream: %w", err)
		}
	}
}

// recording is the Sink that turns parser calls into events and recorded output.
type recording struct {
	ctx    context.Context
	rec    Recorder
	events chan<- Event
	now    func() time.Time

	seq  int
	cmd  string
	out  Output
	err  error
	gone bool // the receiver went away: ctx is done

	sawCommand bool
}

func (s *recording) stopped() bool { return s.gone }

func (s *recording) send(e Event) {
	if s.gone || s.ctx.Err() != nil {
		if !errors.Is(context.Cause(s.ctx), errClosed) {
			s.gone = true
			return
		}
		// A watch that is being closed still reports what it knows, if there is room: this is
		// how the receiver learns that the open command was aborted.
		select {
		case s.events <- e:
		default:
		}
		return
	}
	select {
	case s.events <- e:
	case <-s.ctx.Done():
		s.gone = true
	}
}

func (s *recording) Started(seq int, cmd string) {
	s.sawCommand = true
	s.seq, s.cmd, s.out, s.err = seq, cmd, nil, nil
	out, err := s.rec.Begin(seq, cmd)
	if err != nil {
		s.err = fmt.Errorf("start recording output: %w", err)
	} else {
		s.out = out
	}
	s.send(Event{Kind: CommandStarted, Seq: seq, Cmd: cmd, At: s.now()})
}

func (s *recording) Output(data []byte) {
	if s.out == nil || s.err != nil {
		return
	}
	if _, err := s.out.Write(data); err != nil {
		s.err = fmt.Errorf("record output: %w", err)
	}
}

func (s *recording) Ended(seq int, end End) {
	err := s.err
	if s.out != nil {
		if ferr := s.out.Finish(end); ferr != nil {
			err = errors.Join(err, fmt.Errorf("finish recording output: %w", ferr))
		}
	}
	s.send(Event{Kind: CommandFinished, Seq: seq, Cmd: s.cmd, At: s.now(), End: end, Err: err})
}
