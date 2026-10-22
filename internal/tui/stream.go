package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The gutter is a time slot of five columns and a one-character marker; the text hangs from it.
const (
	slotWidth   = 5
	gutterWidth = slotWidth + 1 + 1 + 1
	// A pane narrower than this has no time slot: only the marker is left of the text.
	compactBelow  = 24
	compactGutter = 2
	minText       = 4
)

func gutterFor(compact bool, slot, marker string, slotStyle, markerStyle lipgloss.Style) string {
	if compact {
		if strings.TrimSpace(marker) == "" {
			marker = slot
		}
		if strings.TrimSpace(marker) == "" {
			marker = " "
		}
		return markerStyle.Render(marker) + " "
	}
	pad := slotWidth - lipgloss.Width(slot)
	left := pad / 2
	return strings.Repeat(" ", left) + slotStyle.Render(slot) + strings.Repeat(" ", pad-left) + " " + markerStyle.Render(marker) + " "
}

// renderEvent returns the lines of an entry for a pane of the given width.
func renderEvent(e Event, width int) []string {
	compact := width < compactBelow
	gw := gutterWidth
	if compact {
		gw = compactGutter
	}
	text := max(width-gw, minText)
	indent := strings.Repeat(" ", gw)
	gutter := func(slot, marker string, slotStyle, markerStyle lipgloss.Style) string {
		return gutterFor(compact, slot, marker, slotStyle, markerStyle)
	}
	clock := ""
	if !e.Time.IsZero() {
		clock = e.Time.Format("15:04")
	}

	hang := func(first string, body string, style lipgloss.Style) []string {
		var out []string
		for i, l := range strings.Split(lipgloss.NewStyle().Width(text).Render(body), "\n") {
			l = strings.TrimRight(l, " ")
			if i == 0 {
				out = append(out, first+style.Render(l))
			} else {
				out = append(out, indent+style.Render(l))
			}
		}
		return out
	}

	switch e.Kind {
	case KindCommand:
		return hang(gutter(clock, "$", styleDim, styleLo), e.Text, styleMid)
	case KindOutput:
		return hang(gutter("", " ", styleDim, styleLo), emphasize(e.Text, e.Words), lipgloss.NewStyle())
	case KindNote:
		return hang(gutter(GlyphReport, " ", styleUser, styleUser), e.Text, styleUser)
	case KindFinding:
		return hang(gutter("!", " ", styleFind, styleFind), e.Text, styleFind)
	case KindSystem:
		return hang(gutter(clock, e.Glyph, styleDim, styleLo), e.Text, styleLo)
	case KindAssistant:
		return renderTurn(e, clock, text, indent, gutter)
	case KindBlock:
		out := make([]string, 0, len(e.Lines)+1)
		out = append(out, "") // a block is set apart from the stream above it
		for _, l := range e.Lines {
			out = append(out, indent+ansi.Truncate(l, text, "…"))
		}
		return out
	}
	return nil
}

func renderTurn(e Event, clock string, text int, indent string, gutter func(slot, marker string, slotStyle, markerStyle lipgloss.Style) string) []string {
	t := e.Turn
	if t == nil {
		return nil
	}
	if t.State == turnThinking {
		return []string{gutter(clock, "›", styleDim, styleMid) + styleMid.Render(string(spinFrames[t.Spin])+" thinking")}
	}
	rationale, command := t.Shown()
	var out []string
	for i, l := range strings.Split(lipgloss.NewStyle().Width(text).Render(rationale), "\n") {
		l = strings.TrimRight(l, " ")
		if i == 0 {
			out = append(out, gutter(clock, "›", styleDim, styleMid)+styleMid.Render(l))
		} else {
			out = append(out, indent+styleMid.Render(l))
		}
	}
	if command != "" || t.State == turnCommand {
		box := styleBlock.Width(text).Render(command)
		for _, l := range strings.Split(box, "\n") {
			out = append(out, indent+l)
		}
	}
	return out
}

// emphasize styles an output line: dim, with the words brought out. The result carries its own styles.
func emphasize(text string, words []string) string {
	var b strings.Builder
	for len(text) > 0 {
		at, hit := -1, ""
		for _, w := range words {
			if w == "" {
				continue
			}
			if i := strings.Index(text, w); i >= 0 && (at < 0 || i < at) {
				at, hit = i, w
			}
		}
		if at < 0 {
			b.WriteString(styleDim.Render(text))
			break
		}
		if at > 0 {
			b.WriteString(styleDim.Render(text[:at]))
		}
		b.WriteString(styleMid.Render(hit))
		text = text[at+len(hit):]
	}
	return b.String()
}
