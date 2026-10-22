package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 22, 15, 4, 0, 0, time.UTC) }

func plain(lines []string) string { return ansi.Strip(strings.Join(lines, "\n")) }

func newModel(t *testing.T, cfg Config) *Model {
	t.Helper()
	cfg.Now = fixedNow
	m := New(cfg)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cfg.NoAnim {
		m.Init()
	}
	return m
}

func press(m *Model, k string) tea.Cmd {
	var key tea.Msg
	switch k {
	case "ctrl+c":
		key = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		key = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
	}
	_, cmd := m.Update(key)
	return cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestTheWordmarkIsThreeRowsOfEqualWidth(t *testing.T) {
	mark := wordmark("kagegami")
	want := []string{"╦╔═╔═╗╔═╗╔═╗╔═╗╔═╗╔╦╗╦", "╠╩╗╠═╣║ ╦║╣ ║ ╦╠═╣║║║║", "╩ ╩╩ ╩╚═╝╚═╝╚═╝╩ ╩╩ ╩╩"}
	for i := range mark {
		if string(mark[i]) != want[i] {
			t.Errorf("row %d = %q, want %q", i, string(mark[i]), want[i])
		}
		if lipgloss.Width(string(mark[i])) != splashWidth() {
			t.Errorf("row %d is %d columns wide", i, lipgloss.Width(string(mark[i])))
		}
	}
	if len(wordmark("xyz")[0]) != 0 {
		t.Error("letters with no glyph are left out")
	}
}

func TestTheSplashIsAFunctionOfTheFrame(t *testing.T) {
	for _, f := range []int{0, 5, 12, 30, 63} {
		if a, b := plain(splashLines(f, "v")), plain(splashLines(f, "v")); a != b {
			t.Errorf("frame %d differs between two calls", f)
		}
	}
	if plain(splashLines(3, "v")) == plain(splashLines(4, "v")) {
		t.Error("the noise must move from one frame to the next")
	}
}

// wordmarkRow returns the part of a splash line that holds the wordmark, without the centring.
func wordmarkRow(line string) []rune {
	offset := (max(len([]rune(tagline)), splashWidth()) - splashWidth()) / 2
	return []rune(line)[offset : offset+splashWidth()]
}

func TestTheSplashSettlesFromLeftToRightAndEndsOnTheWordmark(t *testing.T) {
	mark := wordmark("kagegami")
	first := strings.Split(plain(splashLines(0, "v")), "\n")
	for c, ch := range mark[0] {
		if got := wordmarkRow(first[0])[c]; ch == ' ' && got != ' ' {
			t.Fatalf("column %d: noise drawn where the lettering has a gap", c)
		}
	}
	mid := wordmarkRow(strings.Split(plain(splashLines(decodeFrom+8, "v")), "\n")[0])
	if string(mid[:8]) != string(mark[0][:8]) {
		t.Errorf("the first columns must have settled by frame %d: %q", decodeFrom+8, string(mid[:8]))
	}
	if string(mid[len(mid)-3:]) == string(mark[0][len(mark[0])-3:]) {
		t.Error("the last columns must still be noise at that frame")
	}

	end := strings.Split(plain(splashLines(splashFrames-1, "kagegami v1")), "\n")
	for i := range mark {
		if got := string(wordmarkRow(end[i])); got != string(mark[i]) {
			t.Errorf("row %d at the end = %q", i, got)
		}
	}
	if !strings.Contains(end[4], tagline) || !strings.Contains(end[5], "kagegami v1") {
		t.Errorf("the tagline and the version are missing at the end:\n%s", strings.Join(end, "\n"))
	}
}

func TestTheTaglineTypesInPlaceAndTheVersionComesLast(t *testing.T) {
	early := strings.Split(plain(splashLines(sweepFrom, "kagegami v1")), "\n")
	if strings.Contains(early[4], tagline) || strings.TrimSpace(early[4]) == "" {
		t.Errorf("at frame %d the tagline should be partly typed: %q", sweepFrom, early[4])
	}
	if strings.Contains(strings.Join(early, "\n"), "kagegami v1") {
		t.Error("the version must not show before the sweep is over")
	}
	a := splashLines(sweepFrom+4, "v")
	b := splashLines(sweepFrom+10, "v")
	if lipgloss.Width(a[4]) != lipgloss.Width(b[4]) || lipgloss.Width(a[0]) != lipgloss.Width(b[0]) {
		t.Error("lines change width while the tagline types, which would make the wordmark move")
	}
}

func TestAKeyPressSkipsTheSplashAndQQuits(t *testing.T) {
	m := newModel(t, Config{})
	if m.phase != phaseSplash {
		t.Fatal("a panel with animation starts on the splash")
	}
	if cmd := press(m, " "); cmd != nil || m.phase != phaseStream {
		t.Errorf("a key must skip the splash: phase=%v", m.phase)
	}
	if !isQuit(press(m, "q")) || !isQuit(press(m, "ctrl+c")) {
		t.Error("q and ctrl+c quit")
	}
	if m2 := newModel(t, Config{}); !isQuit(press(m2, "q")) {
		t.Error("q quits from the splash too")
	}
}

func TestTheSplashEndsByItselfAndIsSkippedInANarrowPane(t *testing.T) {
	m := newModel(t, Config{})
	for range splashFrames - 1 {
		if _, cmd := m.Update(splashMsg{}); cmd == nil {
			t.Fatal("the splash must keep ticking until it is done")
		}
	}
	if m.phase != phaseSplash {
		t.Fatal("one frame short of the end it is still the splash")
	}
	m.Update(splashMsg{})
	if m.phase != phaseStream {
		t.Error("the splash must end at its last frame")
	}

	narrow := New(Config{Now: fixedNow})
	narrow.Update(tea.WindowSizeMsg{Width: splashWidth(), Height: 10})
	narrow.Update(splashMsg{})
	if narrow.phase != phaseStream {
		t.Error("a pane narrower than the wordmark goes straight to the stream")
	}
	if New(Config{NoAnim: true}).Init() != nil {
		t.Error("with no animation and no demo there is nothing to wait for")
	}
}

func TestTheHelpBlockOpensAndClosesWithItsKey(t *testing.T) {
	m := newModel(t, Config{NoAnim: true})
	m.Init()
	press(m, "?")
	if len(m.events) != 1 || m.events[0].Block != blockHelp || !strings.Contains(plain(m.events[0].Lines), "ask for the next step") {
		t.Fatalf("events = %+v", m.events)
	}
	press(m, "?")
	if len(m.events) != 0 {
		t.Error("the same key closes it")
	}
}

func TestBlocksStayWhereTheyWereOpened(t *testing.T) {
	m := newModel(t, Config{NoAnim: true, Demo: true})
	m.Update(demoMsg{index: 0})
	press(m, "m")
	m.Update(demoMsg{index: 1})
	if len(m.events) != 3 || m.events[1].Block != blockMap || m.events[2].Kind != KindOutput {
		t.Errorf("the map block must stay between the entries around it: %+v", m.events)
	}
	press(m, "m")
	if len(m.events) != 2 {
		t.Errorf("closing it removes it from the middle of the stream: %d entries", len(m.events))
	}
}

func TestPanelsThatNeedACaseSayTheyAreNotConnected(t *testing.T) {
	m := newModel(t, Config{NoAnim: true})
	for _, k := range []string{"m", "p", "n"} {
		press(m, k)
	}
	if len(m.events) != 3 {
		t.Fatalf("%d entries", len(m.events))
	}
	for _, e := range m.events {
		if e.Kind != KindSystem || !strings.Contains(e.Text, "not connected") && !strings.Contains(e.Text, "no model") {
			t.Errorf("entry = %+v", e)
		}
	}
}

func TestAnAssistantTurnSpinsThenTypesThenStopsItsTimers(t *testing.T) {
	m := newModel(t, Config{NoAnim: true, Demo: true})
	cmd := press(m, "n")
	if cmd == nil || !m.spinning {
		t.Fatal("asking starts the spinner")
	}
	turn := m.events[0].Turn
	if turn == nil || turn.State != turnThinking || !strings.Contains(plain(renderEvent(m.events[0], 80)), "thinking") {
		t.Fatal("the turn starts by thinking")
	}
	if _, cmd := m.Update(spinMsg{}); cmd == nil {
		t.Error("the spinner goes on while the turn thinks")
	}

	if _, cmd := m.Update(answerMsg{turn: turn}); cmd == nil || !m.typing {
		t.Fatal("an answer starts the typing")
	}
	ticks := 0
	for m.typing {
		_, cmd := m.Update(typeMsg{})
		ticks++
		if cmd == nil {
			break
		}
		if ticks > 10_000 {
			t.Fatal("typing never ends")
		}
	}
	if want := len([]rune(demoRationale)) + len([]rune(demoCommand)); ticks != want {
		t.Errorf("typing took %d ticks, want one per character (%d)", ticks, want)
	}
	if turn.State != turnDone || m.typing {
		t.Error("when the last character is typed the turn is done and its timer is stopped")
	}
	if _, cmd := m.Update(spinMsg{}); cmd != nil || m.spinning {
		t.Error("with nothing left to spin, the spinner timer stops")
	}
	out := plain(renderEvent(m.events[0], 80))
	if !strings.Contains(out, demoCommand) || !strings.Contains(out, "lists its directory") {
		t.Errorf("the finished turn is missing text:\n%s", out)
	}
}

func TestTheDemoPlaysItsStepsInOrderAndEnds(t *testing.T) {
	m := newModel(t, Config{NoAnim: true, Demo: true})
	script := demoScript()
	if m.Init() == nil {
		t.Fatal("the demo has a first step to wait for")
	}
	var cmd tea.Cmd
	for i := range script {
		_, cmd = m.Update(demoMsg{index: i})
	}
	if cmd != nil {
		t.Error("after the last step nothing more is scheduled")
	}
	if len(m.events) != len(script) {
		t.Fatalf("%d entries for %d steps", len(m.events), len(script))
	}
	kinds := map[Kind]bool{}
	for _, e := range m.events {
		kinds[e.Kind] = true
		if e.Time.IsZero() {
			t.Error("an entry has no time")
		}
	}
	for _, k := range []Kind{KindCommand, KindOutput, KindNote, KindFinding, KindSystem} {
		if !kinds[k] {
			t.Errorf("the demo shows no entry of kind %d", k)
		}
	}
}

func TestEveryLineOfTheViewFitsThePane(t *testing.T) {
	for _, w := range []int{14, 20, 33, 60, 120} {
		m := New(Config{NoAnim: true, Demo: true, Now: fixedNow})
		m.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		m.Init()
		for i := range demoScript() {
			m.Update(demoMsg{index: i})
		}
		press(m, "?")
		press(m, "m")
		press(m, "p")
		press(m, "n")
		m.Update(answerMsg{turn: m.events[len(m.events)-1].Turn})
		for range 400 {
			m.Update(typeMsg{})
		}
		v := m.View()
		lines := strings.Split(v.Content, "\n")
		if len(lines) > 12 {
			t.Errorf("width %d: %d lines for a pane of 12", w, len(lines))
		}
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("width %d: a line is %d columns: %q", w, lipgloss.Width(l), ansi.Strip(l))
			}
		}
		if !v.AltScreen {
			t.Error("the panel takes the whole pane")
		}
	}
}

func TestTextHangsUnderTheGutter(t *testing.T) {
	long := "gobuster dir -u http://10.10.11.42 -w /usr/share/wordlists/dirb/common.txt -t 50"
	lines := renderEvent(Event{Kind: KindCommand, Time: fixedNow(), Text: long}, 40)
	if len(lines) < 2 {
		t.Fatalf("a long command must wrap: %v", lines)
	}
	first := ansi.Strip(lines[0])
	if !strings.HasPrefix(first, "15:04 $ gobuster") {
		t.Errorf("first line = %q", first)
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(ansi.Strip(l), strings.Repeat(" ", gutterWidth)) {
			t.Errorf("a continuation line does not hang from the text: %q", ansi.Strip(l))
		}
	}
}

func TestNotesAndFindingsPutTheirMarkerInTheTimeSlot(t *testing.T) {
	note := ansi.Strip(renderEvent(Event{Kind: KindNote, Time: fixedNow(), Text: "look at the backup"}, 60)[0])
	find := ansi.Strip(renderEvent(Event{Kind: KindFinding, Time: fixedNow(), Text: "open listing"}, 60)[0])
	if !strings.HasPrefix(strings.TrimLeft(note, " "), GlyphReport) || strings.Contains(note, "15:04") {
		t.Errorf("note = %q", note)
	}
	if !strings.HasPrefix(strings.TrimLeft(find, " "), "!") || strings.Contains(find, "15:04") {
		t.Errorf("finding = %q", find)
	}
}

func TestEmphasizeKeepsTheTextAndBringsOutTheWords(t *testing.T) {
	text := "22/tcp open ssh · 80/tcp open http"
	got := emphasize(text, []string{"open"})
	if ansi.Strip(got) != text {
		t.Errorf("text changed: %q", ansi.Strip(got))
	}
	if got == styleDim.Render(text) {
		t.Error("the words are not brought out")
	}
	if ansi.Strip(emphasize(text, nil)) != text || ansi.Strip(emphasize(text, []string{""})) != text {
		t.Error("no words, no change")
	}
}

func TestTheTurnKnowsWhenItNeedsATimer(t *testing.T) {
	turn := NewTurn()
	if !turn.Busy() || turn.Ticks() != spinTick {
		t.Error("a thinking turn needs the spinner timer")
	}
	turn.Spun()
	turn.Answer("ab", "c")
	if turn.Ticks() != typeTick {
		t.Error("a typing turn needs the typing timer")
	}
	turn.Typed()
	if r, c := turn.Shown(); r != "a" || c != "" {
		t.Errorf("shown = %q, %q", r, c)
	}
	turn.Typed()
	turn.Typed()
	if r, c := turn.Shown(); r != "ab" || c != "c" || turn.Busy() {
		t.Errorf("shown = %q, %q, busy = %v", r, c, turn.Busy())
	}
	turn.Typed() // typing past the end changes nothing
	if _, c := turn.Shown(); c != "c" {
		t.Error("a finished turn stays as it is")
	}
}
