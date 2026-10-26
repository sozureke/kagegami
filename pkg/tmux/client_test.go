package tmux

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var bg = context.Background()

// fakeRunner records the commands and answers them with fn.
type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	fn    func(args []string) (string, error)
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(args))
	f.mu.Unlock()
	if f.fn == nil {
		return "", nil
	}
	return f.fn(args)
}

func (f *fakeRunner) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func inside(pane string) func(string) string {
	return env(map[string]string{"TMUX": "/tmp/tmux-1000/default,123,0", "TMUX_PANE": pane})
}

func newFake(getenv func(string) string, fn func([]string) (string, error)) (*Client, *fakeRunner) {
	r := &fakeRunner{fn: fn}
	return New(r, getenv), r
}

// --- Pane ids ---------------------------------------------------------------------------

func TestParsePaneID(t *testing.T) {
	for _, ok := range []string{"%0", "%1", "%42", "%123456"} {
		if id, err := ParsePaneID(ok); err != nil || string(id) != ok {
			t.Errorf("ParsePaneID(%q) = %q, %v", ok, id, err)
		}
	}
	bad := []string{
		"", "%", "1", "%a", "%-1", "%1.5", " %1", "%1 ", "%1\n", "\n%1",
		"%1;kill-server", "%1 -a", "%1\x00", "$1", "@1", "main", "main:0.1", "%١", // Arabic-Indic digit one
		"%1%2", "{last}", "%1,2", "=%1", "!", "~",
	}
	for _, s := range bad {
		if _, err := ParsePaneID(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParsePaneID(%q) err = %v, want ErrInvalid", s, err)
		}
	}
}

// Every method that takes a pane must refuse a bad id before it runs tmux.
func TestBadPaneIDsNeverReachTmux(t *testing.T) {
	c, r := newFake(inside("%0"), nil)
	bad := PaneID("%1;kill-server")

	calls := map[string]func() error{
		"SplitWindow": func() error { _, err := c.SplitWindow(bg, SplitOptions{Target: bad}); return err },
		"PipePane":    func() error { return c.PipePane(bg, bad, "cat") },
		"StopPipe":    func() error { return c.StopPipe(bg, bad) },
		"Piped":       func() error { _, err := c.Piped(bg, bad); return err },
		"PaneExists":  func() error { _, err := c.PaneExists(bg, bad); return err },
		"PaneSize":    func() error { _, _, err := c.PaneSize(bg, bad); return err },
		"CapturePane": func() error { _, err := c.CapturePane(bg, bad, CaptureOptions{}); return err },
		"SendText":    func() error { return c.SendText(bg, bad, "x", true) },
		"KillPane":    func() error { return c.KillPane(bg, bad) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s with a bad pane id: err = %v, want ErrInvalid", name, err)
		}
	}
	if n := r.count(); n != 0 {
		t.Errorf("tmux was run %d times for invalid ids: %v", n, r.calls)
	}
}

// --- Environment ------------------------------------------------------------------------

func TestInsideTmux(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"both set", map[string]string{"TMUX": "x", "TMUX_PANE": "%1"}, true},
		{"only TMUX", map[string]string{"TMUX": "x"}, false},
		{"only TMUX_PANE", map[string]string{"TMUX_PANE": "%1"}, false},
		{"neither", map[string]string{}, false},
		{"empty values", map[string]string{"TMUX": "", "TMUX_PANE": ""}, false},
	}
	for _, tt := range tests {
		c, r := newFake(env(tt.env), nil)
		if got := c.InsideTmux(); got != tt.want {
			t.Errorf("%s: InsideTmux = %v, want %v", tt.name, got, tt.want)
		}
		if r.count() != 0 {
			t.Errorf("%s: InsideTmux must not start tmux", tt.name)
		}
	}
}

func TestCurrentPane(t *testing.T) {
	c, _ := newFake(inside("%7"), nil)
	if id, err := c.CurrentPane(); err != nil || id != "%7" {
		t.Errorf("CurrentPane = %q, %v", id, err)
	}
	outside, _ := newFake(env(nil), nil)
	if _, err := outside.CurrentPane(); !errors.Is(err, ErrNotInTmux) {
		t.Errorf("outside tmux err = %v, want ErrNotInTmux", err)
	}
	garbled, _ := newFake(inside("not-a-pane"), nil)
	if _, err := garbled.CurrentPane(); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbled TMUX_PANE err = %v, want ErrInvalid", err)
	}
}

// --- Commands ---------------------------------------------------------------------------

func TestVersionCommand(t *testing.T) {
	c, r := newFake(env(nil), func([]string) (string, error) { return "tmux 3.8", nil })
	v, err := c.Version(bg)
	if err != nil || v != (Version{3, 8, ""}) {
		t.Errorf("Version = %v, %v", v, err)
	}
	if got := r.last(); !slices.Equal(got, []string{"-V"}) {
		t.Errorf("ran %v, want [-V]", got)
	}

	garbage, _ := newFake(env(nil), func([]string) (string, error) { return "something else", nil })
	if _, err := garbage.Version(bg); !errors.Is(err, ErrVersion) {
		t.Errorf("err = %v, want ErrVersion", err)
	}
	boom := errors.New("boom")
	failing, _ := newFake(env(nil), func([]string) (string, error) { return "", boom })
	if _, err := failing.Version(bg); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the runner's error", err)
	}
}

func TestSplitWindowArguments(t *testing.T) {
	tests := []struct {
		name string
		opts SplitOptions
		want []string
	}{
		{"minimal", SplitOptions{Target: "%1"}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1"}},
		{"side by side", SplitOptions{Target: "%1", Horizontal: true}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1", "-h"}},
		{"keep focus", SplitOptions{Target: "%1", KeepFocus: true}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1", "-d"}},
		{"percent", SplitOptions{Target: "%1", Percent: 40}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1", "-l", "40%"}},
		{"command", SplitOptions{Target: "%1", Command: "kagegami panel"}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1", "kagegami panel"}},
		{"directory", SplitOptions{Target: "%1", Dir: "/work/my box"}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1", "-c", "/work/my box"}},
		{"environment", SplitOptions{Target: "%1", Env: []string{"A=1", "KEY=a b=c"}}, []string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%1", "-e", "A=1", "-e", "KEY=a b=c"}},
		{
			"everything",
			SplitOptions{Target: "%3", Horizontal: true, KeepFocus: true, Percent: 35, Env: []string{"K=v"}, Command: "x"},
			[]string{"split-window", "-P", "-F", "#{pane_id}", "-t", "%3", "-h", "-d", "-l", "35%", "-e", "K=v", "x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r := newFake(inside("%0"), func([]string) (string, error) { return "%9", nil })
			id, err := c.SplitWindow(bg, tt.opts)
			if err != nil || id != "%9" {
				t.Fatalf("SplitWindow = %q, %v", id, err)
			}
			if got := r.last(); !slices.Equal(got, tt.want) {
				t.Errorf("ran %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestSplitWindowValidation(t *testing.T) {
	c, r := newFake(inside("%0"), func([]string) (string, error) { return "%9", nil })
	for _, p := range []int{-1, 100, 1000} {
		if _, err := c.SplitWindow(bg, SplitOptions{Target: "%1", Percent: p}); !errors.Is(err, ErrInvalid) {
			t.Errorf("percent %d err = %v, want ErrInvalid", p, err)
		}
	}
	for _, p := range []int{0, 1, 99} {
		if _, err := c.SplitWindow(bg, SplitOptions{Target: "%1", Percent: p}); errors.Is(err, ErrInvalid) {
			t.Errorf("percent %d must be accepted: %v", p, err)
		}
	}
	if r.count() != 3 {
		t.Errorf("tmux ran %d times, want 3 (only for the valid percents)", r.count())
	}
}

func TestSplitWindowRefusesAnEnvironmentThatIsNotNameEqualsValue(t *testing.T) {
	c, r := newFake(inside("%0"), func([]string) (string, error) { return "%9", nil })
	for _, bad := range []string{"", "NOEQUALS", "=v", "1A=v", "A B=v", "A;rm=v", "A-B=v", "A=line\nbreak", "A=nul\x00", "A=cr\r"} {
		if _, err := c.SplitWindow(bg, SplitOptions{Target: "%1", Env: []string{bad}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("env %q: err = %v, want ErrInvalid", bad, err)
		}
	}
	if r.count() != 0 {
		t.Errorf("tmux ran %d times for a bad environment", r.count())
	}
	if _, err := c.SplitWindow(bg, SplitOptions{Target: "%1", Env: []string{"_OK_1=", "a=b"}}); err != nil {
		t.Errorf("an empty value and a lower-case name are fine: %v", err)
	}
}

func TestSplitWindowRejectsAnAnswerThatIsNotAPane(t *testing.T) {
	c, _ := newFake(inside("%0"), func([]string) (string, error) { return "can't do that", nil })
	if _, err := c.SplitWindow(bg, SplitOptions{Target: "%1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
	if len(c.owned) != 0 {
		t.Errorf("a bad answer must not mark anything as created by us: %v", c.owned)
	}
}

func TestPipePaneArguments(t *testing.T) {
	c, r := newFake(inside("%0"), nil)
	if err := c.PipePane(bg, "%2", "cat >> /tmp/x"); err != nil {
		t.Fatal(err)
	}
	// -o is deliberately absent: it toggles
	if got, want := r.last(), []string{"pipe-pane", "-t", "%2", "cat >> /tmp/x"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
	for _, empty := range []string{"", " ", "\t\n"} {
		if err := c.PipePane(bg, "%2", empty); !errors.Is(err, ErrInvalid) {
			t.Errorf("PipePane(%q) err = %v, want ErrInvalid: an empty command closes the pipe", empty, err)
		}
	}
	if err := c.StopPipe(bg, "%2"); err != nil {
		t.Fatal(err)
	}
	if got, want := r.last(), []string{"pipe-pane", "-t", "%2"}; !slices.Equal(got, want) {
		t.Errorf("StopPipe ran %q, want %q", got, want)
	}
}

func TestCapturePaneArguments(t *testing.T) {
	tests := []struct {
		opts CaptureOptions
		want []string
	}{
		{CaptureOptions{}, []string{"capture-pane", "-p", "-t", "%1"}},
		{CaptureOptions{Escapes: true}, []string{"capture-pane", "-p", "-t", "%1", "-e"}},
		{CaptureOptions{Join: true}, []string{"capture-pane", "-p", "-t", "%1", "-J"}},
		{CaptureOptions{Start: -50}, []string{"capture-pane", "-p", "-t", "%1", "-S", "-50"}},
		{CaptureOptions{End: 10}, []string{"capture-pane", "-p", "-t", "%1", "-E", "10"}},
		{CaptureOptions{Escapes: true, Join: true, Start: -5, End: 5}, []string{"capture-pane", "-p", "-t", "%1", "-e", "-J", "-S", "-5", "-E", "5"}},
	}
	for _, tt := range tests {
		c, r := newFake(inside("%0"), nil)
		if _, err := c.CapturePane(bg, "%1", tt.opts); err != nil {
			t.Fatal(err)
		}
		if got := r.last(); !slices.Equal(got, tt.want) {
			t.Errorf("%+v ran %q, want %q", tt.opts, got, tt.want)
		}
	}
}

func TestSendTextArguments(t *testing.T) {
	c, r := newFake(inside("%0"), nil)
	if err := c.SendText(bg, "%1", "-n; rm -rf $HOME", false); err != nil {
		t.Fatal(err)
	}
	if got, want := r.calls, [][]string{{"send-keys", "-t", "%1", "-l", "--", "-n; rm -rf $HOME"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("ran %q, want %q", got, want)
	}
	r.calls = nil
	if err := c.SendText(bg, "%1", "ls", true); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 || !slices.Equal(r.calls[1], []string{"send-keys", "-t", "%1", "Enter"}) {
		t.Errorf("ran %q, want the text and then Enter", r.calls)
	}
}

func TestSendTextStopsAtTheFirstError(t *testing.T) {
	boom := errors.New("boom")
	c, r := newFake(inside("%0"), func([]string) (string, error) { return "", boom })
	if err := c.SendText(bg, "%1", "x", true); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if r.count() != 1 {
		t.Errorf("Enter was sent although typing the text failed (%d calls)", r.count())
	}
}

func TestPaneExistsUsesListPanes(t *testing.T) {
	c, r := newFake(inside("%0"), func(args []string) (string, error) {
		if args[0] == "list-panes" && args[2] == "%5" {
			return "%5", nil
		}
		return "", &CommandError{Args: args, Err: ErrNotFound}
	})
	if ok, err := c.PaneExists(bg, "%5"); !ok || err != nil {
		t.Errorf("existing pane: %v, %v", ok, err)
	}
	if ok, err := c.PaneExists(bg, "%6"); ok || err != nil {
		t.Errorf("missing pane: %v, %v; want false and no error", ok, err)
	}
	if got := r.last(); !slices.Equal(got, []string{"list-panes", "-t", "%6", "-F", "#{pane_id}"}) {
		t.Errorf("ran %q", got)
	}
	boom := errors.New("server exploded")
	failing, _ := newFake(inside("%0"), func([]string) (string, error) { return "", boom })
	if ok, err := failing.PaneExists(bg, "%1"); ok || !errors.Is(err, boom) {
		t.Errorf("other errors must be returned: %v, %v", ok, err)
	}
}

func TestPaneFormatsNeedAnExistingPane(t *testing.T) {
	c, _ := newFake(inside("%0"), func(args []string) (string, error) {
		if args[0] == "list-panes" {
			return "", &CommandError{Args: args, Err: ErrNotFound}
		}
		return "", nil // what display-message does for a missing pane
	})
	if _, err := c.Piped(bg, "%9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Piped on a missing pane: %v, want ErrNotFound", err)
	}
	if _, _, err := c.PaneSize(bg, "%9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("PaneSize on a missing pane: %v, want ErrNotFound", err)
	}
}

func TestPaneSizeParsing(t *testing.T) {
	answer := "120 40"
	c, _ := newFake(inside("%0"), func(args []string) (string, error) {
		if args[0] == "display-message" {
			return answer, nil
		}
		return "%1", nil
	})
	if w, h, err := c.PaneSize(bg, "%1"); err != nil || w != 120 || h != 40 {
		t.Errorf("PaneSize = %d x %d, %v", w, h, err)
	}
	for _, bad := range []string{"", "120", "a b", "120x40"} {
		answer = bad
		if _, _, err := c.PaneSize(bg, "%1"); err == nil {
			t.Errorf("PaneSize accepted %q", bad)
		}
	}
}

func TestPipedAnswers(t *testing.T) {
	answer := "1"
	c, _ := newFake(inside("%0"), func(args []string) (string, error) {
		if args[0] == "display-message" {
			return answer, nil
		}
		return "%1", nil
	})
	for a, want := range map[string]bool{"1": true, "0": false, "": false, "yes": false} {
		answer = a
		if got, err := c.Piped(bg, "%1"); err != nil || got != want {
			t.Errorf("answer %q: Piped = %v, %v; want %v", a, got, err, want)
		}
	}
}

// --- Owning panes: KillPane only closes what SplitWindow made -----------------------------

func splitClient(t *testing.T, currentPane string) (*Client, *fakeRunner) {
	t.Helper()
	next := 10
	c, r := newFake(inside(currentPane), func(args []string) (string, error) {
		if args[0] == "split-window" {
			next++
			return "%" + strconv.Itoa(next), nil
		}
		return "", nil
	})
	return c, r
}

func TestKillPaneClosesOnlyPanesWeCreated(t *testing.T) {
	c, r := splitClient(t, "%0")

	mine, err := c.SplitWindow(bg, SplitOptions{Target: "%0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.KillPane(bg, mine); err != nil {
		t.Fatalf("KillPane of our own pane: %v", err)
	}
	if got := r.last(); !slices.Equal(got, []string{"kill-pane", "-t", string(mine)}) {
		t.Errorf("ran %q", got)
	}

	before := r.count()
	for _, foreign := range []PaneID{"%0", "%1", "%5", "%99"} {
		if err := c.KillPane(bg, foreign); !errors.Is(err, ErrNotOwned) {
			t.Errorf("KillPane(%s) err = %v, want ErrNotOwned", foreign, err)
		}
	}
	if r.count() != before {
		t.Error("tmux ran a kill-pane for a pane that is not ours")
	}
}

func TestKillPaneForgetsThePane(t *testing.T) {
	c, _ := splitClient(t, "%0")
	id, _ := c.SplitWindow(bg, SplitOptions{Target: "%0"})
	if err := c.KillPane(bg, id); err != nil {
		t.Fatal(err)
	}
	// the id may be reused by tmux later; once closed it must not stay closable
	if err := c.KillPane(bg, id); !errors.Is(err, ErrNotOwned) {
		t.Errorf("second KillPane err = %v, want ErrNotOwned", err)
	}
}

func TestKillPaneNeverClosesTheCurrentPane(t *testing.T) {
	c, r := splitClient(t, "%0")
	// even if the pane somehow were on the list, the pane we run in is off limits
	c.owned["%0"] = struct{}{}
	err := c.KillPane(bg, "%0")
	if !errors.Is(err, ErrNotOwned) || !strings.Contains(err.Error(), "runs in") {
		t.Errorf("err = %v, want a refusal that names the current pane", err)
	}
	if r.count() != 0 {
		t.Error("tmux was asked to kill the user's own pane")
	}
}

func TestKillPaneOfAPaneThatIsAlreadyGone(t *testing.T) {
	gone := false
	c, _ := newFake(inside("%0"), func(args []string) (string, error) {
		switch args[0] {
		case "split-window":
			return "%11", nil
		case "kill-pane":
			if gone {
				return "", &CommandError{Args: args, Err: ErrNotFound}
			}
		}
		return "", nil
	})
	id, _ := c.SplitWindow(bg, SplitOptions{Target: "%0"})
	gone = true // the user closed it first
	if err := c.KillPane(bg, id); err != nil {
		t.Errorf("a pane that is already gone is not an error: %v", err)
	}
	if err := c.KillPane(bg, id); !errors.Is(err, ErrNotOwned) {
		t.Errorf("it must be forgotten as well: %v", err)
	}
}

func TestKillPaneKeepsTheOwnershipWhenTmuxFails(t *testing.T) {
	fail := true
	boom := errors.New("tmux hiccup")
	c, _ := newFake(inside("%0"), func(args []string) (string, error) {
		switch args[0] {
		case "split-window":
			return "%11", nil
		case "kill-pane":
			if fail {
				return "", boom
			}
		}
		return "", nil
	})
	id, _ := c.SplitWindow(bg, SplitOptions{Target: "%0"})
	if err := c.KillPane(bg, id); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	fail = false
	if err := c.KillPane(bg, id); err != nil {
		t.Errorf("after a failed attempt the pane must still be ours: %v", err)
	}
}

func TestOwnershipIsPerClient(t *testing.T) {
	a, _ := newFake(inside("%0"), func([]string) (string, error) { return "%11", nil })
	b, rb := newFake(inside("%0"), nil)
	id, _ := a.SplitWindow(bg, SplitOptions{Target: "%0"})
	if err := b.KillPane(bg, id); !errors.Is(err, ErrNotOwned) {
		t.Errorf("another client closed a pane it did not create: %v", err)
	}
	if rb.count() != 0 {
		t.Error("tmux ran")
	}
}

func TestSplitAndKillFromManyGoroutines(t *testing.T) {
	var mu sync.Mutex
	n := 100
	c, _ := newFake(inside("%0"), func(args []string) (string, error) {
		if args[0] == "split-window" {
			mu.Lock()
			defer mu.Unlock()
			n++
			return "%" + strconv.Itoa(n), nil
		}
		return "", nil
	})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := c.SplitWindow(bg, SplitOptions{Target: "%0"})
			if err != nil {
				t.Error(err)
				return
			}
			if err := c.KillPane(bg, id); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(c.owned) != 0 {
		t.Errorf("%d panes are still marked as ours", len(c.owned))
	}
}

func TestPaneFocusedAsksOnceAndReadsTheAnswer(t *testing.T) {
	answer := "1\n"
	c, r := newFake(inside("%0"), func([]string) (string, error) { return answer, nil })
	ok, err := c.PaneFocused(bg, "%4")
	if err != nil || !ok {
		t.Fatalf("PaneFocused = %v, %v", ok, err)
	}
	if r.count() != 1 {
		t.Errorf("%d calls: polling must cost one", r.count())
	}
	got := strings.Join(r.last(), " ")
	if got != "display-message -p -t %4 #{&&:#{pane_active},#{window_active}}" {
		t.Errorf("command = %q: the pane must be active in a window that is active", got)
	}
	for _, no := range []string{"0", "", "10", "yes"} {
		answer = no
		if ok, err := c.PaneFocused(bg, "%4"); err != nil || ok {
			t.Errorf("answer %q: focused = %v, %v", no, ok, err)
		}
	}
	if _, err := c.PaneFocused(bg, "%4;kill-server"); err == nil {
		t.Error("a bad pane id must be refused before tmux is asked")
	}
	if c2, _ := newFake(inside("%0"), func([]string) (string, error) { return "", errors.New("no server") }); true {
		if _, err := c2.PaneFocused(bg, "%4"); err == nil {
			t.Error("a failure of tmux must be returned")
		}
	}
}

func TestSplitWindowRefusesADirectoryThatIsNotAnAbsolutePathOnOneLine(t *testing.T) {
	c, r := newFake(inside("%0"), func([]string) (string, error) { return "%9", nil })
	for _, bad := range []string{"relative/dir", "-c", "/a\nb", "/a\x00b"} {
		if _, err := c.SplitWindow(bg, SplitOptions{Target: "%1", Dir: bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("dir %q: err = %v, want ErrInvalid", bad, err)
		}
	}
	if r.count() != 0 {
		t.Errorf("tmux ran %d times for a bad directory", r.count())
	}
}
