package tui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// The opening animation: the letters of the wordmark flicker like noise and settle from left to
// right, a bright band sweeps across them, and the tagline types itself out under it. It is a
// function of the frame number alone, so it can be tested without a terminal.
const (
	splashTick   = 40 * time.Millisecond
	decodeFrom   = 2   // the first column settles at this frame
	sweepFrom    = 20  // the bright band starts to move
	sweepSpeed   = 1.4 // columns per frame
	splashFrames = 64  // the frame at which the splash ends by itself (about 2.6 s)
	tagline      = "AI pentest copilot for your terminal"
)

var noiseRunes = []rune("░▒▓╳·:;")

// splashWidth is the width of the wordmark, the narrowest pane the animation is shown in.
func splashWidth() int { return len(wordmark("kagegami")[0]) }

// noise picks a rune for a cell that has not settled yet, the same one for the same frame and cell.
func noise(frame, row, col int) rune {
	h := (frame*73856093 ^ row*19349663 ^ col*83492791) & 0x7fffffff
	h ^= h >> 13
	h = (h * 0x5bd1e995) & 0x7fffffff
	h ^= h >> 15
	return noiseRunes[h%len(noiseRunes)]
}

// splashLines returns the lines of the animation at a frame, without any centring.
func splashLines(frame int, version string) []string {
	mark := wordmark("kagegami")
	band := float64(frame-sweepFrom)*sweepSpeed - 3

	lines := make([]string, 0, 6)
	for r, row := range mark {
		var b strings.Builder
		for c, ch := range row {
			if ch == ' ' {
				b.WriteRune(' ')
				continue
			}
			if frame < decodeFrom+c {
				b.WriteString(styleLo.Render(string(noise(frame, r, c))))
				continue
			}
			b.WriteString(cellStyle(frame, float64(c), band).Render(string(ch)))
		}
		lines = append(lines, b.String())
	}

	lines = append(lines, "")
	full := []rune(tagline)
	typed := min(max((frame-sweepFrom+8)*2, 0), len(full))
	// the tagline is padded to its final length before it is centred, so it types out in place
	lines = append(lines, styleMid.Render(string(full[:typed]))+strings.Repeat(" ", len(full)-typed))
	if frame >= sweepFrom+24 {
		lines = append(lines, styleLo.Render(version))
	} else {
		lines = append(lines, "")
	}

	// every line is centred on the widest one, so that the wordmark does not move while text types
	block := max(len(full), len(mark[0]))
	for i, l := range lines {
		lines[i] = lipgloss.PlaceHorizontal(block, lipgloss.Center, l)
	}
	return lines
}

// cellStyle is the look of a settled letter: dim until the band reaches it, bright inside the band,
// and chrome after it has passed.
func cellStyle(frame int, col, band float64) lipgloss.Style {
	if frame < sweepFrom {
		return styleLo
	}
	d := col - band
	switch {
	case d > 3:
		return styleLo
	case d >= -1 && d <= 1:
		return styleHi
	case d >= -3 && d <= 3:
		return fg(chromeHi)
	}
	return styleMid
}

// splashView centres the animation in a pane.
func splashView(frame int, version string, width, height int) string {
	body := strings.Join(splashLines(frame, version), "\n")
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, body)
}
