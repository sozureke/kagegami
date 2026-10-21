package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

// memRecorder keeps the output of every command in memory.
type memRecorder struct {
	mu       sync.Mutex
	outs     map[int]*memOutput
	beginErr error
	nilFor   map[int]bool
	writeErr error // returned by the first Write of the outputs named in failSeq
	failSeq  map[int]bool
	finErr   error
}

type memOutput struct {
	rec      *memRecorder
	seq      int
	buf      bytes.Buffer
	finished int
	end      End
	wrote    bool
}

func (r *memRecorder) Begin(seq int, _ string) (Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	if r.nilFor[seq] {
		return nil, nil
	}
	if r.outs == nil {
		r.outs = map[int]*memOutput{}
	}
	o := &memOutput{rec: r, seq: seq}
	r.outs[seq] = o
	return o, nil
}

func (o *memOutput) Write(p []byte) (int, error) {
	if o.rec.writeErr != nil && o.rec.failSeq[o.seq] && !o.wrote {
		o.wrote = true
		return 0, o.rec.writeErr
	}
	return o.buf.Write(p)
}

func (o *memOutput) Finish(end End) error {
	o.finished++
	o.end = end
	return o.rec.finErr
}

// run captures stream and returns the events.
func run(t *testing.T, r io.Reader, rec Recorder) ([]Event, error) {
	t.Helper()
	events := make(chan Event, 4)
	var got []Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range events {
			got = append(got, e)
		}
	}()
	err := Capture(context.Background(), r, nonce, rec, events)
	close(events)
	<-done
	return got, err
}

func summary(events []Event) []string {
	var out []string
	for _, e := range events {
		switch e.Kind {
		case CommandStarted:
			out = append(out, fmt.Sprintf("started %d %q", e.Seq, e.Cmd))
		case CommandFinished:
			s := fmt.Sprintf("finished %d %q rc=%d aborted=%v alt=%v", e.Seq, e.Cmd, e.End.ExitCode, e.End.Aborted, e.End.AltScreen)
			if e.Err != nil {
				s += " err"
			}
			out = append(out, s)
		}
	}
	return out
}

func TestCaptureReportsCommandsAndRecordsTheirOutput(t *testing.T) {
	stream := "prompt% " + start(1, "echo hi") + "hi\r\n" + end(1, 0) + "prompt% " + start(2, "false") + end(2, 1) + start(3, "cat x") + "partial"
	rec := &memRecorder{}
	events, err := run(t, strings.NewReader(stream), rec)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`started 1 "echo hi"`, `finished 1 "echo hi" rc=0 aborted=false alt=false`,
		`started 2 "false"`, `finished 2 "false" rc=1 aborted=false alt=false`,
		`started 3 "cat x"`, `finished 3 "cat x" rc=0 aborted=true alt=false`,
	}
	if got := summary(events); !reflect.DeepEqual(got, want) {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
	for seq, wantOut := range map[int]string{1: "hi\r\n", 2: "", 3: "partial"} {
		o := rec.outs[seq]
		if o == nil || o.buf.String() != wantOut || o.finished != 1 {
			t.Errorf("command %d: output %q finished %d times, want %q once", seq, o.buf.String(), o.finished, wantOut)
		}
	}
	if !rec.outs[3].end.Aborted {
		t.Error("Finish did not learn that the command was aborted")
	}
	for i, e := range events {
		if e.At.IsZero() || (i > 0 && e.At.Before(events[i-1].At)) {
			t.Errorf("event %d has time %v: times must be set and not go back", i, e.At)
		}
	}
}

func TestCaptureGivesTheSameResultForAnyReadSize(t *testing.T) {
	stream := start(1, "a") + "one\x1b[31m" + end(1, 0) + start(2, "b;c") + strings.Repeat("x", 200000) + end(2, 4)
	whole := &memRecorder{}
	wantEvents, _ := run(t, strings.NewReader(stream), whole)
	for name, reader := range map[string]io.Reader{
		"one byte": iotest.OneByteReader(strings.NewReader(stream)),
		"half":     iotest.HalfReader(strings.NewReader(stream)),
		"data+err": iotest.DataErrReader(strings.NewReader(stream)),
	} {
		t.Run(name, func(t *testing.T) {
			rec := &memRecorder{}
			got, err := run(t, reader, rec)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(summary(got), summary(wantEvents)) {
				t.Errorf("events differ:\n got %q\nwant %q", summary(got), summary(wantEvents))
			}
			for seq, o := range whole.outs {
				if rec.outs[seq].buf.String() != o.buf.String() {
					t.Errorf("output of command %d differs", seq)
				}
			}
		})
	}
}

func TestCaptureKeepsGoingWhenRecordingFails(t *testing.T) {
	boom := errors.New("disk full")
	stream := start(1, "a") + "A" + end(1, 0) + start(2, "b") + "B" + end(2, 0)

	t.Run("Begin fails", func(t *testing.T) {
		got, err := run(t, strings.NewReader(stream), &memRecorder{beginErr: boom})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{`started 1 "a"`, `finished 1 "a" rc=0 aborted=false alt=false err`, `started 2 "b"`, `finished 2 "b" rc=0 aborted=false alt=false err`}
		if !reflect.DeepEqual(summary(got), want) {
			t.Errorf("events:\n got %q\nwant %q", summary(got), want)
		}
		if !errors.Is(got[1].Err, boom) {
			t.Errorf("the cause is lost: %v", got[1].Err)
		}
	})
	t.Run("Write fails", func(t *testing.T) {
		rec := &memRecorder{writeErr: boom, failSeq: map[int]bool{1: true}}
		long := start(1, "a") + "AAA" + end(1, 0) + start(2, "b") + "B" + end(2, 0)
		got, _ := run(t, iotest.OneByteReader(strings.NewReader(long)), rec)
		if len(got) != 4 || got[1].Err == nil || got[3].Err != nil || !errors.Is(got[1].Err, boom) {
			t.Fatalf("events = %q: only the command whose output failed carries the error", summary(got))
		}
		if rec.outs[1].finished != 1 || rec.outs[2].finished != 1 {
			t.Error("a failed output must still be finished, so that its file is closed")
		}
		if rec.outs[1].buf.Len() != 0 {
			t.Errorf("%q was written after the output had failed", rec.outs[1].buf.String())
		}
		if rec.outs[2].buf.String() != "B" {
			t.Error("the next command's output was affected by the failure")
		}
	})
	t.Run("Finish fails", func(t *testing.T) {
		got, _ := run(t, strings.NewReader(stream), &memRecorder{finErr: boom})
		if len(got) != 4 || !errors.Is(got[1].Err, boom) {
			t.Fatalf("events = %q, err %v", summary(got), got[1].Err)
		}
	})
	t.Run("a command that is not recorded", func(t *testing.T) {
		rec := &memRecorder{nilFor: map[int]bool{1: true}}
		got, _ := run(t, strings.NewReader(stream), rec)
		if len(got) != 4 || got[1].Err != nil || rec.outs[1] != nil || rec.outs[2].buf.String() != "B" {
			t.Errorf("events = %q", summary(got))
		}
	})
}

func TestCaptureFlagsFullScreenCommands(t *testing.T) {
	rec := &memRecorder{}
	got, _ := run(t, strings.NewReader(start(1, "less f")+"\x1b[?1049hredraw"+end(1, 0)), rec)
	if len(got) != 2 || !got[1].End.AltScreen || !rec.outs[1].end.AltScreen {
		t.Errorf("events = %q", summary(got))
	}
}

func TestCaptureReportsAReadErrorAndEndsOpenCommands(t *testing.T) {
	boom := errors.New("pipe broke")
	r := io.MultiReader(strings.NewReader(start(1, "a")+"A"), iotest.ErrReader(boom))
	got, err := run(t, r, &memRecorder{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read error", err)
	}
	if want := []string{`started 1 "a"`, `finished 1 "a" rc=0 aborted=true alt=false`}; !reflect.DeepEqual(summary(got), want) {
		t.Errorf("events = %q, want the open command ended as aborted", summary(got))
	}
}

func TestCaptureStopsWhenTheContextIsDone(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()

	rec := &memRecorder{}
	events := make(chan Event, 16)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Capture(ctx, pr, nonce, rec, events) }()

	_, _ = pw.WriteString(start(1, "sleep 99") + "so far")
	select {
	case e := <-events:
		if e.Kind != CommandStarted {
			t.Fatalf("unexpected event %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Capture did not return after the context was cancelled")
	}
	if o := rec.outs[1]; o == nil || o.finished != 1 || !o.end.Aborted {
		t.Error("the open command's output was not finished as aborted")
	}
	if _, err := pr.Read(make([]byte, 1)); err == nil {
		t.Error("the stream was not closed")
	}
}

func TestCaptureDoesNotHangOnAFullEventChannel(t *testing.T) {
	// nobody reads events: Capture must still return once the context ends
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event) // unbuffered, never read
	rec := &memRecorder{}
	result := make(chan error, 1)
	go func() {
		result <- Capture(ctx, strings.NewReader(start(1, "a")+"A"+end(1, 0)), nonce, rec, events)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Capture is stuck on a send nobody receives")
	}
	if o := rec.outs[1]; o == nil || o.finished != 1 {
		t.Error("the output was not finished although the event could not be delivered")
	}
}

func TestCaptureHandlesALotOfOutputWithoutKeepingIt(t *testing.T) {
	const lines, perLine = 200_000, 50
	line := strings.Repeat("x", perLine-2) + "\r\n"
	var counted countingOutput
	rec := recorderFunc(func(int, string) (Output, error) { return &counted, nil })
	r := io.MultiReader(strings.NewReader(start(1, "seq")), &repeatReader{s: line, n: lines}, strings.NewReader(end(1, 0)))
	got, err := run(t, r, rec)
	if err != nil || len(got) != 2 {
		t.Fatalf("err %v, %d events", err, len(got))
	}
	if counted.n != lines*perLine {
		t.Errorf("recorded %d bytes, want %d", counted.n, lines*perLine)
	}
}

type recorderFunc func(int, string) (Output, error)

func (f recorderFunc) Begin(seq int, cmd string) (Output, error) { return f(seq, cmd) }

type countingOutput struct{ n int }

func (c *countingOutput) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }
func (c *countingOutput) Finish(End) error            { return nil }

type repeatReader struct {
	s   string
	n   int
	off int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.off:])
	if r.off += n; r.off == len(r.s) {
		r.off, r.n = 0, r.n-1
	}
	return n, nil
}

func TestCaptureReportsNothingAfterTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// a select with two ready cases chooses at random, so one try could pass by luck
	for range 50 {
		events := make(chan Event, 16) // room for everything: only the context can stop the events
		rec := &memRecorder{}
		err := Capture(ctx, strings.NewReader(start(1, "a")+"A"+end(1, 0)+start(2, "b")+"B"), nonce, rec, events)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("%d events were delivered after the context was done", len(events))
		}
		for seq, o := range rec.outs {
			if o.finished != 1 {
				t.Errorf("output of command %d was not finished", seq)
			}
		}
	}
}
