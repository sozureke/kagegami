package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// wrapperTmux writes a script that stands in for tmux. The script logs how it was called
// (arguments to args.log, TMUX variables of its environment to env.log) and then runs body;
// body usually ends by running the real tmux ("$REAL").
func wrapperTmux(t *testing.T, body string) (binary, dir string) {
	t.Helper()
	realTmux := requireTmux(t)
	dir = t.TempDir()
	script := "#!/bin/sh\n" +
		"REAL=" + ShellQuote(realTmux) + "\n" +
		"DIR=" + ShellQuote(dir) + "\n" +
		`printf '%s\n' "$*" >> "$DIR/args.log"` + "\n" +
		`env | grep '^TMUX' >> "$DIR/env.log"` + "\n" +
		body + "\n"
	binary = filepath.Join(dir, "tmux")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test double
		t.Fatal(err)
	}
	return binary, dir
}

const passThrough = `exec "$REAL" "$@"`

func readLog(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // test file
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

var socketRE = regexp.MustCompile(`-S (\S+) `)

// sockets returns the server sockets the wrapper was asked to use.
func sockets(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readLog(t, dir, "args.log"), "\n") {
		if m := socketRE.FindStringSubmatch(line); m != nil && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func TestVerifyCapturePassesOnARealTmux(t *testing.T) {
	bin := requireTmux(t)
	start := time.Now()
	if err := VerifyCapture(bg, CheckOptions{Binary: bin}); err != nil {
		t.Fatalf("VerifyCapture: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("a passing check took %v: it should be quick, the deadline is only for failures", elapsed)
	}
}

func TestVerifyCaptureWorksWithTheDefaultBinary(t *testing.T) {
	requireTmux(t)
	if err := VerifyCapture(bg, CheckOptions{}); err != nil {
		t.Fatalf("an empty Binary means tmux from PATH: %v", err)
	}
}

func TestVerifyCaptureFailsWhenTheMarkerNeverArrives(t *testing.T) {
	// a tmux that accepts pipe-pane and then delivers nothing
	bin, dir := wrapperTmux(t, `case "$*" in *pipe-pane*) exit 0;; esac`+"\n"+passThrough)

	start := time.Now()
	err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 300 * time.Millisecond})
	if !errors.Is(err, ErrCaptureUnsupported) {
		t.Fatalf("err = %v, want ErrCaptureUnsupported", err)
	}
	// two attempts: 300 ms, then 600 ms
	if elapsed := time.Since(start); elapsed < 800*time.Millisecond || elapsed > 8*time.Second {
		t.Errorf("took %v: it should wait the deadline, retry once with twice the deadline, and stop", elapsed)
	}
	if got := len(sockets(t, dir)); got != 2 {
		t.Errorf("%d servers were used, want exactly 2: one try and one retry", got)
	}
	msg := err.Error()
	for _, want := range []string{"300ms", "600ms", "nothing"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error should say what happened (%q missing): %v", want, err)
		}
	}
}

func TestVerifyCaptureRetriesOnceAndThenBelievesTheSecondAnswer(t *testing.T) {
	// the first attempt's pipe-pane is swallowed, the second works: a transient failure
	bin, dir := wrapperTmux(t, `case "$*" in *pipe-pane*)
  if [ ! -e "$DIR/seen" ]; then : > "$DIR/seen"; exit 0; fi;;
esac`+"\n"+passThrough)

	if err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 400 * time.Millisecond}); err != nil {
		t.Fatalf("a failure on the first try only must not forbid capture: %v", err)
	}
	if got := len(sockets(t, dir)); got != 2 {
		t.Errorf("%d servers were used, want 2: the retry must repeat the whole check on a fresh server", got)
	}
}

func TestVerifyCaptureDoesNotRetryWhenItPasses(t *testing.T) {
	bin, dir := wrapperTmux(t, passThrough)
	if err := VerifyCapture(bg, CheckOptions{Binary: bin}); err != nil {
		t.Fatal(err)
	}
	if got := len(sockets(t, dir)); got != 1 {
		t.Errorf("%d servers were used, want 1", got)
	}
}

func TestVerifyCaptureNeedsAWorkingTmux(t *testing.T) {
	t.Run("no binary", func(t *testing.T) {
		start := time.Now()
		err := VerifyCapture(bg, CheckOptions{Binary: "/no/such/tmux", Deadline: 5 * time.Second})
		if err == nil || errors.Is(err, ErrCaptureUnsupported) {
			t.Fatalf("err = %v: a missing tmux is not 'capture unsupported'", err)
		}
		if time.Since(start) > 2*time.Second {
			t.Error("a missing binary must fail at once, not wait out the deadlines")
		}
	})
	t.Run("the server does not start", func(t *testing.T) {
		bin, _ := wrapperTmux(t, `case "$*" in *new-session*) echo "server exploded" >&2; exit 1;; esac`+"\n"+passThrough)
		err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 5 * time.Second})
		if err == nil || errors.Is(err, ErrCaptureUnsupported) || !strings.Contains(err.Error(), "server exploded") {
			t.Fatalf("err = %v, want the start-up failure with tmux's own message", err)
		}
	})
	t.Run("pipe-pane fails", func(t *testing.T) {
		bin, _ := wrapperTmux(t, `case "$*" in *pipe-pane*) echo "no pipes today" >&2; exit 1;; esac`+"\n"+passThrough)
		err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 5 * time.Second})
		if err == nil || errors.Is(err, ErrCaptureUnsupported) || !strings.Contains(err.Error(), "no pipes today") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestVerifyCaptureHonoursACancelledContext(t *testing.T) {
	bin, dir := wrapperTmux(t, `case "$*" in *pipe-pane*) exit 0;; esac`+"\n"+passThrough)
	ctx, cancel := context.WithTimeout(bg, 400*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := VerifyCapture(ctx, CheckOptions{Binary: bin, Deadline: 30 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the check did not stop when the context ended")
	}
	assertCleanedUp(t, dir)
}

// The check must leave nothing behind: no server, no files.
func assertCleanedUp(t *testing.T, dir string) {
	t.Helper()
	assertNoPaneProcess(t)
	for _, sock := range sockets(t, dir) {
		if _, err := os.Stat(filepath.Dir(sock)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the temporary directory %s is still there", filepath.Dir(sock))
		}
		out, err := exec.CommandContext(bg, requireTmux(t), "-S", sock, "list-sessions").CombinedOutput()
		if err == nil {
			t.Errorf("the server on %s is still running: %s", sock, out)
		}
	}
}

func TestVerifyCaptureCleansUpOnEveryPath(t *testing.T) {
	cases := map[string]string{
		"success":         passThrough,
		"marker lost":     `case "$*" in *pipe-pane*) exit 0;; esac` + "\n" + passThrough,
		"pipe-pane fails": `case "$*" in *pipe-pane*) exit 1;; esac` + "\n" + passThrough,
		"send-keys fails": `case "$*" in *send-keys*) exit 1;; esac` + "\n" + passThrough,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			bin, dir := wrapperTmux(t, body)
			_ = VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 250 * time.Millisecond})
			if len(sockets(t, dir)) == 0 {
				t.Skip("tmux was never started on this path")
			}
			assertCleanedUp(t, dir)
		})
	}
}

func TestVerifyCaptureUsesAPrivateServer(t *testing.T) {
	t.Setenv("TMUX", "/tmp/the-users-server,1,0")
	t.Setenv("TMUX_PANE", "%3")
	bin, dir := wrapperTmux(t, passThrough)

	if err := VerifyCapture(bg, CheckOptions{Binary: bin}); err != nil {
		t.Fatal(err)
	}
	if env := readLog(t, dir, "env.log"); strings.TrimSpace(env) != "" {
		t.Errorf("TMUX variables reached tmux, which would then find the user's server:\n%s", env)
	}
	args := readLog(t, dir, "args.log")
	if !strings.Contains(args, "-f /dev/null") {
		t.Errorf("the server must start without a configuration file:\n%s", args)
	}
	socks := sockets(t, dir)
	if len(socks) == 0 {
		t.Fatalf("no -S socket was used:\n%s", args)
	}
	for _, s := range socks {
		if !strings.HasPrefix(s, os.TempDir()) || strings.Contains(s, "tmux-") {
			t.Errorf("socket %s is not a private one in a temporary directory", s)
		}
	}
}

// The marker is random on every check and is not any nonce of a capture session.
func TestVerifyCaptureUsesAFreshRandomMarker(t *testing.T) {
	bin, dir := wrapperTmux(t, passThrough)
	tokenRE := regexp.MustCompile(`7771;V;([0-9a-f]+)`)
	var tokens []string
	for range 3 {
		if err := VerifyCapture(bg, CheckOptions{Binary: bin}); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range tokenRE.FindAllStringSubmatch(readLog(t, dir, "args.log"), -1) {
		tokens = append(tokens, m[1])
	}
	if len(tokens) != 3 {
		t.Fatalf("found %d markers in the commands, want 3: %v", len(tokens), tokens)
	}
	seen := map[string]bool{}
	for _, tok := range tokens {
		if len(tok) != 32 {
			t.Errorf("marker %q has %d hex digits, want 32 (128 random bits)", tok, len(tok))
		}
		if seen[tok] {
			t.Errorf("the marker %s was used twice", tok)
		}
		seen[tok] = true
	}
}

func TestVerifyCaptureDefaultDeadline(t *testing.T) {
	if defaultCheckDeadline != 5*time.Second {
		t.Errorf("default deadline = %v, want 5s", defaultCheckDeadline)
	}
}

func TestDescribe(t *testing.T) {
	if got := describe(""); got != "nothing" {
		t.Errorf("describe(\"\") = %q", got)
	}
	if got := describe("abc\x1b"); got != `4 bytes: "abc\x1b"` {
		t.Errorf("describe = %q", got)
	}
	long := strings.Repeat("x", 500)
	got := describe(long)
	if !strings.HasPrefix(got, "500 bytes starting ") || len(got) > 140 {
		t.Errorf("a long answer must be shortened but keep its size: %q", got)
	}
}

// pipeVia returns a script body that runs the command given to pipe-pane through filter
// first, as a tmux that alters (or delays) what the pipe delivers would.
func pipeVia(filter string) string {
	return `case "$*" in *pipe-pane*)
  n=$#
  i=0
  for a in "$@"; do
    i=$((i+1))
    last=$a
    if [ $i -lt $n ]; then set -- "$@" "$a"; fi
  done
  shift $n
  exec "$REAL" "$@" "` + filter + ` $last";;
esac
` + passThrough
}

// alteringTmux is a tmux whose pipe-pane runs the output through a perl filter that changes
// it one byte at a time. The filter does not buffer, which matters: the marker is short and
// has no newline, and a buffering filter such as tr would hold it back, making an altered
// marker look like no marker at all.
func alteringTmux(t *testing.T, perlStatements string) string {
	t.Helper()
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skipf("perl is not installed: %v", err)
	}
	bin, dir := wrapperTmux(t, pipeVia(`perl $DIR/filter.pl |`))
	script := "$| = 1;\nwhile (read(STDIN, $b, 1)) {\n" + perlStatements + "\nprint $b;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "filter.pl"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A pipe that delivers the marker altered must not pass, whatever the alteration: the check
// is about raw, unchanged bytes.
func TestVerifyCaptureRejectsAnAlteredMarker(t *testing.T) {
	alterations := map[string]string{
		"characters changed":    `$b =~ tr/a-f/A-F/;`,
		"escape bytes removed":  `next if $b eq "\033";`,
		"bell removed":          `next if $b eq "\007";`,
		"everything upper-case": `$b = uc $b;`,
		"digits changed":        `$b =~ tr/0-9/a-j/;`,
	}
	for name, statements := range alterations {
		t.Run(name, func(t *testing.T) {
			bin := alteringTmux(t, statements)
			err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 400 * time.Millisecond})
			if !errors.Is(err, ErrCaptureUnsupported) {
				t.Fatalf("err = %v, want ErrCaptureUnsupported: an altered marker was accepted", err)
			}
			if strings.Contains(err.Error(), "delivered nothing") {
				t.Errorf("the pipe did deliver the altered bytes, the message should say what arrived: %v", err)
			}
		})
	}
}

// The filter used above must be harmless: with no alteration the marker still passes, so a
// rejection in the test before really comes from the alteration.
func TestTheAlteringFilterIsTransparentWhenItAltersNothing(t *testing.T) {
	bin := alteringTmux(t, `# no change`)
	if err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 5 * time.Second}); err != nil {
		t.Fatalf("an unchanged marker through the perl filter must pass: %v", err)
	}
}

// Output travels through tmux and a pipe, so a marker can arrive after a delay.
func TestVerifyCaptureWaitsForALateMarker(t *testing.T) {
	bin, dir := wrapperTmux(t, pipeVia(`sleep 1;`))
	start := time.Now()
	if err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 5 * time.Second}); err != nil {
		t.Fatalf("a marker that arrives after a second must be waited for: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("took %v: the delay did not happen, the test is not testing what it claims", elapsed)
	}
	if got := len(sockets(t, dir)); got != 1 {
		t.Errorf("%d servers: a late marker is not a failure and needs no retry", got)
	}
}

func TestVerifyCaptureRefusesAPathThatIsTooLongForASocket(t *testing.T) {
	long := t.TempDir()
	for len(long) < 120 {
		long = filepath.Join(long, "a-deliberately-long-directory-name")
	}
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
	bin, dir := wrapperTmux(t, passThrough)

	err := VerifyCapture(bg, CheckOptions{Binary: bin})
	if err == nil || errors.Is(err, ErrCaptureUnsupported) || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v, want a clear 'too long' error that is not 'capture unsupported'", err)
	}
	if args := readLog(t, dir, "args.log"); args != "" {
		t.Errorf("tmux was started although the socket path cannot work: %s", args)
	}
}

func TestVerifyCaptureWithoutATemporaryDirectory(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	bin, _ := wrapperTmux(t, passThrough)
	err := VerifyCapture(bg, CheckOptions{Binary: bin})
	if err == nil || errors.Is(err, ErrCaptureUnsupported) || !strings.Contains(err.Error(), "temporary directory") {
		t.Fatalf("err = %v", err)
	}
}

// assertNoPaneProcess looks for a live process of a check's pane, found by its marker. A
// server whose socket directory is already gone cannot be found through tmux, so this is the
// only way to see a server that was left running.
func assertNoPaneProcess(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skipf("pgrep is not installed: %v", err)
	}
	var out []byte
	for range 60 {
		out, _ = exec.CommandContext(bg, "pgrep", "-f", `7771;V;[0-9a-f]{32}`).Output()
		if len(out) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("a process of a check's pane is still running (pids %s): its server was not killed", strings.Fields(string(out)))
}

// A retry waits twice as long as the first try: a marker that needs longer than the
// deadline, but not longer than twice the deadline, passes on the retry.
func TestVerifyCaptureRetryWaitsLonger(t *testing.T) {
	bin, dir := wrapperTmux(t, pipeVia(`sleep 0.8;`))
	if err := VerifyCapture(bg, CheckOptions{Binary: bin, Deadline: 500 * time.Millisecond}); err != nil {
		t.Fatalf("the retry should have waited long enough: %v", err)
	}
	if got := len(sockets(t, dir)); got != 2 {
		t.Errorf("%d servers, want 2: the first try must have timed out, the retry succeeded", got)
	}
}

// With no deadline given, the default applies; zero must not mean "do not wait".
func TestVerifyCaptureZeroDeadlineMeansTheDefault(t *testing.T) {
	bin, _ := wrapperTmux(t, pipeVia(`sleep 1;`))
	if err := VerifyCapture(bg, CheckOptions{Binary: bin}); err != nil {
		t.Fatalf("a marker that arrives after a second must be waited for with the default deadline: %v", err)
	}
}
