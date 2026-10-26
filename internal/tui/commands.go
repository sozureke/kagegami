package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

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
	cmdTarget   = "target"
	cmdGoal     = "goal"
	cmdScope    = "scope"
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

// commandInfo describes a slash command for the help block and for the list that opens when the
// tester types a slash.
type commandInfo struct {
	name  string
	usage string // as typed, with its argument
	what  string
	// args says the command needs something after its name, so completing it leaves the cursor there
	// instead of running it.
	args bool
}

// commandList is every slash command, in the order they are listed.
var commandList = []commandInfo{
	{cmdAsk, "/ask <question>", "ask the assistant; or just type, with no slash", true},
	{cmdTarget, "/target <address>", "set the target (and the scope, if there is none)", true},
	{cmdGoal, "/goal <text>", "set what you are after", true},
	{cmdScope, "/scope [add] <list>", "show the scope, replace it, or add to it", false},
	{cmdNext, "/next", "ask for the next step", false},
	{cmdMap, "/map", "show the map, or hide it", false},
	{cmdPipeline, "/pipeline", "show the pipeline (/pipeline new for a fresh one)", false},
	{cmdCopy, "/copy", "copy the last suggested command", false},
	{cmdClear, "/clear", "clear the stream", false},
	{cmdHelp, "/help", "this help", false},
	{cmdExit, "/exit", "save the session and quit (or ctrl+c)", false},
}

// matchCommands lists the commands the line could become: while it is a slash and the start of a
// name, with no space yet. Once a space is typed the tester is on the arguments and nothing is offered.
func matchCommands(line string) []commandInfo {
	rest, ok := strings.CutPrefix(line, "/")
	if !ok || strings.Contains(rest, " ") {
		return nil
	}
	rest = strings.ToLower(rest)
	var out []commandInfo
	for _, c := range commandList {
		if strings.HasPrefix(c.name, rest) {
			out = append(out, c)
		}
	}
	return out
}

// commandHelp lists the slash commands for the help block.
func commandHelp() []string {
	lines := []string{styleHi.Render("commands")}
	for _, c := range commandList {
		lines = append(lines, cmdLine(c.usage, c.what))
	}
	return lines
}

func cmdLine(name, what string) string {
	return "  " + styleHi.Render(name) + strings.Repeat(" ", max(21-len(name), 1)) + styleMid.Render(what)
}

// paletteRows draws the commands on offer, one per row, with the chosen one marked.
func paletteRows(cmds []commandInfo, sel, width int) []string {
	rows := make([]string, 0, len(cmds))
	for i, c := range cmds {
		pad := strings.Repeat(" ", max(21-len(c.usage), 1))
		var row string
		if i == sel {
			row = stylePrompt.Render("▸ ") + styleHi.Render(c.usage) + pad + styleMid.Render(c.what)
		} else {
			row = "  " + styleMid.Render(c.usage) + pad + styleLo.Render(c.what)
		}
		rows = append(rows, ansi.Truncate(row, width, "…"))
	}
	return rows
}
