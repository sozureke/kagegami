package tui

import "strings"

// Names of the blocks a key opens and closes in the stream.
const (
	blockHelp     = "help"
	blockMap      = "map"
	blockPipeline = "pipeline"
)

// blockLines styles the plain text of a block the backend returned, under a heading.
func blockLines(name, text string) []string {
	body := strings.Split(strings.TrimRight(text, "\n"), "\n")
	lines := make([]string, 0, len(body)+1)
	lines = append(lines, styleHi.Render(name))
	for _, l := range body {
		lines = append(lines, styleMid.Render(l))
	}
	return lines
}

func key(k, what string) string {
	return "  " + styleHi.Render(k) + "  " + styleMid.Render(what)
}

func helpLines() []string {
	return []string{
		styleHi.Render("keys"),
		key("n", "ask for the next step"),
		key("m", "the map (kept up to date while it is open)"),
		key("p", "the pipeline: the latest, or a new one if there is none"),
		key("P", "ask for a new pipeline"),
		key("c", "copy the suggested command"),
		key("?", "this help"),
		key("q", "quit"),
	}
}
