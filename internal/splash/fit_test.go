package splash

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func blockArt(w, h int, ch string) string {
	return strings.TrimSuffix(strings.Repeat(strings.Repeat(ch, w)+"\n", h), "\n")
}

func dims(art string) (w, h int) {
	rows := strings.Split(art, "\n")
	for _, r := range rows {
		w = max(w, len([]rune(r)))
	}
	return w, len(rows)
}

func TestFitLeavesArtThatFitsAlone(t *testing.T) {
	art := blockArt(20, 6, "#")
	cfg := DefaultConfig()
	if got := Fit(art, 80, 24, cfg); got != art {
		t.Errorf("art that fits was changed:\n%s", got)
	}
	// short lines come back padded to one width
	if got := Fit("ab\nabcd", 80, 24, cfg); got != "ab  \nabcd" {
		t.Errorf("padded = %q", got)
	}
	if Fit("", 80, 24, cfg) != "" || Fit("\n\n", 80, 24, cfg) != "" {
		t.Error("no art, no output")
	}
}

func TestFitShrinksToTheTerminalWithAMargin(t *testing.T) {
	art := blockArt(180, 89, "d")
	cfg := DefaultConfig()
	for _, size := range [][2]int{{200, 50}, {80, 24}, {60, 40}, {30, 10}} {
		w, h := dims(Fit(art, size[0], size[1], cfg))
		if w > size[0]-2 || h > size[1] {
			t.Errorf("terminal %dx%d: art is %dx%d", size[0], size[1], w, h)
		}
		if w < 1 || h < 1 {
			t.Errorf("terminal %dx%d: art vanished", size[0], size[1])
		}
	}
	// the shape stays: shrinking by one factor keeps the ratio within a cell of rounding
	w, h := dims(Fit(art, 100, 100, cfg))
	if r := float64(w) / float64(h); r < 1.9 || r > 2.1 {
		t.Errorf("a 180x89 art became %dx%d", w, h)
	}
}

func TestFitNeverEnlargesAndHonoursScaleAndFit(t *testing.T) {
	art := blockArt(40, 20, "#")
	cfg := DefaultConfig()
	if w, h := dims(Fit(art, 400, 400, cfg)); w != 40 || h != 20 {
		t.Errorf("a big terminal enlarged the art to %dx%d", w, h)
	}
	cfg.Scale = 0.5
	if w, h := dims(Fit(art, 400, 400, cfg)); w != 20 || h != 10 {
		t.Errorf("scale 0.5 gave %dx%d, want 20x10", w, h)
	}
	cfg.Fit = false
	if w, h := dims(Fit(art, 10, 5, cfg)); w != 20 || h != 10 {
		t.Errorf("with fit off the terminal does not matter: %dx%d", w, h)
	}
	cfg.Scale = 3
	if w, h := dims(Fit(art, 400, 400, cfg)); w != 40 || h != 20 {
		t.Errorf("scale above 1 enlarged the art to %dx%d", w, h)
	}
}

func TestShrinkingClearsSpeckleKeepsInkAndKeepsTone(t *testing.T) {
	// a lone speck in a block of 4 is under minCover and goes; a half-filled block stays
	art := strings.Join([]string{
		"#.  ..",
		"    ..",
		"    ..",
		"    ..",
	}, "\n")
	rows := padded(art)
	out := shrink(rows, 0.5)
	if len(out) != 2 || len(out[0]) != 3 {
		t.Fatalf("shrunk to %dx%d", len(out[0]), len(out))
	}
	if out[0][0] == ' ' {
		t.Error("two glyphs in a block of four are ink and must show")
	}
	if out[0][1] != ' ' || out[1][0] != ' ' {
		t.Errorf("empty blocks must stay empty: %q", string(out[0]))
	}
	if out[0][2] == ' ' || out[1][2] == ' ' {
		t.Error("a block of dots must show something")
	}

	light, dense := shrink(padded(blockArt(4, 4, ".")), 0.5), shrink(padded(blockArt(4, 4, "@")), 0.5)
	if glyphWeight(light[0][0]) >= glyphWeight(dense[0][0]) {
		t.Errorf("a dense block (%q) must be heavier than a light one (%q)", string(dense[0][0]), string(light[0][0]))
	}
	if got := shrink(padded(blockArt(4, 4, "█")), 0.5)[0][0]; glyphWeight(got) != len(densRunes)-1 {
		t.Errorf("a glyph that is not on the scale counts as the densest, got %q", string(got))
	}
}

func TestShrinkHandlesTinyAndOddSizes(t *testing.T) {
	for _, f := range []float64{0.9, 0.5, 0.33, 0.1, 0.01} {
		for _, art := range []string{"#", "##\n##", blockArt(7, 3, "#"), blockArt(1, 50, "#"), blockArt(50, 1, "#")} {
			w, h := dims(join(shrink(padded(art), f)))
			if w < 1 || h < 1 {
				t.Errorf("factor %v: %q shrank to nothing", f, art)
			}
		}
	}
}

func TestTheArtIsFittedWhenTheSizeIsKnownAndAgainOnResize(t *testing.T) {
	art := blockArt(180, 89, "d")
	cfg := seeded(1)
	m := &model{art: art, cfg: cfg}
	if m.state != nil {
		t.Fatal("no state before the size is known")
	}
	if v := m.View(); v.Content != "" {
		t.Error("nothing is drawn before the size is known")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if w, h := m.state.Size(); w > 98 || h > 30 {
		t.Fatalf("the art is %dx%d in a 100x30 terminal", w, h)
	}
	for range 40 {
		m.Update(tickMsg{})
	}
	tick, seed := m.state.tick, m.state.Seed()

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if w, h := m.state.Size(); w > 58 || h > 20 {
		t.Errorf("after a resize the art is %dx%d in 60x20", w, h)
	}
	if m.state.tick != tick || m.state.Seed() != seed {
		t.Errorf("a resize must keep the frame and the seed: tick %d->%d", tick, m.state.tick)
	}
	same := m.state
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if m.state != same {
		t.Error("the same size again must not rebuild anything")
	}
}

func TestWithoutASizeTheAnimationGoesOnWithACommonOne(t *testing.T) {
	cfg := seeded(1)
	m := &model{art: testArt, cfg: cfg}
	for range cfg.FPS - 1 {
		if _, cmd := m.Update(tickMsg{}); cmd == nil || m.state != nil {
			t.Fatal("until a second has passed the animation waits for a size")
		}
	}
	m.Update(tickMsg{})
	if m.state == nil || m.width != 80 || m.height != 24 {
		t.Errorf("after a second it goes on at 80x24: %v %dx%d", m.state != nil, m.width, m.height)
	}
}

func TestTheEmptyMarginsAreCutAndNothingElse(t *testing.T) {
	art := "\n\n      \n   ab  \n    c   \n\n"
	if got := Fit(art, 80, 24, DefaultConfig()); got != "ab\n c" {
		t.Errorf("Fit = %q, want the drawing without its margins", got)
	}
}

func TestShrinkingUsesOnlyTheGlyphsOfTheArt(t *testing.T) {
	art := strings.Join([]string{
		"dd00vvii66rr",
		"d0d0vivi6r6r",
		"00ddiivv66rr",
		"0d0dvivi6r6r",
	}, "\n")
	allowed := "d0vi6r "
	for _, f := range []float64{0.9, 0.7, 0.5, 0.34} {
		for _, r := range join(shrink(padded(art), f)) {
			if r != '\n' && !strings.ContainsRune(allowed, r) {
				t.Errorf("factor %v brought in %q, which the art does not use", f, r)
			}
		}
	}
}

func TestHalvingAnArtDrawnInBlocksGivesTheSameDrawing(t *testing.T) {
	// each glyph of the small drawing is a 2x2 block of the big one; halving must give it back
	small := []string{"d0 v", " i6r", "dd  "}
	var big []string
	for _, row := range small {
		var b strings.Builder
		for _, r := range row {
			b.WriteRune(r)
			b.WriteRune(r)
		}
		big = append(big, b.String(), b.String())
	}
	if got := join(shrink(padded(strings.Join(big, "\n")), 0.5)); got != strings.Join(small, "\n") {
		t.Errorf("halved:\n%s\nwant:\n%s", got, strings.Join(small, "\n"))
	}
}
