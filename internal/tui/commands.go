package tui

import "strings"

// A slash command typed in the panel. They are what the keys used to be, plus the ones that need an
// argument, so that the panel can be driven from the one input line (like Claude Code).
const (
	cmdMap      = "map"
	cmdPipeline = "pipeline"
	cmdNew      = "new" // a fresh pipeline
	cmdAsk      = "ask"
	cmdNext     = "next"
	cmdCopy     = "copy"
	cmdHelp     = "help"
	cmdClear    = "clear"
	cmdExit     = "exit"
)

// command is a parsed slash command: its name and the rest of the line.
type command struct {
	name string
	args string
}

// parseCommand reads a line that starts with "/". It is a command only when a word follows the
// slash; "/" alone, or "/ foo", is not, so a path typed as a message is not mistaken for one.
func parseCommand(line string) (command, bool) {
	rest, ok := strings.CutPrefix(line, "/")
	if !ok {
		return command{}, false
	}
	name, args, _ := strings.Cut(rest, " ")
	if name == "" {
		return command{}, false
	}
	return command{name: strings.ToLower(name), args: strings.TrimSpace(args)}, true
}

// commandHelp lists the slash commands for the help block.
func commandHelp() []string {
	return []string{
		styleHi.Render("commands"),
		cmdLine("/ask <question>", "ask the assistant; or just type, with no slash"),
		cmdLine("/next", "ask for the next step"),
		cmdLine("/map", "show the map, or hide it"),
		cmdLine("/pipeline", "show the pipeline (/pipeline new for a fresh one)"),
		cmdLine("/copy", "copy the last suggested command"),
		cmdLine("/clear", "clear the stream"),
		cmdLine("/help", "this help"),
		cmdLine("/exit", "save the session and quit (or ctrl+c)"),
	}
}

func cmdLine(name, what string) string {
	return "  " + styleHi.Render(name) + strings.Repeat(" ", max(14-len(name), 1)) + styleMid.Render(what)
}
