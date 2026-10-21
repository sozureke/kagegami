package hook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sozureke/kagegami/pkg/tmux"
)

var bg = context.Background()

const testNonce = "0123456789abcdef0123456789abcdef"

// requireTools finds tmux and zsh. Where CI promises them (KAGEGAMI_REQUIRE_TMUX), their
// absence fails the test instead of skipping it.
func requireTools(t *testing.T) (tmuxPath string) {
	t.Helper()
	var paths []string
	for _, name := range []string{"tmux", "zsh"} {
		path, err := exec.LookPath(name)
		if err != nil {
			if os.Getenv("KAGEGAMI_REQUIRE_TMUX") != "" {
				t.Fatalf("%s is required (KAGEGAMI_REQUIRE_TMUX is set) but is not installed: %v", name, err)
			}
			t.Skipf("%s is not installed: skipping the tests that need it", name)
		}
		paths = append(paths, path)
	}
	return paths[0]
}

// session is a zsh in a pane of a private tmux server, with everything the pane prints
// piped into a file, as the capture will see it.
type session struct {
	t      *testing.T
	runner tmux.ExecRunner
	pane   string
	out    string // the raw stream
	runDir string
	script string // the rendered hook
}

func newSession(t *testing.T) *session {
	t.Helper()
	binary := requireTools(t)
	dir, err := os.MkdirTemp("", "kgh") // short: a unix socket path is limited in length
	if err != nil {
		t.Fatal(err)
	}
	runner := tmux.ExecRunner{Binary: binary, GlobalArgs: []string{"-S", filepath.Join(dir, "s"), "-f", "/dev/null"}, Env: tmux.CleanEnv()}
	t.Cleanup(func() {
		_, _ = runner.Run(context.WithoutCancel(bg), "kill-server")
		_ = os.RemoveAll(dir)
	})

	runDir := filepath.Join(dir, "run")
	if err := os.Mkdir(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := Render("zsh", runDir)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, runner: runner, out: filepath.Join(dir, "out"), runDir: runDir, script: filepath.Join(dir, "hook.zsh")}
	if err := os.WriteFile(s.script, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	pane, err := runner.Run(bg, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "m", "-x", "100", "-y", "30", "zsh -f")
	if err != nil {
		t.Fatal(err)
	}
	s.pane = pane
	if _, err := runner.Run(bg, "pipe-pane", "-t", pane, "cat >> "+tmux.ShellQuote(s.out)); err != nil {
		t.Fatal(err)
	}
	s.settle()
	return s
}

func (s *session) nonceFile() string {
	return filepath.Join(s.runDir, strings.TrimPrefix(s.pane, "%")+".nonce")
}

func (s *session) watch(content string) {
	s.t.Helper()
	if err := os.WriteFile(s.nonceFile(), []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) unwatch() {
	s.t.Helper()
	if err := os.Remove(s.nonceFile()); err != nil {
		s.t.Fatal(err)
	}
}

// run types a line into the shell and waits until the pane is quiet.
func (s *session) run(line string) {
	s.t.Helper()
	s.send(line)
	s.settle()
}

// send types a line into the shell and returns at once.
func (s *session) send(line string) {
	s.t.Helper()
	if _, err := s.runner.Run(bg, "send-keys", "-t", s.pane, "-l", "--", line); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.runner.Run(bg, "send-keys", "-t", s.pane, "Enter"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) load() { s.t.Helper(); s.run("source " + tmux.ShellQuote(s.script)) }

// settle waits until nothing has been written for a while.
func (s *session) settle() {
	s.t.Helper()
	last, stable := -1, 0
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		data, _ := os.ReadFile(s.out) //nolint:gosec // a file in the test's temporary directory
		if len(data) == last {
			if stable++; stable >= 6 {
				return
			}
		} else {
			last, stable = len(data), 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("the pane never went quiet")
}

func (s *session) stream() string {
	data, _ := os.ReadFile(s.out) //nolint:gosec // a file in the test's temporary directory
	return string(data)
}

// event is one marker and the output that follows it up to the next marker.
type event struct {
	kind   string // "C" or "D"
	fields map[string]string
	after  string // bytes between this marker and the next one
}

var markerRE = regexp.MustCompile(`\x1b\]7771;([CD]);([^\x07]*)\x07`)

func parse(stream string) []event {
	var events []event
	locs := markerRE.FindAllStringSubmatchIndex(stream, -1)
	for i, loc := range locs {
		ev := event{kind: stream[loc[2]:loc[3]], fields: map[string]string{}}
		for _, kv := range strings.SplitN(stream[loc[4]:loc[5]], ";", 3) { // the command text is last and may hold ";"
			k, v, _ := strings.Cut(kv, "=")
			ev.fields[k] = v
		}
		end := len(stream)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		ev.after = stream[loc[1]:end]
		events = append(events, ev)
	}
	return events
}

func TestZshHookMarksEachCommand(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	s.run("echo hello")
	s.run("false")
	s.run("") // an empty line starts no command
	s.run("echo again; (exit 7)")

	ev := parse(s.stream())
	type want struct{ kind, id, detail, out string }
	wants := []want{
		{"C", "1", "echo hello", "hello\r\n"},
		{"D", "1", "0", ""},
		{"C", "2", "false", ""},
		{"D", "2", "1", ""},
		{"C", "3", "echo again; (exit 7)", "again\r\n"},
		{"D", "3", "7", ""},
	}
	// The prompt that follows a D marker is outside the command: only C segments are output.
	if len(ev) != len(wants) {
		t.Fatalf("%d markers, want %d: %q", len(ev), len(wants), s.stream())
	}
	for i, w := range wants {
		e := ev[i]
		if e.kind != w.kind || e.fields["id"] != w.id || e.fields["kg"] != testNonce {
			t.Errorf("marker %d = %s %v, want %s id=%s with the nonce", i, e.kind, e.fields, w.kind, w.id)
		}
		switch w.kind {
		case "C":
			if e.fields["cmd"] != w.detail {
				t.Errorf("marker %d: cmd = %q, want %q", i, e.fields["cmd"], w.detail)
			}
			if e.after != w.out {
				t.Errorf("marker %d: output = %q, want exactly %q (nothing from zsh itself)", i, e.after, w.out)
			}
		case "D":
			if e.fields["rc"] != w.detail {
				t.Errorf("marker %d: rc = %q, want %q", i, e.fields["rc"], w.detail)
			}
		}
	}
}

func TestZshHookKeepsControlCharactersOutOfTheCommandText(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	// the hook is called directly with a text that has a tab, a newline, an escape and a bell
	s.run(`__kg_preexec $'a\tb\nc\e[31md\ae'`)

	var cmds []string
	for _, e := range parse(s.stream()) {
		if e.kind == "C" {
			cmds = append(cmds, e.fields["cmd"])
		}
	}
	if len(cmds) < 2 || cmds[len(cmds)-1] != "a b c [31md e" {
		t.Errorf("command texts = %q: control bytes must become spaces so they cannot end the marker early", cmds)
	}
}

func TestZshHookIsSilentUntilKagegamiWatches(t *testing.T) {
	s := newSession(t)
	s.load()
	s.run("echo one")
	if n := len(parse(s.stream())); n != 0 {
		t.Fatalf("%d markers without a nonce file", n)
	}

	s.watch(testNonce)
	s.run("[[ -o promptsp ]] && echo SP-ON || echo SP-OFF")
	s.run("echo two")
	watched := parse(s.stream())
	if len(watched) != 4 || watched[0].after != "SP-OFF\r\n" {
		t.Fatalf("while watched: %d markers, first output %q; want 4 and PROMPT_SP switched off", len(watched), firstAfter(watched))
	}

	mark := len(s.stream())
	s.unwatch()
	s.run("[[ -o promptsp ]] && echo SP-ON || echo SP-OFF")
	s.run("echo three")
	rest := s.stream()[mark:]
	if n := len(parse(rest)); n != 0 {
		t.Errorf("%d markers after the watch ended", n)
	}
	if !strings.Contains(rest, "\r\nSP-ON\r\n") || strings.Contains(rest, "\r\nSP-OFF\r\n") {
		t.Errorf("PROMPT_SP was not restored when the watch ended: %q", rest)
	}
}

func firstAfter(ev []event) string {
	if len(ev) == 0 {
		return ""
	}
	return ev[0].after
}

func TestZshHookKeepsAPromptSPTheUserHadOff(t *testing.T) {
	s := newSession(t)
	s.run("unsetopt promptsp")
	s.load()
	s.watch(testNonce)
	s.run("true")
	s.unwatch()
	s.run("true")
	s.run("[[ -o promptsp ]] && echo SP-ON || echo SP-OFF")
	if got := s.stream(); !strings.Contains(got, "\r\nSP-OFF\r\n") || strings.Contains(got, "\r\nSP-ON\r\n") {
		t.Error("an option the user had switched off must not be switched on by the hook")
	}
}

func TestZshHookIgnoresAnInvalidNonceFile(t *testing.T) {
	bad := map[string]string{
		"too short":      "0123456789abcdef\n",
		"one short":      testNonce[:31] + "\n",
		"too long":       testNonce + "0\n",
		"upper case":     strings.ToUpper(testNonce) + "\n",
		"not hex":        strings.Repeat("g", 32) + "\n",
		"empty":          "",
		"just a newline": "\n",
	}
	for name, content := range bad {
		t.Run(name, func(t *testing.T) {
			s := newSession(t)
			s.load()
			s.watch(content)
			s.run("echo x")
			if n := len(parse(s.stream())); n != 0 {
				t.Errorf("%d markers from a nonce file that is not a nonce", n)
			}
		})
	}
}

func TestZshHookNeedsTMUXPane(t *testing.T) {
	requireTools(t)
	script, err := Render("zsh", "/run")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(bg, "zsh", "-fc", script+"\nprint -r -- \"loaded=${__kg_loaded:-no}\"").Output()
	if err != nil || strings.TrimSpace(string(out)) != "loaded=no" {
		t.Errorf("outside tmux the hook must do nothing: %q, %v", out, err)
	}
}

func TestZshHookDoesNotReplaceTheUsersOwnHooks(t *testing.T) {
	s := newSession(t)
	s.run(`preexec() { print -r USER-PREEXEC }`)
	s.run(`precmd() { print -r USER-PRECMD }`)
	s.load()
	s.watch(testNonce)
	s.run("echo x")
	stream := s.stream()
	// the typed definitions are echoed too, so look for the printed lines
	// (the preexec runs for every line typed after it was defined, including the load)
	if strings.Count(stream, "USER-PREEXEC\r\n") < 3 || strings.Count(stream, "USER-PRECMD\r\n") < 3 {
		t.Errorf("the user's own preexec and precmd must keep running: %q", stream)
	}
	if len(parse(stream)) != 2 {
		t.Errorf("the hook did not mark the command: %q", stream)
	}
}

func TestZshHookLoadedTwiceMarksOnce(t *testing.T) {
	s := newSession(t)
	s.load()
	s.load()
	s.watch(testNonce)
	s.run("echo x")
	if n := len(parse(s.stream())); n != 2 {
		t.Errorf("%d markers after loading the hook twice, want 2 (one C, one D)", n)
	}
}

// Output of a command is untrusted and ends up in logs: the nonce must not be printable by
// the usual ways of dumping a shell's state.
func TestZshHookDoesNotLeakTheNonce(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	for _, line := range []string{"set", "typeset", "env", "export", "print -l ${(k)parameters}", "functions __kg_preexec"} {
		s.run(line)
	}
	if n := strings.Count(markerRE.ReplaceAllString(s.stream(), ""), testNonce); n != 0 {
		t.Errorf("the nonce appears %d times outside the markers", n)
	}
	if len(parse(s.stream())) < 12 {
		t.Error("the commands were not marked, so the test proves nothing")
	}
}

func TestRenderedRunDirReachesTheShellUnchanged(t *testing.T) {
	requireTools(t)
	dirs := []string{
		"/plain/run",
		"/with space/run",
		"/it's/run",
		"/'; echo injected; '/run",
		"/dollar/$HOME/`id`/$(id)/run",
		`/back\slash/"quote"/run`,
		"/unicode/日本語/run",
	}
	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			script, err := Render("zsh", dir)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(bg, "zsh", "-f", "-c", script+"\nprint -rn -- $__kg_dir")
			cmd.Env = append(os.Environ(), "TMUX_PANE=%1")
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != dir {
				t.Errorf("the shell saw %q (%v), want %q", out, err, dir)
			}
		})
	}
}

func TestEveryMarkerHasAnIncreasingID(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	for range 5 {
		s.run("true")
	}
	last := 0
	for _, e := range parse(s.stream()) {
		id, err := strconv.Atoi(e.fields["id"])
		if err != nil || (e.kind == "C" && id != last+1) {
			t.Fatalf("id %q after %d: ids must count up by one per command", e.fields["id"], last)
		}
		last = id
	}
	if last != 5 {
		t.Errorf("last id = %d, want 5", last)
	}
}

// If the watch ends while a command runs, there is nothing valid to close it with: no end
// marker, and above all none with an empty nonce.
func TestZshHookPrintsNoEndMarkerWhenTheWatchEndedDuringACommand(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	s.send("sleep 1")
	time.Sleep(400 * time.Millisecond)
	s.unwatch()
	s.settle()
	s.run("true")

	ev := parse(s.stream())
	if len(ev) != 1 || ev[0].kind != "C" {
		t.Errorf("markers = %+v, want only the start of sleep 1", ev)
	}
}

// Sourcing .zshrc again must not start the counting over: ids repeat otherwise.
func TestZshHookKeepsCountingWhenLoadedAgain(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	s.run("true")
	s.load()
	s.run("true")

	var ids []string
	for _, e := range parse(s.stream()) {
		if e.kind == "C" && e.fields["cmd"] == "true" {
			ids = append(ids, e.fields["id"])
		}
	}
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "3" {
		t.Errorf("ids of the two true commands = %v, want [1 3] (the reload itself is command 2)", ids)
	}
}

// The end-of-line indicator is switched with the watch also when no command is typed.
func TestZshHookSwitchesPromptSPAtThePromptToo(t *testing.T) {
	s := newSession(t)
	s.load()
	s.watch(testNonce)
	s.run("") // the prompt after this line switches the option off
	mark := len(s.stream())
	s.run("")
	if strings.Contains(s.stream()[mark:], "\x1b[7m%") {
		t.Error("the end-of-line indicator is still printed while watching")
	}

	s.unwatch()
	s.run("") // the prompt after this line switches it back on
	mark = len(s.stream())
	s.run("")
	if !strings.Contains(s.stream()[mark:], "\x1b[7m%") {
		t.Error("the end-of-line indicator did not come back after the watch ended")
	}
}

// What the user's other precmd hooks print must not end up in the output of the command.
func TestZshHookEndsTheCommandBeforeOtherPrecmdHooksRun(t *testing.T) {
	s := newSession(t)
	s.run(`userprompt() { print -r USER-PROMPT-INFO }`)
	s.run(`precmd_functions+=(userprompt)`)
	s.load()
	s.watch(testNonce)
	s.run("echo x")

	ev := parse(s.stream())
	if len(ev) != 2 || ev[0].after != "x\r\n" {
		t.Errorf("output of the command = %q, want exactly %q", firstAfter(ev), "x\r\n")
	}
	if !strings.Contains(s.stream(), "USER-PROMPT-INFO\r\n") {
		t.Error("the user's precmd hook did not run, so the test proves nothing")
	}
}

// What the user's other preexec hooks print comes before the start marker, not after it.
func TestZshHookStartsTheCommandAfterOtherPreexecHooksRan(t *testing.T) {
	s := newSession(t)
	s.run(`userpre() { print -r USER-PRE-INFO }`)
	s.run(`preexec_functions+=(userpre)`)
	s.load()
	s.watch(testNonce)
	s.run("echo x")

	ev := parse(s.stream())
	if len(ev) != 2 || ev[0].after != "x\r\n" {
		t.Errorf("output of the command = %q, want exactly %q", firstAfter(ev), "x\r\n")
	}
}
