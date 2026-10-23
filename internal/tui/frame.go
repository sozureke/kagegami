package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// minFrame is the narrowest pane that gets an outline; a narrower one keeps the room for the text.
const minFrame = 24

// frame draws an outline around the lines, filling a pane of the given size. The outline is bright
// when the panel has the keyboard and dim when it does not, so that the person can tell at a glance
// which pane they are in; the title sits in the top line.
func frame(lines []string, width, height int, focused bool, title string) string {
	if width < minFrame || height < 3 {
		return strings.Join(lines, "\n")
	}
	edge, name := styleLo, styleDim
	if focused {
		edge, name = styleHi, styleHi
	}
	inner := width - 2

	out := make([]string, 0, height)
	// "╭─ " + title + " " + dashes + "╮" is as wide as the pane
	if dashes := inner - 3 - lipgloss.Width(title); dashes >= 1 {
		out = append(out, edge.Render("╭─ ")+name.Render(title)+edge.Render(" "+strings.Repeat("─", dashes)+"╮"))
	} else { // no room for the title: a plain line
		out = append(out, edge.Render("╭"+strings.Repeat("─", inner)+"╮"))
	}

	side := edge.Render("│")
	for i := range height - 2 {
		var l string
		if i < len(lines) {
			l = ansi.Truncate(lines[i], inner-2, "")
		}
		pad := max(inner-2-lipgloss.Width(l), 0)
		out = append(out, side+" "+l+strings.Repeat(" ", pad)+" "+side)
	}
	out = append(out, edge.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(out, "\n")
}
