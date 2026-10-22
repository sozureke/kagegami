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

func TestTheHelpBlockOpensAndClosesWithItsKey(t *testing.T) {
	m := newModel(t, Config{})
	press(m, "?")
	if len(m.events) != 1 || m.events[0].Block != blockHelp || !strings.Contains(plain(m.events[0].Lines), "ask for the next step") {
		t.Fatalf("events = %+v", m.events)
	}
	press(m, "?")
	if len(m.events) != 0 {
		t.Error("the same key closes it")
	}
}

func TestPanelsThatNeedACaseSayTheyAreNotConnected(t *testing.T) {
	m := newModel(t, Config{})
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

func TestQAndCtrlCQuit(t *testing.T) {
	m := newModel(t, Config{})
	if m.Init() != nil {
		t.Error("the panel has nothing to start")
	}
	if !isQuit(press(m, "q")) || !isQuit(press(m, "ctrl+c")) {
		t.Error("q and ctrl+c quit")
	}
	if press(m, "x") != nil || len(m.events) != 0 {
		t.Error("a key with no meaning does nothing")
	}
}

func TestAnEmptyPanelSaysHowToGetHelp(t *testing.T) {
	m := newModel(t, Config{})
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "press ? for the keys") {
		t.Errorf("view = %q", got)
	}
}

func TestEveryLineOfTheViewFitsThePane(t *testing.T) {
	turn := NewTurn()
	turn.Answer("/backup/ lists its directory and nginx serves it on :80, so the listing shows what is in it", "curl -s http://10.10.11.42/backup/")
	for range 400 {
		turn.Typed()
	}
	events := []Event{
		{Kind: KindCommand, Time: fixedNow(), Text: "gobuster dir -u http://10.10.11.42 -w /usr/share/wordlists/dirb/common.txt"},
		{Kind: KindOutput, Text: "/admin (301) · /login (200) · /backup (200)", Words: []string{"/admin"}},
		{Kind: KindSystem, Time: fixedNow(), Glyph: GlyphMap, Text: ":80 gained 3 paths, from gobuster"},
		{Kind: KindFinding, Text: "/backup/ lists its directory (medium)"},
		{Kind: KindNote, Text: "check whether the backup holds a database dump"},
		{Kind: KindAssistant, Time: fixedNow(), Turn: turn},
		{Kind: KindBlock, Block: blockHelp, Lines: helpLines()},
	}
	for _, w := range []int{14, 20, 33, 60, 120} {
		m := New(Config{Now: fixedNow})
		m.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		m.events = events
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

func TestATurnShowsItsSpinnerThenItsTextAndCommand(t *testing.T) {
	turn := NewTurn()
	ev := Event{Kind: KindAssistant, Time: fixedNow(), Turn: turn}
	if !strings.Contains(plain(renderEvent(ev, 80)), "thinking") {
		t.Error("a thinking turn shows the spinner")
	}
	turn.Answer("the reason", "curl -s http://x/")
	for turn.Busy() {
		turn.Typed()
	}
	out := plain(renderEvent(ev, 80))
	if !strings.Contains(out, "the reason") || !strings.Contains(out, "curl -s http://x/") {
		t.Errorf("finished turn:\n%s", out)
	}
}
