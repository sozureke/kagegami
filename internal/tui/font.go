package tui

// glyphs is the wordmark's lettering, three rows tall so that it fits a narrow pane.
var glyphs = map[rune][3]string{
	'k': {"╦╔═", "╠╩╗", "╩ ╩"},
	'a': {"╔═╗", "╠═╣", "╩ ╩"},
	'g': {"╔═╗", "║ ╦", "╚═╝"},
	'e': {"╔═╗", "║╣ ", "╚═╝"},
	'm': {"╔╦╗", "║║║", "╩ ╩"},
	'i': {"╦", "║", "╩"},
}

// wordmark returns the three rows of text drawn in the lettering. Letters it has no glyph for are left out.
func wordmark(text string) [3][]rune {
	var rows [3]string
	for _, r := range text {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for i := range rows {
			rows[i] += g[i]
		}
	}
	var out [3][]rune
	for i := range rows {
		out[i] = []rune(rows[i])
	}
	return out
}
