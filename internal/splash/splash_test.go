package splash

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// testArt is made up for the tests; the art of the product is assets/art.txt.
const testArt = `  ####   ####
 #    # #    #
 #    # #    #
  ####   ####
    ##   ##
  ########  █
`

func seeded(seed uint64) Config {
	c := DefaultConfig()
	c.Seed = seed
	return c
}

func flat(f [][]Cell) string {
	var b strings.Builder
	for _, row := range f {
		for _, c := range row {
			b.WriteRune(c.Ch)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func runToEnd(t *testing.T, s *State, cfg Config) int {
	t.Helper()
	limit := int(cfg.TotalMaxSec*float64(cfg.FPS)) + 10
	n := 0
	for !s.Done() {
		s.Step(cfg)
		n++
		if n > limit {
			t.Fatalf("the animation did not end after %d frames (limit %d)", n, limit)
		}
	}
	return n
}

func TestFrameIsDeterministicForASeed(t *testing.T) {
	cfg := seeded(42)
	cfg.Glitch, cfg.RGBSplit = 100, 100
	a, b, other := NewState(testArt, cfg), NewState(testArt, cfg), NewState(testArt, seeded(43))
	differs := false
	for i := range 200 {
		fa, fb := Frame(a, cfg), Frame(b, cfg)
		if flat(fa) != flat(fb) {
			t.Fatalf("frame %d differs between two states with the same seed", i)
		}
		for y := range fa {
			for x := range fa[y] {
				if fa[y][x] != fb[y][x] {
					t.Fatalf("frame %d: cell %d,%d differs in colour", i, x, y)
				}
			}
		}
		if flat(fa) != flat(Frame(other, seeded(43))) {
			differs = true
		}
		a.Step(cfg)
		b.Step(cfg)
		other.Step(seeded(43))
	}
	if !differs {
		t.Error("another seed must give other frames")
	}
}

func TestDrawingAFrameDoesNotMoveTheState(t *testing.T) {
	cfg := seeded(7)
	s := NewState(testArt, cfg)
	for range 60 {
		s.Step(cfg)
	}
	first := flat(Frame(s, cfg))
	if second := flat(Frame(s, cfg)); first != second {
		t.Error("two Frame calls with no Step between them differ")
	}
}

func TestTheEndOfTheFadeOutIsEmpty(t *testing.T) {
	for _, mode := range []string{ModeFade, ModeDecode} {
		cfg := seeded(5)
		cfg.Mode = mode
		s := NewState(testArt, cfg)
		runToEnd(t, s, cfg)
		for y, row := range Frame(s, cfg) {
			for x, c := range row {
				if c.Ch != ' ' {
					t.Fatalf("%s: cell %d,%d is %q at the end", mode, x, y, c.Ch)
				}
			}
		}
	}
}

func TestNothingShowsAtTheStartInFadeMode(t *testing.T) {
	cfg := seeded(5)
	s := NewState(testArt, cfg)
	if strings.TrimSpace(flat(Frame(s, cfg))) != "" {
		t.Error("in fade mode the first frame is empty")
	}
}

func TestTheArtAppearsExactlyWhenNothingElseIsGoingOn(t *testing.T) {
	for _, mode := range []string{ModeFade, ModeDecode} {
		cfg := seeded(9)
		cfg.Mode = mode
		cfg.Glitch, cfg.PulseFreq, cfg.RGBSplit = 0, 0, 0
		cfg.HoldSec = 5
		cfg.TotalMaxSec = 20
		s := NewState(testArt, cfg)
		for s.t < s.appearT {
			s.Step(cfg)
		}
		want := strings.Split(strings.TrimRight(testArt, "\n"), "\n")
		for y, row := range Frame(s, cfg) {
			if got := strings.TrimRight(string(runesOf(row)), " "); got != strings.TrimRight(want[y], " ") {
				t.Errorf("%s: row %d of the finished art is %q, want %q", mode, y, got, want[y])
			}
		}
	}
}

func runesOf(row []Cell) []rune {
	out := make([]rune, len(row))
	for i, c := range row {
		out[i] = c.Ch
	}
	return out
}

func TestSpacesOfTheArtStayEmptyUnlessAGhostIsDrawnBesideACharacter(t *testing.T) {
	cfg := seeded(11)
	cfg.Glitch, cfg.RGBSplit = 100, 0
	cfg.PulseFreq = 100
	s := NewState(testArt, cfg)
	art := strings.Split(strings.TrimRight(testArt, "\n"), "\n")
	for range 150 {
		s.Step(cfg)
		f := Frame(s, cfg)
		for y, row := range f {
			dx, _ := s.rowShift(y)
			for x, c := range row {
				sx := x - dx
				inArt := sx >= 0 && sx < len([]rune(art[y])) && []rune(art[y])[sx] != ' '
				if c.Ch != ' ' && !inArt {
					t.Fatalf("a character at %d,%d where the art has a space (shift %d)", x, y, dx)
				}
			}
		}
	}
}

func TestEveryCellIsInsideTheArtAndPrintable(t *testing.T) {
	cfg := seeded(3)
	cfg.Glitch, cfg.RGBSplit, cfg.PulseFreq = 100, 100, 100
	for _, mode := range []string{ModeFade, ModeDecode} {
		cfg.Mode = mode
		s := NewState(testArt, cfg)
		w, h := s.Size()
		if w != 14 || h != 6 {
			t.Fatalf("size = %dx%d", w, h)
		}
		for i := 0; !s.Done() && i < 1000; i++ {
			f := Frame(s, cfg)
			if len(f) != h {
				t.Fatalf("frame %d has %d rows, want %d", i, len(f), h)
			}
			for y, row := range f {
				if len(row) != w {
					t.Fatalf("frame %d row %d has %d cells, want %d", i, y, len(row), w)
				}
				for x, c := range row {
					if c.Ch != ' ' && (unicode.IsControl(c.Ch) || !unicode.IsPrint(c.Ch)) {
						t.Fatalf("frame %d cell %d,%d is not printable: %U", i, x, y, c.Ch)
					}
					if c.Ch == ' ' && c.RGB != (RGB{}) {
						t.Fatalf("frame %d: an empty cell %d,%d has a colour", i, x, y)
					}
				}
			}
			s.Step(cfg)
		}
	}
}

func TestShortLinesArePaddedAndEmptyArtIsDone(t *testing.T) {
	s := NewState("ab\nabcdef\n", seeded(1))
	if w, h := s.Size(); w != 6 || h != 2 {
		t.Errorf("size = %dx%d, want 6x2", w, h)
	}
	if empty := NewState("", seeded(1)); !empty.Done() || len(Frame(empty, seeded(1))) != 0 {
		t.Error("an empty art is done and draws nothing")
	}
}

func TestTheWholeAnimationFitsTheTotalAndTheHoldIsCutToMakeRoom(t *testing.T) {
	cfg := seeded(2)
	cfg.TotalMaxSec = 6
	frames := runToEnd(t, NewState(testArt, cfg), cfg)
	if float64(frames) > cfg.TotalMaxSec*float64(cfg.FPS)+1 {
		t.Errorf("%d frames is longer than %v s", frames, cfg.TotalMaxSec)
	}

	long := seeded(2)
	long.HoldSec, long.TotalMaxSec = 30, 3
	if n := runToEnd(t, NewState(testArt, long), long); float64(n) > 3*float64(long.FPS)+1 {
		t.Errorf("a hold of 30 s inside a total of 3 s took %d frames", n)
	}

	tight := seeded(2)
	tight.TotalMaxSec, tight.FadeOutSec = 0.5, 5
	if n := runToEnd(t, NewState(testArt, tight), tight); float64(n) > 0.5*float64(tight.FPS)+1 {
		t.Errorf("a dissolve longer than the total took %d frames", n)
	}

	short := seeded(2)
	short.HoldSec = 0
	if a, b := runToEnd(t, NewState(testArt, short), short), frames; a >= b {
		t.Errorf("no hold took %d frames, a hold of %v s took %d", a, cfg.HoldSec, b)
	}
}

func TestSpeedMakesTheAppearanceShorter(t *testing.T) {
	slow, fast := seeded(4), seeded(4)
	slow.Speed, fast.Speed = 0.5, 4
	slow.HoldSec, fast.HoldSec = 0, 0
	slow.TotalMaxSec, fast.TotalMaxSec = 60, 60
	if runToEnd(t, NewState(testArt, fast), fast) >= runToEnd(t, NewState(testArt, slow), slow) {
		t.Error("a higher speed must end sooner")
	}
}

func TestRingsAppearFromTheCentreOutwards(t *testing.T) {
	cfg := seeded(6)
	cfg.Jitter = 0
	s := NewState(strings.Repeat(strings.Repeat("#", 41)+"\n", 9), cfg)
	cx, cy := 20, 4
	if s.start[cy][cx] > s.start[0][0] || s.start[cy][cx] > s.start[0][40] {
		t.Errorf("the centre starts at %v, the corners at %v and %v", s.start[cy][cx], s.start[0][0], s.start[0][40])
	}
	if s.start[cy][cx] != 0 {
		t.Errorf("the centre starts at %v, want 0", s.start[cy][cx])
	}
}

func TestPulsesAndGlitchOnlyStartOnceTheArtHasAppeared(t *testing.T) {
	cfg := seeded(8)
	cfg.PulseFreq, cfg.Glitch = 100, 100
	s := NewState(testArt, cfg)
	for s.t+cfg.Speed < s.appearT {
		s.Step(cfg)
		if len(s.pulses) != 0 || s.burst.left != 0 || s.line.y != -1 || s.flick.left != 0 {
			t.Fatal("an effect started before the art had appeared")
		}
	}
	started := false
	for range 400 {
		s.Step(cfg)
		started = started || len(s.pulses) > 0
	}
	if !started {
		t.Error("with a frequency of 100 a pulse must start")
	}
}

func TestGlitchZeroTurnsEveryGlitchOff(t *testing.T) {
	cfg := seeded(12)
	cfg.Glitch, cfg.PulseFreq, cfg.RGBSplit = 0, 0, 100
	cfg.HoldSec, cfg.TotalMaxSec = 5, 20
	s := NewState(testArt, cfg)
	for s.t < s.appearT {
		s.Step(cfg)
	}
	base := flat(Frame(s, cfg))
	for range 100 {
		s.Step(cfg)
		if flat(Frame(s, cfg)) != base {
			t.Fatal("with no glitch and no pulses the finished art must not change")
		}
	}
}

func TestRGBSplitDrawsGhostsOnlyOnGlitchingRows(t *testing.T) {
	cfg := seeded(13)
	cfg.Glitch, cfg.RGBSplit = 100, 100
	cfg.HoldSec, cfg.TotalMaxSec = 10, 30
	s := NewState(testArt, cfg)
	for s.t < s.appearT {
		s.Step(cfg)
	}
	ghosts := 0
	for range 300 {
		s.Step(cfg)
		for y, row := range Frame(s, cfg) {
			_, glitching := s.rowShift(y)
			for _, c := range row {
				if c.Ch != ' ' && c.R > c.G+50 && c.R > 100 && !glitching {
					ghosts++
				}
			}
		}
	}
	if ghosts != 0 {
		t.Errorf("%d red cells outside glitching rows", ghosts)
	}
}

func TestScanlinesAndVignetteDarken(t *testing.T) {
	art := strings.Repeat(strings.Repeat("#", 21)+"\n", 7)
	cfg := seeded(14)
	cfg.Glitch, cfg.PulseFreq, cfg.RGBSplit = 0, 0, 0
	cfg.HoldSec, cfg.TotalMaxSec = 5, 20
	settle := func(c Config) [][]Cell {
		s := NewState(art, c)
		for s.t < s.appearT {
			s.Step(c)
		}
		return Frame(s, c)
	}
	plain := cfg
	plain.Scanlines, plain.Vignette = false, false
	p := settle(plain)
	if p[0][0].G != p[3][10].G {
		t.Error("with both off every cell has the same brightness")
	}
	scan := plain
	scan.Scanlines = true
	f := settle(scan)
	if f[1][10].G >= f[0][10].G {
		t.Error("scanlines darken every second row")
	}
	vig := plain
	vig.Vignette = true
	f = settle(vig)
	if f[3][10].G <= f[0][0].G {
		t.Error("the vignette darkens the corners more than the middle")
	}
}

func TestDecodeShowsGarbageBeforeTheCharacterSettles(t *testing.T) {
	cfg := seeded(15)
	cfg.Mode = ModeDecode
	cfg.Glitch = 0
	s := NewState(testArt, cfg)
	if strings.Count(flat(Frame(s, cfg)), "#") > 3 {
		t.Error("at the start decode shows garbage, not the art")
	}
	if strings.TrimSpace(flat(Frame(s, cfg))) == "" {
		t.Error("decode shows characters from the first frame")
	}
}

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want RGB
		ok   bool
	}{
		{"#39ff14", RGB{0x39, 0xff, 0x14}, true},
		{"#FFF", RGB{255, 255, 255}, true},
		{" Cyan ", RGB{0, 255, 255}, true},
		{"magenta", RGB{255, 0, 255}, true},
		{"white", RGB{255, 255, 255}, true},
		{"green", RGB{0x39, 0xff, 0x14}, true},
		{"#12345", RGB{}, false},
		{"#gggggg", RGB{}, false},
		{"orange", RGB{}, false},
		{"", RGB{}, false},
	}
	for _, tt := range tests {
		got, err := ParseColor(tt.in)
		if (err == nil) != tt.ok || (tt.ok && got != tt.want) {
			t.Errorf("ParseColor(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestDefaultsAreTheDocumentedOnesAndValid(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	want := Config{
		Speed: 1.8, FPS: 30, Rings: 6, Pulse: 5, Fade: 12, Jitter: 2, CenterX: 50, CenterY: 50, PulseFreq: 35, PulseSpeed: 40, PulseWidth: 18,
		Glitch: 55, RGBSplit: 50, Scanlines: true, Vignette: true, CellAspect: 0.5, FadeOutSec: 1.2, HoldSec: 2, TotalMaxSec: 6, Color: "#39ff14", Mode: "fade",
	}
	if c != want {
		t.Errorf("defaults = %+v", c)
	}
}

func TestValidateNamesEveryProblem(t *testing.T) {
	c := DefaultConfig()
	c.Speed, c.FPS, c.Rings, c.Fade, c.Glitch, c.Mode, c.Color = 0, 0, 0, 0, 101, "melt", "nope"
	err := c.Validate()
	if err == nil {
		t.Fatal("want errors")
	}
	for _, field := range []string{"speed", "fps", "rings", "fade", "glitch", "mode", "color"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("the error does not mention %q: %v", field, err)
		}
	}
}

func TestLoadYAML(t *testing.T) {
	c, err := LoadYAML([]byte("glitch: 80\ncolor: cyan\nloop: true\nmode: decode\nseed: 7\n"))
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultConfig()
	if c.Glitch != 80 || c.Color != "cyan" || !c.Loop || c.Mode != ModeDecode || c.Seed != 7 || c.Speed != def.Speed || c.Rings != def.Rings {
		t.Errorf("config = %+v", c)
	}
	if c, err := LoadYAML(nil); err != nil || c != def {
		t.Errorf("an empty file keeps the defaults: %v", err)
	}
	for name, in := range map[string]string{
		"a misspelt field": "glich: 80\n",
		"a bad value":      "glitch: 500\n",
		"a wrong type":     "fps: fast\n",
	} {
		if _, err := LoadYAML([]byte(in)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestColorHintIsGivenOnlyWithoutTrueColourAndMentionsTmuxInsideIt(t *testing.T) {
	if colorHint(colorprofile.TrueColor, nil) != "" || colorHint(colorprofile.NoTTY, nil) != "" {
		t.Error("no hint with true colour or without a terminal")
	}
	plain := colorHint(colorprofile.ANSI256, []string{"TERM=xterm-256color"})
	if !strings.Contains(plain, "256 colours") || strings.Contains(plain, "terminal-features") {
		t.Errorf("hint outside tmux = %q", plain)
	}
	inTmux := colorHint(colorprofile.ANSI256, []string{"TMUX=/tmp/tmux-501/default,1,0"})
	if !strings.Contains(inTmux, "set -as terminal-features ',*:RGB'") {
		t.Errorf("hint inside tmux = %q", inTmux)
	}
	if strings.Contains(colorHint(colorprofile.ANSI256, []string{"TMUX="}), "terminal-features") {
		t.Error("an empty TMUX is not tmux")
	}
}

func TestAnyKeyEndsTheAnimationAndItEndsByItselfWithNoTimerLeft(t *testing.T) {
	cfg := seeded(1)
	m := &model{art: testArt, cfg: cfg, state: NewState(testArt, cfg), width: 40, height: 12}
	if m.Init() == nil {
		t.Fatal("the animation starts a timer")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}); cmd == nil {
		t.Fatal("a key must quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("a key must quit")
	}

	m = &model{art: testArt, cfg: cfg, state: NewState(testArt, cfg), width: 40, height: 12}
	var last tea.Cmd
	for i := 0; i < 10_000 && !m.state.Done(); i++ {
		_, last = m.Update(tickMsg{})
	}
	if last == nil {
		t.Fatal("the last tick must return a command")
	}
	if _, ok := last().(tea.QuitMsg); !ok {
		t.Error("when the animation ends the program quits, and no timer is left")
	}
}

func TestLoopStartsAgainInsteadOfQuitting(t *testing.T) {
	cfg := seeded(1)
	cfg.Loop = true
	m := &model{art: testArt, cfg: cfg, state: NewState(testArt, cfg), width: 40, height: 12}
	first, restarted := m.state, false
	for i := 0; i < 10_000 && !restarted; i++ {
		_, cmd := m.Update(tickMsg{})
		if cmd == nil {
			t.Fatal("a looping animation always has its timer")
		}
		restarted = m.state != first
	}
	if !restarted {
		t.Fatal("a looping animation never started again")
	}
	if m.state.tick != 0 || m.state.Done() {
		t.Errorf("the new state must be fresh: tick = %d", m.state.tick)
	}
}

func TestRenderCentresCropsAndKeepsRowsApart(t *testing.T) {
	cells := [][]Cell{{{Ch: 'a', RGB: RGB{1, 2, 3}}, {Ch: ' '}, {Ch: 'b', RGB: RGB{1, 2, 3}}}, {{Ch: ' '}, {Ch: ' '}, {Ch: ' '}}}
	out := render(cells, 9, 5)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lines, want the height of the terminal", len(lines))
	}
	if !strings.Contains(lines[1], "\x1b[38;2;1;2;3ma") || !strings.HasPrefix(lines[1], "   ") {
		t.Errorf("row not centred: %q", lines[1])
	}
	narrow := strings.Split(render(cells, 2, 1), "\n")
	if len(narrow) != 1 {
		t.Fatalf("%d lines", len(narrow))
	}
	if render(nil, 10, 10) != "" || render(cells, 0, 5) != "" {
		t.Error("nothing to draw, nothing drawn")
	}
}

func TestRunPlaysInAFakeTerminalAndStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, testArt, seeded(1), strings.NewReader(""), &out, &errOut, nil)
	}()
	t.Logf("run returned: %v", <-done) // it must return: a cancelled context ends the program
	if err := run(context.Background(), testArt, Config{}, strings.NewReader(""), &out, &errOut, nil); err == nil {
		t.Error("an invalid config must be refused before anything is drawn")
	}
}
