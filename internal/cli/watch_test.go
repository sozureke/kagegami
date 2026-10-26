package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sozureke/kagegami/internal/agent"
	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/pipeline"
	"github.com/sozureke/kagegami/internal/terminal"
	"github.com/sozureke/kagegami/internal/tui"
	"github.com/sozureke/kagegami/pkg/tmux"
)

type fakeTmux struct {
	busy    bool
	inside  bool
	pane    tmux.PaneID
	splits  []tmux.SplitOptions
	splitEr error
}

func (f *fakeTmux) InsideTmux() bool { return f.inside }
func (f *fakeTmux) CurrentPane() (tmux.PaneID, error) {
	if !f.inside {
		return "", tmux.ErrNotInTmux
	}
	return f.pane, nil
}

func (f *fakeTmux) Piped(context.Context, tmux.PaneID) (bool, error) { return f.busy, nil }

func (f *fakeTmux) SplitWindow(_ context.Context, o tmux.SplitOptions) (tmux.PaneID, error) {
	f.splits = append(f.splits, o)
	return "%9", f.splitEr
}

func TestWatchOpensThePanelBesideThePaneAndKeepsFocus(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	f := &fakeTmux{inside: true, pane: "%3"}
	var out bytes.Buffer
	err := launchWatch(context.Background(), launchConfig{client: f, exe: "/opt/my tools/kagegami", percent: 40, noAnim: true, stderr: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.splits) != 1 {
		t.Fatalf("%d splits", len(f.splits))
	}
	s := f.splits[0]
	if s.Target != "%3" || !s.Horizontal || !s.KeepFocus || s.Percent != 40 {
		t.Errorf("split = %+v: it must split the person's pane side by side, keep their focus and use the size asked", s)
	}
	want := "'/opt/my tools/kagegami' 'panel' '--pane' '%3' '--case' 'htb' '--no-anim'"
	if !strings.Contains(s.Command, want) {
		t.Errorf("command = %q\nwant      %q", s.Command, want)
	}
	if !strings.Contains(out.String(), "watching case \"htb\"") {
		t.Errorf("stderr = %q", out.String())
	}
}

func TestWatchRefusesOutsideTmuxAndWithoutACase(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	f := &fakeTmux{}
	if err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", stderr: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "inside tmux") {
		t.Errorf("err = %v", err)
	}
	if len(f.splits) != 0 {
		t.Error("nothing is split outside tmux")
	}

	t.Setenv("KAGEGAMI_HOME", t.TempDir())
	f = &fakeTmux{inside: true, pane: "%1"}
	if err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", stderr: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "no case") {
		t.Errorf("a missing case must be said before a pane is opened: %v", err)
	}
	if len(f.splits) != 0 {
		t.Error("no case, no pane")
	}
}

func TestWatchReportsAFailedSplit(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	f := &fakeTmux{inside: true, pane: "%1", splitEr: errors.New("no space for new pane")}
	if err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", stderr: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "no space") {
		t.Errorf("err = %v", err)
	}
}

func TestWatchFlagsSetTheCaseBeforeWatching(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	target, goal := "10.0.0.5", "get in"
	f := &fakeTmux{inside: true, pane: "%1"}
	err := launchWatch(context.Background(), launchConfig{
		client: f, exe: "kg", caseName: "htb", stderr: &bytes.Buffer{},
		changes: engagement.Changes{Target: &target, Goal: &goal},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runCLI(t, "case", "show", "--case", "htb")
	if code != 0 || !strings.Contains(stdout, "10.0.0.5") || !strings.Contains(stdout, "get in") || !strings.Contains(stdout, "scope") {
		t.Errorf("the case was not updated (the scope follows the target):\n%s", stdout)
	}
}

func TestPanelArgs(t *testing.T) {
	if got := strings.Join(panelArgs("%2", "box", false, ""), " "); got != "panel --pane %2 --case box" {
		t.Errorf("args = %q", got)
	}
	if got := panelArgs("%2", "box", true, ""); got[len(got)-1] != "--no-anim" {
		t.Errorf("args = %v", got)
	}
}

func TestACommandIsShownAtOnceAndItsResultFollowsWithoutShowingItAgain(t *testing.T) {
	first := updateEvents(agent.Update{Cmd: "nmap -sV  10.0.0.5", Queued: true})
	if len(first) != 1 || first[0].Kind != tui.KindCommand || first[0].Text != "nmap -sV 10.0.0.5" {
		t.Fatalf("the acceptance shows the command and nothing else: %+v", first)
	}
	done := updateEvents(agent.Update{Cmd: "nmap -sV 10.0.0.5", Handled: true, By: "parser nmap", Added: pipeline.Added{Hosts: 1, Services: 3, WebPaths: 2}})
	if len(done) != 1 || done[0].Kind != tui.KindSystem || done[0].Glyph != tui.GlyphMap || done[0].Text != "map: +1 host, +3 services, +2 paths, from nmap" {
		t.Errorf("the result is the map line only, the command is not shown twice: %+v", done)
	}
	if got := updateEvents(agent.Update{Cmd: "x", Handled: true, By: "tier1", Added: pipeline.Added{Notes: 1}}); len(got) != 1 || got[0].Text != "map: +1 note, from tier1" {
		t.Errorf("events = %+v", got)
	}
	if got := updateEvents(agent.Update{Cmd: "./exploit.sh", Handled: true}); len(got) != 0 {
		t.Errorf("a command that changed nothing says nothing more: %+v", got)
	}
}

func TestMeaninglessCommandsAreRecordedButNeverShown(t *testing.T) {
	for _, reason := range []terminal.Reason{terminal.ReasonNone + "ignored", "silent", "empty"} {
		if got := updateEvents(agent.Update{Cmd: "clear", Skipped: reason}); len(got) != 0 {
			t.Errorf("a command dropped for %q was shown: %+v", reason, got)
		}
	}
}

func TestACommandThatFailedShowsItsExitCodeAndItsErrors(t *testing.T) {
	bad := updateEvents(agent.Update{Cmd: "false", Exit: 1, Queued: true})
	if len(bad) != 1 || bad[0].Text != "false  (exit 1)" {
		t.Errorf("events = %+v", bad)
	}
	failed := updateEvents(agent.Update{Cmd: "nmap  -sV\n 10.0.0.5", Handled: true, Err: errors.New("disk full")})
	if len(failed) != 1 || !strings.Contains(failed[0].Text, "could not read nmap -sV 10.0.0.5: disk full") {
		t.Errorf("events = %+v", failed)
	}
	unrecorded := updateEvents(agent.Update{Cmd: "make", Err: errors.New("no space")})
	if len(unrecorded) != 2 || unrecorded[0].Kind != tui.KindCommand {
		t.Errorf("a command whose output could not be recorded is shown with the error: %+v", unrecorded)
	}
}

func TestOnlyTheMissingHookComesFromTheCapture(t *testing.T) {
	silent := false
	if got := captureEvents(terminal.Event{Kind: terminal.CommandFinished, Cmd: "ls"}, &silent); len(got) != 0 {
		t.Errorf("a finished command is shown once the agent has judged it, not here: %+v", got)
	}
	if got := captureEvents(terminal.Event{Kind: terminal.CommandStarted, Cmd: "x"}, &silent); len(got) != 0 {
		t.Errorf("events = %+v", got)
	}
}

func TestTheMissingHookIsSaidOnce(t *testing.T) {
	silent := false
	first := captureEvents(terminal.Event{Kind: terminal.HookSilent}, &silent)
	if len(first) != 1 || !strings.Contains(first[0].Text, "kagegami hook zsh") {
		t.Errorf("events = %+v", first)
	}
	if again := captureEvents(terminal.Event{Kind: terminal.HookSilent}, &silent); len(again) != 0 {
		t.Error("the hook warning must not repeat")
	}
}

func TestWatchEndReasons(t *testing.T) {
	if got := watchEndReason(terminal.ErrPaneGone); !strings.Contains(got, "closed") {
		t.Errorf("reason = %q", got)
	}
	if got := watchEndReason(errors.New("boom")); got != "boom" {
		t.Errorf("reason = %q", got)
	}
}

func TestThePanelPaneKeepsItsErrorOnScreenAndReceivesTheEnvironment(t *testing.T) {
	env := forwardedEnv(func(name string) string {
		return map[string]string{"KAGEGAMI_HOME": "/my kg", "XDG_DATA_HOME": "", "HOME": "/home/x", "XDG_CONFIG_HOME": "/cfg"}[name]
	}, forwardedVars)
	if strings.Join(env, "|") != "KAGEGAMI_HOME=/my kg|XDG_CONFIG_HOME=/cfg" {
		t.Errorf("env = %v: only the variables that are set and that Kagegami reads", env)
	}
	cmd := panelCommand("/bin/kg", []string{"panel", "--pane", "%1"}, env)
	if !strings.HasPrefix(cmd, "env 'KAGEGAMI_HOME=/my kg' 'XDG_CONFIG_HOME=/cfg' '/bin/kg' 'panel'") {
		t.Errorf("command = %q", cmd)
	}
	if !strings.Contains(cmd, "|| {") || !strings.Contains(cmd, "read -r _") {
		t.Errorf("a failed panel must wait for Enter, or its pane and the reason vanish together: %q", cmd)
	}
	if bare := panelCommand("/bin/kg", nil, nil); strings.HasPrefix(bare, "env") {
		t.Errorf("no variables, no env: %q", bare)
	}
}

func TestWatchRefusesAPaneThatIsAlreadyWatched(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	f := &fakeTmux{inside: true, pane: "%1", busy: true}
	err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "already being watched") {
		t.Errorf("err = %v", err)
	}
	if len(f.splits) != 0 {
		t.Error("a second panel must not be opened for a pane that is taken")
	}
}

type fakeFocus struct {
	mu      sync.Mutex
	focused bool
	polls   int
}

func (f *fakeFocus) CurrentPane() (tmux.PaneID, error) { return "%5", nil }
func (f *fakeFocus) PaneFocused(context.Context, tmux.PaneID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return f.focused, nil
}
func (f *fakeFocus) set(v bool) { f.mu.Lock(); f.focused = v; f.mu.Unlock() }

type recordSender struct {
	mu   sync.Mutex
	msgs []bool
}

func (r *recordSender) Send(m tea.Msg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m.(tui.FocusMsg).Focused)
}

func (r *recordSender) got() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.msgs...)
}

func TestTheFocusIsReportedOnlyWhenItChanges(t *testing.T) {
	f, r := &fakeFocus{}, &recordSender{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { watchFocus(ctx, f, r, 5*time.Millisecond); close(done) }()

	waitFor := func(want []bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if g := r.got(); len(g) == len(want) {
				for i := range g {
					if g[i] != want[i] {
						t.Fatalf("messages = %v, want %v", g, want)
					}
				}
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("messages = %v, want %v", r.got(), want)
	}
	waitFor([]bool{false}) // the first answer is always told, so the outline starts right
	time.Sleep(30 * time.Millisecond)
	waitFor([]bool{false}) // no change, no message, however often it asks
	f.set(true)
	waitFor([]bool{false, true})
	f.set(false)
	waitFor([]bool{false, true, false})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchFocus must stop with its context")
	}
}

func TestKagegamisOwnCommandsAreNotTheTarget(t *testing.T) {
	for cmd, want := range map[string]bool{
		"kagegami map": true, "/usr/local/bin/kagegami next": true, "  kagegami  case show": true,
		"nmap kagegami.htb": false, "echo kagegami": false, "": false, "kagegamix": false,
	} {
		if got := ownCommand(cmd); got != want {
			t.Errorf("ownCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestAKeyReachesThePanelThroughItsEnvironmentAndNeverThroughItsCommand(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	const key = "sk-or-v1-NOT-A-REAL-KEY"
	env := map[string]string{"OPENROUTER_API_KEY": key, "KAGEGAMI_NO_ANIM": "1"}
	f := &fakeTmux{inside: true, pane: "%3"}
	err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", getenv: func(k string) string { return env[k] }, stderr: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	s := f.splits[0]
	if len(s.Env) != 1 || s.Env[0] != "OPENROUTER_API_KEY="+key {
		t.Errorf("env of the pane = %v", s.Env)
	}
	if strings.Contains(s.Command, key) {
		t.Errorf("the key is in the command line of the panel, where every process can read it: %q", s.Command)
	}
	if !strings.Contains(s.Command, "KAGEGAMI_NO_ANIM=1") {
		t.Errorf("a setting that is not a secret still goes with the command: %q", s.Command)
	}

	f = &fakeTmux{inside: true, pane: "%3"}
	_ = launchWatch(context.Background(), launchConfig{client: f, exe: "kg", getenv: func(string) string { return "" }, stderr: &bytes.Buffer{}})
	if len(f.splits[0].Env) != 0 {
		t.Errorf("with no key nothing is passed: %v", f.splits[0].Env)
	}
}

func TestWatchOfAGivenPaneNeedsNoTmuxAroundIt(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	f := &fakeTmux{} // not inside tmux: the launcher has just made the session
	err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", pane: "%1", notice: "first run: created x", stderr: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.splits) != 1 || f.splits[0].Target != "%1" {
		t.Fatalf("splits = %+v", f.splits)
	}
	if !strings.Contains(f.splits[0].Command, "'--notice' 'first run: created x'") {
		t.Errorf("command = %q: the notice must reach the panel", f.splits[0].Command)
	}
}

func TestThePanelOpensInTheDirectoryOfTheShell(t *testing.T) {
	caseWithHost(t, t.TempDir(), "htb")
	f := &fakeTmux{inside: true, pane: "%3"}
	if err := launchWatch(context.Background(), launchConfig{client: f, exe: "kg", dir: "/work/box", stderr: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if f.splits[0].Dir != "/work/box" {
		t.Errorf("dir = %q", f.splits[0].Dir)
	}
}
