package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Card is the welcome the panel opens with: who and where on the left, what to do first on the right,
// in a box of its own at the top of the stream.
type Card struct {
	// Title sits in the top line of the box, such as "kagegami v0.1".
	Title string
	// Heading is the greeting, such as "Welcome back!".
	Heading string
	// Facts are the lines under the greeting: the case, the model, the directory.
	Facts []string
	// Tips say what to do first.
	Tips []string
}

const (
	cardMinTwoColumns = 64
	cardMinWidth      = 24
	cardLeft          = 30 // width of the left column
)

// renderCard draws the card for a pane of the given width: two columns when there is room, and one
// under the other when there is not.
func renderCard(c Card, width int) []string {
	if width < cardMinWidth {
		return nil
	}
	var (
		cells func(left, right []string) []string
		left  []string
		right []string
		lw    int
		rw    int
	)
	two := width >= cardMinTwoColumns
	if two {
		lw = cardLeft
		rw = width - 7 - lw // "│ " left " │ " right " │"
		left = cardLeftColumn(c, lw)
		right = cardRightColumn(c, rw)
	} else {
		lw = width - 4
		left = cardLeftColumn(c, lw)
		right = cardRightColumn(c, lw)
	}
	edge := styleLo
	side := edge.Render("│")
	cells = func(l, r []string) []string {
		var out []string
		for i := range max(len(l), len(r)) {
			a, b := "", ""
			if i < len(l) {
				a = l[i]
			}
			if i < len(r) {
				b = r[i]
			}
			if two {
				out = append(out, side+" "+padTo(a, lw)+" "+side+" "+padTo(b, rw)+" "+side)
			} else {
				out = append(out, side+" "+padTo(a, lw)+" "+side)
			}
		}
		return out
	}

	out := []string{cardTop(c.Title, width)}
	if two {
		out = append(out, cells(left, right)...)
	} else {
		out = append(out, cells(left, nil)...)
		out = append(out, side+" "+padTo(styleLo.Render(strings.Repeat("─", lw)), lw)+" "+side)
		out = append(out, cells(right, nil)...)
	}
	out = append(out, edge.Render("╰"+strings.Repeat("─", width-2)+"╯"), "")
	return out
}

func cardTop(title string, width int) string {
	dashes := width - 5 - lipgloss.Width(title)
	if title == "" || dashes < 1 {
		return styleLo.Render("╭" + strings.Repeat("─", width-2) + "╮")
	}
	return styleLo.Render("╭─ ") + styleMid.Render(title) + styleLo.Render(" "+strings.Repeat("─", dashes)+"╮")
}

// cardLeftColumn is the greeting and the facts, centred in the column.
func cardLeftColumn(c Card, w int) []string {
	var out []string
	if c.Heading != "" {
		out = append(out, centre(styleHi.Render(ansi.Truncate(c.Heading, w, "…")), w), "")
	}
	for _, f := range c.Facts {
		if !strings.Contains(f, " ") && lipgloss.Width(f) > w { // a path: its end is what tells it apart
			f = ansi.TruncateLeft(f, lipgloss.Width(f)-w+1, "…")
		}
		for _, l := range wrap(f, w) {
			out = append(out, centre(styleText.Render(l), w))
		}
	}
	return out
}

// cardRightColumn is the tips.
func cardRightColumn(c Card, w int) []string {
	var out []string
	out = append(out, stylePrompt.Render("Tips for getting started"))
	for _, t := range c.Tips {
		for _, l := range wrap(t, w) {
			out = append(out, styleText.Render(l))
		}
	}
	return out
}

func wrap(s string, w int) []string { return strings.Split(ansi.Wrap(s, max(w, 1), ""), "\n") }

func centre(s string, w int) string {
	return strings.Repeat(" ", max((w-lipgloss.Width(s))/2, 0)) + s
}

// padTo fills a styled cell with spaces to a width, cutting what is longer.
func padTo(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}
