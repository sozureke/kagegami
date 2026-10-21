package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sozureke/kagegami/pkg/tmux"
)

// fakePipe plays tmux: PipePane really runs the command (so the named pipe is really
// written to), reading from a pipe the test controls.
type fakePipe struct {
	t      *testing.T
	runDir string

	mu                 sync.Mutex
	exists             bool
	existsErr          error
	busy               bool
	pipeErr            error
	stopErr            error
	calls              []string
	stdin              *os.File // the test writes the "pane output" here
	exited             chan struct{}
	noncePresentAtPipe bool
	fifoPresentAtPipe  bool
	nonceAtStop        bool
}

func newFakePipe(t *testing.T, runDir string) *fakePipe {
	return &fakePipe{t: t, runDir: runDir, exists: true}
}

func (f *fakePipe) record(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }

func (f *fakePipe) PaneExists(context.Context, tmux.PaneID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exists, f.existsErr
}

func (f *fakePipe) setExists(v bool) { f.mu.Lock(); f.exists = v; f.mu.Unlock() }

func (f *fakePipe) setExistsErr(err error) { f.mu.Lock(); f.existsErr = err; f.mu.Unlock() }

func (f *fakePipe) Piped(context.Context, tmux.PaneID) (bool, error) {
	f.record("piped")
	return f.busy, nil
}

func (f *fakePipe) PipePane(_ context.Context, _ tmux.PaneID, command string) error {
	f.record("pipe " + command)
	if f.pipeErr != nil {
		return f.pipeErr
	}
	_, nerr := os.Lstat(filepath.Join(f.runDir, "7.nonce"))
	_, ferr := os.Lstat(filepath.Join(f.runDir, "7.fifo"))
	f.noncePresentAtPipe, f.fifoPresentAtPipe = nerr == nil, ferr == nil

	pr, pw, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "sh", "-c", command) //nolint:gosec // the command Watch gave to tmux
	cmd.Stdin = pr
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	_ = pr.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	f.mu.Lock()
	f.stdin, f.exited = pw, exited
	f.mu.Unlock()
	return nil
}

func (f *fakePipe) StopPipe(context.Context, tmux.PaneID) error {
	f.record("stop")
	_, err := os.Lstat(filepath.Join(f.runDir, "7.nonce"))
	f.nonceAtStop = err == nil
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stdin != nil {
		_ = f.stdin.Close() // what tmux does: the command's input ends
		f.stdin = nil
	}
	return f.stopErr
}

// waitCommand waits for the command that PipePane started to end.
func (f *fakePipe) waitCommand() {
	f.mu.Lock()
	exited := f.exited
	if f.stdin != nil {
		_ = f.stdin.Close()
		f.stdin = nil
	}
	f.mu.Unlock()
	if exited == nil {
		return
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		f.t.Error("the pipe command did not end")
	}
}

func (f *fakePipe) pane(text string) {
	f.t.Helper()
	f.mu.Lock()
	w := f.stdin
	f.mu.Unlock()
	if _, err := io.WriteString(w, text); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakePipe) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func newRunDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func nextEvent(t *testing.T, w *Watcher) Event {
	t.Helper()
	select {
	case e, ok := <-w.Events():
		if !ok {
			t.Fatal("the events ended")
		}
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

func readNonce(t *testing.T, runDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "7.nonce"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestWatchCapturesWhatThePaneOutputs(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	rec := &memRecorder{}
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if !pipe.fifoPresentAtPipe || pipe.noncePresentAtPipe {
		t.Errorf("at the time of pipe-pane the named pipe must exist (%v) and the nonce file must not yet (%v)", pipe.fifoPresentAtPipe, pipe.noncePresentAtPipe)
	}
	if got := entries(t, run); len(got) != 2 {
		t.Fatalf("run directory holds %v, want the nonce file and the named pipe", got)
	}
	info, _ := os.Lstat(filepath.Join(run, "7.fifo"))
	if info.Mode()&os.ModeNamedPipe == 0 || info.Mode().Perm() != 0o600 {
		t.Errorf("pipe file mode = %v, want a named pipe with 0600", info.Mode())
	}

	// the hook would use this nonce
	nonce := readNonce(t, run)
	marker := func(kind, rest string) string {
		return "\x1b]7771;" + kind + ";kg=" + nonce + ";id=1;" + rest + "\a"
	}
	pipe.pane("prompt% " + marker("C", "cmd=echo hi") + "hi\r\n" + marker("D", "rc=0"))

	if e := nextEvent(t, w); e.Kind != CommandStarted || e.Cmd != "echo hi" {
		t.Errorf("first event = %+v", e)
	}
	if e := nextEvent(t, w); e.Kind != CommandFinished || e.End.ExitCode != 0 || e.Err != nil {
		t.Errorf("second event = %+v", e)
	}
	if got := rec.outs[1].buf.String(); got != "hi\r\n" {
		t.Errorf("recorded %q", got)
	}
}

func TestWatchLeavesNothingBehindWhenClosed(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	rec := &memRecorder{}
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	nonce := readNonce(t, run)
	pipe.pane("\x1b]7771;C;kg=" + nonce + ";id=1;cmd=sleep 99\a" + "so far")
	_ = nextEvent(t, w) // started

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close has waited for the clean-up: nothing may be left by the time it returns
	if got := entries(t, run); len(got) != 0 {
		t.Errorf("left behind when Close returned: %v", got)
	}
	if got := rec.outs[1].buf.String(); got != "so far" {
		t.Errorf("recorded %q, want exactly %q: the byte that wakes the reader up must not become output", got, "so far")
	}
	if err := w.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
	if err := w.Wait(); err != nil {
		t.Errorf("Wait after Close: %v", err)
	}
	if got := entries(t, run); len(got) != 0 {
		t.Errorf("left behind: %v", got)
	}
	if pipe.called("stop") != 1 {
		t.Errorf("the pipe was stopped %d times, want once", pipe.called("stop"))
	}
	if pipe.nonceAtStop {
		t.Error("the nonce file must be gone before the pipe is stopped, so the hook stops marking first")
	}

	var last Event
	for e := range w.Events() {
		last = e
	}
	if last.Kind != CommandFinished || !last.End.Aborted {
		t.Errorf("the open command was not reported as aborted: %+v", last)
	}
}

func TestWatchEndsWhenThePaneIsClosed(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}, PollInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	pipe.setExists(false)

	done := make(chan error, 1)
	go func() { done <- w.Wait() }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrPaneGone) {
			t.Errorf("Wait = %v, want ErrPaneGone", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not notice that the pane was gone")
	}
	for range w.Events() {
	}
	if got := entries(t, run); len(got) != 0 {
		t.Errorf("left behind: %v", got)
	}
}

func TestWatchEndsWhenTheContextEnds(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	ctx, cancel := context.WithCancel(context.Background())
	w, err := Watch(ctx, WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- w.Wait() }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Wait = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not stop")
	}
	if got := entries(t, run); len(got) != 0 {
		t.Errorf("left behind: %v", got)
	}
}

func TestWatchRefusesWhatItShouldNotTouch(t *testing.T) {
	t.Run("a pane that pipes elsewhere", func(t *testing.T) {
		run := newRunDir(t)
		pipe := newFakePipe(t, run)
		pipe.busy = true
		_, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
		if !errors.Is(err, ErrPaneBusy) {
			t.Fatalf("err = %v, want ErrPaneBusy", err)
		}
		if pipe.called("pipe ") != 0 || len(entries(t, run)) != 0 {
			t.Error("a busy pane was touched")
		}
	})
	t.Run("a pane that does not exist", func(t *testing.T) {
		run := newRunDir(t)
		pipe := newFakePipe(t, run)
		pipe.exists = false
		if _, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}}); !errors.Is(err, ErrPaneGone) {
			t.Errorf("err = %v, want ErrPaneGone", err)
		}
	})
	t.Run("an invalid pane id", func(t *testing.T) {
		run := newRunDir(t)
		pipe := newFakePipe(t, run)
		if _, err := Watch(context.Background(), WatchConfig{Pane: "%7;x", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}}); err == nil {
			t.Error("accepted")
		}
		if pipe.called("") != 0 {
			t.Error("tmux was asked about an invalid pane id")
		}
	})
	t.Run("an unsafe run directory", func(t *testing.T) {
		run := t.TempDir()
		if err := os.Chmod(run, 0o755); err != nil { //nolint:gosec // the point of the test
			t.Fatal(err)
		}
		pipe := newFakePipe(t, run)
		_, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
		if !errors.Is(err, ErrUnsafeDir) {
			t.Fatalf("err = %v, want ErrUnsafeDir", err)
		}
		if pipe.called("pipe ") != 0 || len(entries(t, run)) != 0 {
			t.Error("something was created in an unsafe directory")
		}
	})
}

func TestWatchCleansUpWhenPipingFails(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	boom := errors.New("tmux says no")
	pipe.pipeErr = boom
	_, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the cause", err)
	}
	if got := entries(t, run); len(got) != 0 {
		t.Errorf("left behind: %v", got)
	}
	if pipe.called("stop") != 0 {
		t.Error("a pipe that never started was stopped")
	}
}

func TestWatchReplacesWhatAKilledRunLeftBehind(t *testing.T) {
	run := newRunDir(t)
	if err := os.WriteFile(filepath.Join(run, "7.nonce"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "7.fifo"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the named pipe's place is taken by a link: it must be replaced, not followed
	_ = os.Remove(filepath.Join(run, "7.fifo"))
	if err := os.Symlink(target, filepath.Join(run, "7.fifo")); err != nil {
		t.Fatal(err)
	}

	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if n := readNonce(t, run); n == "stale" || len(n) != 32 {
		t.Errorf("nonce = %q", n)
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Errorf("the link was followed: %q", data)
	}
	pipe.pane("x")
}

func TestWatchDoesNotLoseWhatIsWrittenBeforeTheFirstRead(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}, SilentAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	nonce := readNonce(t, run)
	// a big first burst, one marker at its very end
	pipe.pane(strings.Repeat("noise\r\n", 50_000) + "\x1b]7771;C;kg=" + nonce + ";id=1;cmd=x\a")
	if e := nextEvent(t, w); e.Kind != CommandStarted {
		t.Errorf("event = %+v", e)
	}
}

func TestWatchWarnsOnceWhenNoMarkerEverComes(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}, SilentAfter: 1000})
	if err != nil {
		t.Fatal(err)
	}
	pipe.pane(strings.Repeat("a line without any marker\r\n", 100))
	if e := nextEvent(t, w); e.Kind != HookSilent {
		t.Fatalf("event = %+v, want HookSilent", e)
	}
	pipe.pane(strings.Repeat("more\r\n", 1000))
	time.Sleep(200 * time.Millisecond)
	_ = w.Close()
	for e := range w.Events() {
		t.Errorf("unexpected event after the warning: %+v", e)
	}
}

func TestCaptureDoesNotWarnWhenAMarkerCame(t *testing.T) {
	stream := start(1, "x") + "o" + end(1, 0) + strings.Repeat("noise\r\n", 5000)
	events := make(chan Event, 16)
	if err := capture(context.Background(), strings.NewReader(stream), nonce, &memRecorder{}, events, 1000); err != nil {
		t.Fatal(err)
	}
	close(events)
	for e := range events {
		if e.Kind == HookSilent {
			t.Error("warned although the hook works")
		}
	}
}

func TestCaptureWarnsAfterEnoughSilence(t *testing.T) {
	events := make(chan Event, 16)
	if err := capture(context.Background(), strings.NewReader(strings.Repeat("x", 999)), nonce, &memRecorder{}, events, 1000); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Error("warned too early")
	}
	if err := capture(context.Background(), strings.NewReader(strings.Repeat("x", 1000)), nonce, &memRecorder{}, events, 1000); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || (<-events).Kind != HookSilent {
		t.Error("did not warn at the threshold")
	}
}

func TestWatchWarnsAtTheDefaultThreshold(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	pipe.pane(strings.Repeat("x", defaultSilentAfter-1))
	select {
	case e := <-w.Events():
		t.Fatalf("warned too early: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
	pipe.pane("x")
	if e := nextEvent(t, w); e.Kind != HookSilent {
		t.Errorf("event = %+v, want HookSilent", e)
	}
}

// A run directory is whatever the user's home looks like: spaces and quotes included. The
// command given to tmux runs in a shell, so the path must be quoted.
func TestWatchQuotesThePathOfThePipe(t *testing.T) {
	run := filepath.Join(t.TempDir(), "it's a run dir; echo injected")
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	data, err := os.ReadFile(filepath.Join(run, "7.nonce"))
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.TrimSpace(string(data))
	pipe.pane("\x1b]7771;C;kg=" + nonce + ";id=1;cmd=x\a")
	if e := nextEvent(t, w); e.Kind != CommandStarted {
		t.Errorf("event = %+v: the pipe did not deliver through a path with special characters", e)
	}
}

func TestWatchReportsAFailureToStopThePipe(t *testing.T) {
	boom := errors.New("tmux is gone")
	for name, tc := range map[string]struct {
		err     error
		wantErr bool
	}{
		"a failure":      {boom, true},
		"pane not found": {&tmux.CommandError{Err: tmux.ErrNotFound}, false},
	} {
		t.Run(name, func(t *testing.T) {
			run := newRunDir(t)
			pipe := newFakePipe(t, run)
			pipe.stopErr = tc.err
			w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
			if err != nil {
				t.Fatal(err)
			}
			err = w.Close()
			if tc.wantErr && !errors.Is(err, boom) {
				t.Errorf("Close = %v, want the failure", err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Close = %v: a pane that is already gone is not a failure", err)
			}
			if got := entries(t, run); len(got) != 0 {
				t.Errorf("left behind: %v", got)
			}
		})
	}
}

// If tmux itself disappears, the pane is as good as gone.
func TestWatchTreatsAFailingTmuxAsAGonePane(t *testing.T) {
	run := newRunDir(t)
	pipe := newFakePipe(t, run)
	w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}, PollInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	pipe.setExistsErr(errors.New("no server running"))
	done := make(chan error, 1)
	go func() { done <- w.Wait() }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrPaneGone) {
			t.Errorf("Wait = %v, want ErrPaneGone", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not end")
	}
}

func TestWatchCleansUpWhenTheNonceFileCannotBeWritten(t *testing.T) {
	run := newRunDir(t)
	// something that cannot be replaced is in the nonce file's place: a directory with content
	blocker := filepath.Join(run, "7.nonce")
	if err := os.MkdirAll(filepath.Join(blocker, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	pipe := newFakePipe(t, run)
	if _, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}}); err == nil {
		t.Fatal("Watch succeeded although the nonce file could not be written")
	}
	if pipe.called("stop") != 1 {
		t.Error("the pipe that had been started was not stopped")
	}
	if got := entries(t, run); len(got) != 1 || got[0] != "7.nonce" {
		t.Errorf("left behind: %v (only the blocker may remain)", got)
	}
}

func TestWatcherReason(t *testing.T) {
	boom := errors.New("read failed")
	tests := []struct {
		name   string
		cause  error
		runErr error
		want   error
	}{
		{"closed by the caller", errClosed, context.Canceled, nil},
		{"closed, capture had failed on its own", errClosed, boom, boom},
		{"the pane is gone", ErrPaneGone, context.Canceled, ErrPaneGone},
		{"the parent context ended", context.Canceled, context.Canceled, context.Canceled},
		{"the capture failed", context.Canceled, boom, boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(tt.cause)
			w := &Watcher{ctx: ctx}
			got := w.reason(tt.runErr)
			if tt.want == nil && got != nil || tt.want != nil && !errors.Is(got, tt.want) {
				t.Errorf("reason = %v, want %v", got, tt.want)
			}
		})
	}
}

// The command given to tmux starts a moment after pipe-pane returns. If the watch is closed
// in between, the shell must not create a regular file where the named pipe was.
func TestWatchLeavesNothingWhenStoppedAtOnce(t *testing.T) {
	for range 40 {
		run := newRunDir(t)
		pipe := newFakePipe(t, run)
		w, err := Watch(context.Background(), WatchConfig{Pane: "%7", RunDir: run, Pipe: pipe, Recorder: &memRecorder{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		pipe.waitCommand()
		if got := entries(t, run); len(got) != 0 {
			t.Fatalf("left behind: %v", got)
		}
	}
}
