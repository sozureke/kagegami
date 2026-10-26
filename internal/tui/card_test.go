package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func sampleCard() *Card {
	return &Card{
		Title:   "kagegami v0.1",
		Heading: "Welcome back!",
		Facts:   []string{"case htb", "openrouter/openai/gpt-4o-mini", "~/Documents/Projects/kagegami"},
		Tips:    []string{"Run your recon in the other pane: I read every command and say what is next.", "/ lists the commands"},
	}
}

func TestTheWelcomeCardHasTwoColumnsWhenThereIsRoomAndOneWhenThereIsNot(t *testing.T) {
	for _, width := range []int{80, 70, 64, 50, 30, 24} {
		lines := renderCard(*sampleCard(), width)
		if len(lines) == 0 {
			t.Fatalf("width %d: no card", width)
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != width && l != "" {
				t.Errorf("width %d line %d is %d columns: %q", width, i, w, ansi.Strip(l))
			}
		}
		out := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"kagegami v0.1", "Welcome back!", "Tips for getting started"} {
			if width >= 50 && !strings.Contains(out, want) {
				t.Errorf("width %d lacks %q:\n%s", width, want, out)
			}
		}
		beside := false
		for _, l := range strings.Split(out, "\n") {
			beside = beside || strings.Contains(l, "Welcome back!") && strings.Contains(l, "Tips for getting started")
		}
		if two := beside; two != (width >= cardMinTwoColumns) {
			t.Errorf("width %d: the tips beside the greeting (two columns) = %v", width, two)
		}
	}
	if renderCard(*sampleCard(), 10) != nil {
		t.Error("a pane too narrow for a box gets no card")
	}
}

func TestThePanelOpensWithTheCardBeforeTheNotice(t *testing.T) {
	m := newModel(t, Config{Welcome: sampleCard(), Notice: "a warning"})
	if len(m.events) != 2 || m.events[0].Kind != KindCard || m.events[1].Text != "a warning" {
		t.Fatalf("events = %+v", m.events)
	}
	if screen := ansi.Strip(m.View().Content); !strings.Contains(screen, "Welcome back!") {
		t.Errorf("the card is on the screen:\n%s", screen)
	}
}

func TestWhatTheAppSaysIsBrighterThanTheClockAndTheMarker(t *testing.T) {
	if styleText.GetForeground() == styleLo.GetForeground() || styleText.GetForeground() == styleDim.GetForeground() {
		t.Error("the text of the app must not be in a dim tone")
	}
	got := renderEvent(Event{Kind: KindSystem, Time: fixedNow(), Glyph: GlyphPipeline, Text: "task taken"}, 60)[0]
	if !strings.Contains(got, styleText.Render("task taken")) {
		t.Errorf("the body of a system line uses the bright tone: %q", got)
	}
}

func TestALongPathInTheCardKeepsItsEnd(t *testing.T) {
	c := sampleCard()
	c.Facts = []string{"/very/long/path/to/some/deep/directory/my-box"}
	out := ansi.Strip(strings.Join(renderCard(*c, 70), "\n"))
	if !strings.Contains(out, "…") || !strings.Contains(out, "directory/my-box") {
		t.Errorf("card:\n%s", out)
	}
}

func TestAThinkingAnswerBlinksThroughItsFramesAndLoops(t *testing.T) {
	ev := Event{Kind: KindAssistant, Time: fixedNow(), Turn: NewTurn()}
	seen := map[string]bool{}
	for range spinCycle {
		lines := renderEvent(ev, 60)
		if len(lines) != 1 || !strings.Contains(plain(lines), "thinking") {
			t.Fatalf("a thinking answer is one line: %q", plain(lines))
		}
		for _, f := range workFrames {
			if strings.HasPrefix(strings.TrimLeft(plain(lines)[strings.Index(plain(lines), "›"):], "› "), f) {
				seen[f] = true
			}
		}
		ev.Turn.Spun()
	}
	for _, f := range []string{"◉", "◎", "—"} {
		if !seen[f] {
			t.Errorf("the frame %q never showed in a cycle", f)
		}
	}
	if ev.Turn.Spin != 0 {
		t.Errorf("after a whole cycle the marker is back at the start, Spin = %d", ev.Turn.Spin)
	}
	if workFrame(0) != workFrames[0] || workFrame(workEvery*len(workFrames)) != workFrames[0] {
		t.Error("the frames loop")
	}
	if workFrame(workEvery*3) != "◎" || workFrame(workEvery*4) != "—" {
		t.Error("the blink comes after three open frames: ◎ then —")
	}
}
