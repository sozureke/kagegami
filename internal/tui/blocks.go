package tui

// Names of the blocks a key opens and closes in the stream.
const (
	blockHelp = "help"
)

func key(k, what string) string {
	return "  " + styleHi.Render(k) + "  " + styleMid.Render(what)
}

func helpLines() []string {
	return []string{
		styleHi.Render("keys"),
		key("n", "ask for the next step"),
		key("m", "the map"),
		key("p", "the pipeline"),
		key("?", "this help"),
		key("q", "quit"),
		"",
		styleHi.Render("the stream"),
		"  " + styleLo.Render("$") + styleMid.Render(" command   ") + styleLo.Render(GlyphMap) + styleMid.Render(" map   ") +
			styleLo.Render(GlyphPipeline) + styleMid.Render(" pipeline"),
		"  " + styleUser.Render(GlyphReport) + styleMid.Render(" note   ") + styleFind.Render("!") + styleMid.Render(" finding   ") +
			styleMid.Render("› assistant"),
	}
}
