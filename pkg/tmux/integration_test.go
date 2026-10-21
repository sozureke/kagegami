package tmux

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// requireTmux finds tmux, or skips (or fails, when KAGEGAMI_REQUIRE_TMUX is set, as it is on
// the Linux CI job) so that tests which need a real tmux cannot be skipped silently there.
func requireTmux(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("KAGEGAMI_REQUIRE_TMUX") != "" {
			t.Fatalf("tmux is required (KAGEGAMI_REQUIRE_TMUX is set) but is not installed: %v", err)
		}
		t.Skip("tmux is not installed: skipping the tests that need a real tmux")
	}
	return path
}

// server is a private tmux server: its own socket in a short temporary directory, no
// configuration file, and an environment without TMUX and TMUX_PANE, so that it can never
// meet the user's tmux, even when the tests run inside one.
type server struct {
	*Client
	runner ExecRunner
	dir    string
	first  PaneID // the pane the first session started with: it plays "the user's pane"
}

func newServer(t *testing.T) *server {
	t.Helper()
	bin := requireTmux(t)

	dir, err := os.MkdirTemp("", "kgt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	if len(sock) > 90 { // sockaddr_un is about 104 bytes on macOS and 108 on Linux
		t.Skipf("the temporary directory path is too long for a unix socket: %s", sock)
	}

	runner := ExecRunner{Binary: bin, GlobalArgs: []string{"-S", sock, "-f", "/dev/null"}, Env: CleanEnv()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = runner.Run(ctx, "kill-server")
	})

	if _, err := runner.Run(bg, "new-session", "-d", "-s", "main", "-x", "120", "-y", "40", "cat"); err != nil {
		t.Fatalf("cannot start a private tmux server: %v", err)
	}
	out, err := runner.Run(bg, "display-message", "-p", "-t", "main", "#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	first, err := ParsePaneID(out)
	if err != nil {
		t.Fatal(err)
	}
	getenv := env(map[string]string{"TMUX": sock + ",1,0", "TMUX_PANE": string(first)})
	return &server{Client: New(runner, getenv), runner: runner, dir: dir, first: first}
}

// path returns a file inside the server's temporary directory.
func (s *server) path(name string) string { return filepath.Join(s.dir, name) }

// catPane starts a pane that echoes what it is typed back, without the terminal echo, so
// the text appears exactly once.
func (s *server) catPane(t *testing.T) PaneID {
	t.Helper()
	id, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true, Command: `stty -echo; exec cat`})
	if err != nil {
		t.Fatalf("SplitWindow: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // let stty run before anything is typed
	return id
}

// eventually polls cond until it holds or the deadline passes. Output travels through
// tmux and a pipe and is never instantaneous.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func fileHas(path, text string) func() bool {
	return func() bool {
		data, _ := os.ReadFile(path)
		return strings.Contains(string(data), text)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

// --- Facts about the installed tmux -------------------------------------------------------

func TestTheInstalledTmux(t *testing.T) {
	s := newServer(t)
	v, err := s.Version(bg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tmux %s (verified: %v)", v, v.Verified())
	if v.Major < 3 {
		t.Errorf("tmux %s is older than anything this package was written against", v)
	}
}

// The property the whole capture design rests on (ADR-0006): pipe-pane hands over the raw
// bytes of the pane, including OSC sequences that tmux itself does not interpret.
func TestPipePaneCarriesRawOSCMarkers(t *testing.T) {
	s := newServer(t)
	marker := "\x1b]7771;C;kg=test;id=1;cmd=ls\x07"
	id, err := s.SplitWindow(bg, SplitOptions{
		Target: s.first, KeepFocus: true,
		Command: `stty -echo; read x; printf '\033]7771;C;kg=test;id=1;cmd=ls\007output\n'; sleep 30`,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := s.path("raw.log")
	if err := s.PipePane(bg, id, "cat >> "+ShellQuote(out)); err != nil {
		t.Fatal(err)
	}
	if err := s.SendText(bg, id, "", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the marker and the output in the pipe", fileHas(out, "output"))

	got := readFile(t, out)
	if !strings.Contains(got, marker) {
		t.Errorf("the raw marker did not come through pipe-pane unchanged:\n%q", got)
	}
	if strings.Index(got, marker) > strings.Index(got, "output") {
		t.Errorf("the marker must come before the output it announces: %q", got)
	}
}

// --- Panes --------------------------------------------------------------------------------

func TestSplitWindowMakesAPaneNextToTheTarget(t *testing.T) {
	s := newServer(t)

	w0, h0, err := s.PaneSize(bg, s.first)
	if err != nil {
		t.Fatal(err)
	}
	side, err := s.SplitWindow(bg, SplitOptions{Target: s.first, Horizontal: true, Percent: 40, KeepFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	w1, h1, err := s.PaneSize(bg, side)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h0 {
		t.Errorf("a side-by-side pane has height %d, want the window's %d", h1, h0)
	}
	if want := w0 * 40 / 100; w1 < want-4 || w1 > want+4 {
		t.Errorf("width = %d, want about 40%% of %d (%d)", w1, w0, want)
	}
	if wLeft, _, _ := s.PaneSize(bg, s.first); wLeft+w1+1 != w0 {
		t.Errorf("the panes %d + %d + 1 border do not fill the width %d", wLeft, w1, w0)
	}

	below, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	if wb, _, _ := s.PaneSize(bg, below); wb != func() int { w, _, _ := s.PaneSize(bg, s.first); return w }() {
		t.Errorf("a pane above/below has width %d, want the same as its target", wb)
	}
	if side == below || side == s.first || below == s.first {
		t.Errorf("pane ids must differ: %s %s %s", s.first, side, below)
	}
}

func TestSplitWindowKeepFocus(t *testing.T) {
	s := newServer(t)
	active := func() string {
		out, err := s.runner.Run(bg, "display-message", "-p", "-t", "main", "#{pane_id}")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true}); err != nil {
		t.Fatal(err)
	}
	if got := active(); got != string(s.first) {
		t.Errorf("active pane = %s, want the original %s: KeepFocus must not steal the focus", got, s.first)
	}
	moved, err := s.SplitWindow(bg, SplitOptions{Target: s.first})
	if err != nil {
		t.Fatal(err)
	}
	if got := active(); got != string(moved) {
		t.Errorf("active pane = %s, want the new %s when the focus is not kept", got, moved)
	}
}

func TestSplitWindowRunsTheCommand(t *testing.T) {
	s := newServer(t)
	cmd := "echo " + ShellQuote("it's a b") + `; echo "$((6*7))"; sleep 30`
	id, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true, Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the command's output on the screen", func() bool {
		scr, _ := s.CapturePane(bg, id, CaptureOptions{})
		return strings.Contains(scr, "it's a b") && strings.Contains(scr, "42")
	})
}

func TestSplitWindowOfAMissingPane(t *testing.T) {
	s := newServer(t)
	if _, err := s.SplitWindow(bg, SplitOptions{Target: "%99"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if len(s.owned) != 0 {
		t.Error("a failed split must not leave a pane marked as ours")
	}
}

func TestPaneExistsAndKillPane(t *testing.T) {
	s := newServer(t)
	id, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.PaneExists(bg, id); !ok || err != nil {
		t.Fatalf("PaneExists(new) = %v, %v", ok, err)
	}
	if ok, err := s.PaneExists(bg, "%99"); ok || err != nil {
		t.Errorf("PaneExists(missing) = %v, %v; want false and no error", ok, err)
	}

	if err := s.KillPane(bg, id); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.PaneExists(bg, id); ok {
		t.Error("the pane is still there after KillPane")
	}
	if ok, _ := s.PaneExists(bg, s.first); !ok {
		t.Error("the original pane must be untouched")
	}
}

func TestKillPaneRefusesThePanesItDidNotCreate(t *testing.T) {
	s := newServer(t)
	foreign, err := s.runner.Run(bg, "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", string(s.first), "cat")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []PaneID{PaneID(foreign), s.first} {
		if err := s.KillPane(bg, id); !errors.Is(err, ErrNotOwned) {
			t.Errorf("KillPane(%s) err = %v, want ErrNotOwned", id, err)
		}
		if ok, _ := s.PaneExists(bg, id); !ok {
			t.Errorf("pane %s was killed although it is not ours", id)
		}
	}
}

func TestKillPaneOfAPaneTheUserAlreadyClosed(t *testing.T) {
	s := newServer(t)
	id, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.runner.Run(bg, "kill-pane", "-t", string(id)); err != nil { // the user closes it
		t.Fatal(err)
	}
	if err := s.KillPane(bg, id); err != nil {
		t.Errorf("a pane that is already gone is not an error: %v", err)
	}
}

func TestErrorsFromARealServer(t *testing.T) {
	s := newServer(t)
	for name, call := range map[string]func() error{
		"PipePane":    func() error { return s.PipePane(bg, "%99", "cat") },
		"StopPipe":    func() error { return s.StopPipe(bg, "%99") },
		"CapturePane": func() error { _, err := s.CapturePane(bg, "%99", CaptureOptions{}); return err },
		"SendText":    func() error { return s.SendText(bg, "%99", "x", true) },
		"PaneSize":    func() error { _, _, err := s.PaneSize(bg, "%99"); return err },
		"Piped":       func() error { _, err := s.Piped(bg, "%99"); return err },
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a missing pane: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestNoServer(t *testing.T) {
	bin := requireTmux(t)
	dir, err := os.MkdirTemp("", "kgn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	c := New(ExecRunner{Binary: bin, GlobalArgs: []string{"-S", filepath.Join(dir, "none"), "-f", "/dev/null"}, Env: CleanEnv()}, env(nil))
	if _, err := c.PaneExists(bg, "%0"); !errors.Is(err, ErrNoServer) {
		t.Errorf("err = %v, want ErrNoServer", err)
	}
	if _, err := c.Version(bg); err != nil {
		t.Errorf("tmux -V does not need a server: %v", err)
	}
}

// --- Pipes --------------------------------------------------------------------------------

func TestPipeLifecycle(t *testing.T) {
	s := newServer(t)
	id := s.catPane(t)
	out := s.path("pipe.log")

	if on, err := s.Piped(bg, id); err != nil || on {
		t.Fatalf("Piped before = %v, %v; want false", on, err)
	}
	if err := s.PipePane(bg, id, "cat >> "+ShellQuote(out)); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.Piped(bg, id); !on {
		t.Error("Piped after PipePane = false")
	}

	if err := s.SendText(bg, id, "while piped", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "text in the pipe", fileHas(out, "while piped"))

	if err := s.StopPipe(bg, id); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.Piped(bg, id); on {
		t.Error("Piped after StopPipe = true")
	}
	if err := s.SendText(bg, id, "after stop", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if strings.Contains(readFile(t, out), "after stop") {
		t.Error("the pipe kept receiving output after StopPipe")
	}
	if err := s.StopPipe(bg, id); err != nil {
		t.Errorf("stopping a pipe that is not open must not fail: %v", err)
	}
}

// Calling PipePane again replaces the pipe: the old one stops, the new one receives
// everything exactly once. (With tmux's -o flag the second call would close the pipe.)
func TestPipePaneTwiceReplacesThePipe(t *testing.T) {
	s := newServer(t)
	id := s.catPane(t)
	first, second := s.path("first.log"), s.path("second.log")

	if err := s.PipePane(bg, id, "cat >> "+ShellQuote(first)); err != nil {
		t.Fatal(err)
	}
	if err := s.PipePane(bg, id, "cat >> "+ShellQuote(second)); err != nil {
		t.Fatal(err)
	}
	if on, _ := s.Piped(bg, id); !on {
		t.Fatal("the second PipePane closed the pipe instead of replacing it")
	}
	if err := s.SendText(bg, id, "who gets this", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "text in the second pipe", fileHas(second, "who gets this"))
	time.Sleep(300 * time.Millisecond)

	if got := strings.Count(readFile(t, second), "who gets this"); got != 1 {
		t.Errorf("the second pipe received the text %d times, want once", got)
	}
	if strings.Contains(readFile(t, first), "who gets this") {
		t.Error("the replaced pipe is still receiving output")
	}
}

func TestEveryPaneHasItsOwnPipe(t *testing.T) {
	s := newServer(t)
	a, b := s.catPane(t), s.catPane(t)
	fa, fb := s.path("a.log"), s.path("b.log")
	if err := s.PipePane(bg, a, "cat >> "+ShellQuote(fa)); err != nil {
		t.Fatal(err)
	}
	if err := s.PipePane(bg, b, "cat >> "+ShellQuote(fb)); err != nil {
		t.Fatal(err)
	}
	if err := s.SendText(bg, a, "for-a", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SendText(bg, b, "for-b", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both", func() bool { return fileHas(fa, "for-a")() && fileHas(fb, "for-b")() })
	if strings.Contains(readFile(t, fa), "for-b") || strings.Contains(readFile(t, fb), "for-a") {
		t.Error("output of one pane reached the pipe of another")
	}
}

// --- Screen and keys ----------------------------------------------------------------------

func TestCapturePane(t *testing.T) {
	s := newServer(t)
	id, err := s.SplitWindow(bg, SplitOptions{
		Target: s.first, KeepFocus: true,
		Command: `printf 'first line\n\033[31mred text\033[0m\n'; seq 1 200; sleep 30`,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the screen to fill", func() bool {
		scr, _ := s.CapturePane(bg, id, CaptureOptions{})
		return strings.Contains(scr, "200")
	})

	plain, err := s.CapturePane(bg, id, CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "first line") {
		t.Error("the visible screen should have scrolled past the first line")
	}
	if strings.Contains(plain, "\x1b[") {
		t.Error("escape sequences must be left out unless asked for")
	}

	history, err := s.CapturePane(bg, id, CaptureOptions{Start: -1000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history, "first line") || !strings.Contains(history, "red text") {
		t.Error("a negative Start must reach into the scrollback")
	}
	if !strings.Contains(history, "\n1\n") && !strings.HasPrefix(history, "1\n") && !strings.Contains(history, "\n1 ") {
		t.Logf("history starts: %.80q", history)
	}

	coloured, err := s.CapturePane(bg, id, CaptureOptions{Start: -1000, Escapes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(coloured, "\x1b[31m") {
		t.Error("Escapes must keep the colour sequence")
	}
}

func TestSendTextIsLiteral(t *testing.T) {
	s := newServer(t)
	id := s.catPane(t)
	out := s.path("keys.log")
	if err := s.PipePane(bg, id, "cat >> "+ShellQuote(out)); err != nil {
		t.Fatal(err)
	}

	texts := []string{
		`$HOME "quoted" 'single' ; echo hacked #`,
		`-n -- -l`,
		"Enter",
		"C-c",
		`back\slash and %`,
		"unicode ✓ \U0001F680",
	}
	for _, text := range texts {
		if err := s.SendText(bg, id, text, true); err != nil {
			t.Fatalf("SendText(%q): %v", text, err)
		}
	}
	last := texts[len(texts)-1]
	eventually(t, "the last line", fileHas(out, last))

	got := strings.ReplaceAll(readFile(t, out), "\r\n", "\n")
	want := strings.Join(texts, "\n") + "\n"
	if got != want {
		t.Errorf("the pane received\n%q\nwant\n%q", got, want)
	}
	// the pane must still be alive: C-c as text is not the key C-c
	if ok, _ := s.PaneExists(bg, id); !ok {
		t.Error("the pane died: some text was interpreted as a key or a command")
	}
}

func TestSendTextWithoutEnterDoesNotEndTheLine(t *testing.T) {
	s := newServer(t)
	id := s.catPane(t)
	out := s.path("noenter.log")
	if err := s.PipePane(bg, id, "cat >> "+ShellQuote(out)); err != nil {
		t.Fatal(err)
	}
	if err := s.SendText(bg, id, "typed", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if strings.Contains(readFile(t, out), "typed") {
		t.Error("cat answered before Enter was pressed")
	}
	if err := s.SendText(bg, id, "", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the line after Enter", fileHas(out, "typed"))
}

// --- Concurrency and cancellation ---------------------------------------------------------

func TestSplitAndKillConcurrentlyOnARealServer(t *testing.T) {
	s := newServer(t)
	const n = 3 // a 40-row window has no room for more panes stacked on one another
	var wg sync.WaitGroup
	ids := make(chan PaneID, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	var all []string
	for id := range ids {
		all = append(all, string(id))
	}
	slices.Sort(all)
	if len(slices.Compact(slices.Clone(all))) != n {
		t.Fatalf("ids are not unique: %v", all)
	}
	for _, id := range all {
		if err := s.KillPane(bg, PaneID(id)); err != nil {
			t.Errorf("KillPane(%s): %v", id, err)
		}
	}
	out, err := s.runner.Run(bg, "list-panes", "-F", "#{pane_id}")
	if err != nil || out != string(s.first) {
		t.Errorf("panes left = %q, %v; want only the original %s", out, err, s.first)
	}
}

func TestACancelledContextStopsARealCommand(t *testing.T) {
	s := newServer(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := s.PaneExists(ctx, s.first); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if _, err := s.Version(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestPaneIDsAreNumbers(t *testing.T) {
	s := newServer(t)
	id, err := s.SplitWindow(bg, SplitOptions{Target: s.first, KeepFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(string(id), "%")); err != nil || n <= 0 {
		t.Errorf("pane id %q is not %%N", id)
	}
	if bytes.ContainsAny([]byte(id), "\n\r \t") {
		t.Errorf("pane id %q has whitespace", id)
	}
}
