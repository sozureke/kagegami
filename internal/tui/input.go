package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// input is a single editable line at the bottom of the panel, where the tester types messages and
// slash commands. It keeps only a buffer and a cursor; the model decides what a submitted line means.
type input struct {
	runes  []rune
	cursor int
}

// handle applies a key to the input and says whether the key was for it. It returns the submitted
// line (and true) on Enter; an empty submit is a line with nothing but spaces.
func (in *input) handle(k tea.KeyPressMsg) (submit string, submitted, used bool) {
	switch k.String() {
	case "enter":
		s := string(in.runes)
		in.runes, in.cursor = nil, 0
		return s, true, true
	case "backspace":
		if in.cursor > 0 {
			in.runes = append(in.runes[:in.cursor-1], in.runes[in.cursor:]...)
			in.cursor--
		}
		return "", false, true
	case "delete":
		if in.cursor < len(in.runes) {
			in.runes = append(in.runes[:in.cursor], in.runes[in.cursor+1:]...)
		}
		return "", false, true
	case "left":
		if in.cursor > 0 {
			in.cursor--
		}
		return "", false, true
	case "right":
		if in.cursor < len(in.runes) {
			in.cursor++
		}
		return "", false, true
	case "home", "ctrl+a":
		in.cursor = 0
		return "", false, true
	case "end", "ctrl+e":
		in.cursor = len(in.runes)
		return "", false, true
	case "ctrl+u":
		in.runes, in.cursor = nil, 0
		return "", false, true
	case "ctrl+w":
		in.deleteWord()
		return "", false, true
	}
	if text := k.Text; text != "" { // a printable key carries its characters
		for _, r := range text {
			in.runes = append(in.runes[:in.cursor], append([]rune{r}, in.runes[in.cursor:]...)...)
			in.cursor++
		}
		return "", false, true
	}
	return "", false, false // a key the input does not use (ctrl+c, a function key)
}

// deleteWord removes the word before the cursor, and the spaces before it.
func (in *input) deleteWord() {
	i := in.cursor
	for i > 0 && in.runes[i-1] == ' ' {
		i--
	}
	for i > 0 && in.runes[i-1] != ' ' {
		i--
	}
	in.runes = append(in.runes[:i], in.runes[in.cursor:]...)
	in.cursor = i
}

func (in *input) empty() bool { return len(in.runes) == 0 }

// view renders the input line to a width, with a prompt and the cursor shown when the panel has the
// keyboard. The text scrolls so that the cursor is always in view.
func (in *input) view(width int, focused bool) string {
	const prompt = "❯ "
	room := max(width-lipgloss.Width(prompt), 1)
	runes := in.runes

	start := 0
	if in.cursor >= room {
		start = in.cursor - room + 1
	}
	end := min(start+room, len(runes))
	visible := append([]rune(nil), runes[start:end]...)
	rel := in.cursor - start

	var body string
	if focused {
		// a block cursor: the character under it is reversed, or a reversed space at the end
		under := " "
		if rel < len(visible) {
			under = string(visible[rel])
			visible = append(visible[:rel], visible[rel+1:]...)
		}
		left := string(visible[:rel])
		right := ""
		if rel < len(visible) {
			right = string(visible[rel:])
		}
		body = styleHi.Render(left) + styleCursor.Render(under) + styleHi.Render(right)
	} else {
		body = styleLo.Render(string(visible))
	}
	line := stylePrompt.Render(prompt) + body
	return ansi.Truncate(line, width, "")
}
