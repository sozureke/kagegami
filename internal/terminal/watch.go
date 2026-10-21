package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sozureke/kagegami/pkg/tmux"
)

var (
	// ErrPaneBusy means the pane already pipes its output somewhere. Kagegami does not take
	// the pipe over: it belongs to the user (or another program).
	ErrPaneBusy = errors.New("the pane already pipes its output elsewhere")
	// ErrPaneGone means the pane was closed while it was watched.
	ErrPaneGone = errors.New("the pane no longer exists")

	errClosed = errors.New("watch closed")
)

// Pipe is the part of tmux that Watch needs. *tmux.Client implements it.
type Pipe interface {
	PaneExists(ctx context.Context, pane tmux.PaneID) (bool, error)
	Piped(ctx context.Context, pane tmux.PaneID) (bool, error)
	PipePane(ctx context.Context, pane tmux.PaneID, shellCommand string) error
	StopPipe(ctx context.Context, pane tmux.PaneID) error
}

// WatchConfig configures Watch.
type WatchConfig struct {
	Pane     tmux.PaneID
	RunDir   string
	Pipe     Pipe
	Recorder Recorder
	// PollInterval is how often the pane is checked for being still there; zero means one
	// second.
	PollInterval time.Duration
	// SilentAfter is how much output without any marker raises HookSilent; zero means the
	// default and a negative value switches the warning off.
	SilentAfter int
}

// Watcher watches one pane.
type Watcher struct {
	events chan Event
	cancel context.CancelCauseFunc
	done   chan struct{}
	ctx    context.Context

	mu  sync.Mutex
	err error
}

// Events delivers what happens in the pane, in order. It is closed when the watch is over;
// the receiver must keep reading until then.
func (w *Watcher) Events() <-chan Event { return w.events }

// Wait blocks until the watch is over and says why: nil after Close, ErrPaneGone if the pane
// went away, the context's error if the context ended, or the failure that stopped it.
func (w *Watcher) Wait() error {
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close ends the watch and waits for the clean-up: the nonce file and the pipe are gone, the
// open command (if any) is reported as aborted. It may be called more than once.
func (w *Watcher) Close() error {
	w.cancel(errClosed)
	return w.Wait()
}

func fifoName(pane tmux.PaneID) string { return strings.TrimPrefix(string(pane), "%") + ".fifo" }

// Watch starts capturing the commands of pane. The order matters and is part of the contract:
// the pipe is open before the nonce file appears, so that the first marked command is not
// lost; and the nonce file goes first when the watch ends, so that the hook stops marking
// before the pipe closes. Whatever the failure, nothing is left behind.
//
// The output of the pane flows through a named pipe in RunDir (a file would grow without
// bound). If the process is killed, the nonce file and the pipe stay and are replaced by the
// next watch.
func Watch(ctx context.Context, cfg WatchConfig) (*Watcher, error) {
	if _, err := tmux.ParsePaneID(string(cfg.Pane)); err != nil {
		return nil, err
	}
	if ok, err := cfg.Pipe.PaneExists(ctx, cfg.Pane); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPaneGone, cfg.Pane)
	}
	if busy, err := cfg.Pipe.Piped(ctx, cfg.Pane); err != nil {
		return nil, err
	} else if busy {
		return nil, fmt.Errorf("%w: %s", ErrPaneBusy, cfg.Pane)
	}
	if err := prepareRunDir(cfg.RunDir); err != nil {
		return nil, err
	}
	nonce, err := NewNonce()
	if err != nil {
		return nil, err
	}

	fifo := filepath.Join(cfg.RunDir, fifoName(cfg.Pane))
	r, err := openFIFO(fifo)
	if err != nil {
		return nil, err
	}
	var nonceFile *NonceFile
	var piped bool
	cleanup := func() error {
		var errs []error
		if nonceFile != nil {
			errs = append(errs, nonceFile.Remove()) // the hook stops marking
		}
		if piped {
			stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := cfg.Pipe.StopPipe(stop, cfg.Pane); err != nil && !errors.Is(err, tmux.ErrNotFound) {
				errs = append(errs, fmt.Errorf("stop the pipe: %w", err))
			}
			cancel()
		}
		_ = r.Close()
		if err := os.Remove(fifo); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove the pipe file: %w", err))
		}
		return errors.Join(errs...)
	}

	if err := cfg.Pipe.PipePane(ctx, cfg.Pane, "cat > "+tmux.ShellQuote(fifo)); err != nil {
		return nil, errors.Join(fmt.Errorf("start piping the pane: %w", err), cleanup())
	}
	piped = true
	if nonceFile, err = WriteNonceFile(cfg.RunDir, cfg.Pane, nonce); err != nil {
		return nil, errors.Join(err, cleanup())
	}

	wctx, cancel := context.WithCancelCause(ctx)
	w := &Watcher{events: make(chan Event, 64), cancel: cancel, done: make(chan struct{}), ctx: wctx}
	silent := cfg.SilentAfter
	if silent == 0 {
		silent = defaultSilentAfter
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = time.Second
	}

	go w.watchPane(cfg, poll)
	go func() {
		defer close(w.done)
		runErr := capture(wctx, r, nonce, cfg.Recorder, w.events, silent)
		cancel(errClosed) // whatever ended the capture, the pane watcher can stop
		cleanErr := cleanup()
		close(w.events)

		w.mu.Lock()
		w.err = errors.Join(w.reason(runErr), cleanErr)
		w.mu.Unlock()
	}()
	return w, nil
}

// reason turns how capture ended into the result of the watch.
func (w *Watcher) reason(runErr error) error {
	cause := context.Cause(w.ctx)
	switch {
	case errors.Is(cause, ErrPaneGone):
		return cause
	case errors.Is(cause, errClosed):
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			return runErr
		}
		return nil
	case runErr != nil:
		return runErr
	default:
		return cause
	}
}

// watchPane ends the watch when the pane goes away: the named pipe is held open by Kagegami
// itself, so the stream never ends on its own.
func (w *Watcher) watchPane(cfg WatchConfig, poll time.Duration) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-t.C:
			ok, err := cfg.Pipe.PaneExists(w.ctx, cfg.Pane)
			if w.ctx.Err() != nil {
				return
			}
			if err != nil || !ok {
				w.cancel(ErrPaneGone)
				return
			}
		}
	}
}

// fifoReader reads a named pipe that this process also holds open for writing, so that the
// stream does not end when the writer does. A read that waits for data cannot be interrupted
// by closing such a file, so Close first writes one byte into the pipe to wake the reader up;
// what is read after Close was called is dropped.
type fifoReader struct {
	f       *os.File
	path    string
	closing atomic.Bool
}

func (r *fifoReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if r.closing.Load() {
		return 0, io.EOF
	}
	return n, err
}

func (r *fifoReader) Close() error {
	if r.closing.Swap(true) {
		return nil
	}
	// Without waiting: if the pipe is full, the reader is not waiting for data either.
	if w, err := os.OpenFile(r.path, os.O_WRONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0); err == nil { //nolint:gosec // in the private run directory
		_, _ = w.Write([]byte{0})
		_ = w.Close()
	}
	return r.f.Close()
}

// openFIFO makes a named pipe at path (replacing whatever was left there, never following a
// link) and opens it for reading and writing, so that opening does not wait for a writer and
// the stream does not end when the writer does.
func openFIFO(path string) (*fifoReader, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove the old pipe file: %w", err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return nil, fmt.Errorf("create the pipe file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0) //nolint:gosec // in the private run directory
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("open the pipe file: %w", err)
	}
	return &fifoReader{f: f, path: path}, nil
}
