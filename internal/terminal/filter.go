package terminal

import (
	"path"
	"strings"
)

// Reason says why a finished command is not worth a model's attention.
type Reason string

const (
	// ReasonNone means the command should be processed.
	ReasonNone Reason = ""
	// ReasonIgnored: every part of the command line is on the user's ignore list.
	ReasonIgnored Reason = "ignored command"
	// ReasonNoOutput: nothing was printed, so there is nothing to learn.
	ReasonNoOutput Reason = "no output"
	// ReasonNotFound: the shell could not run it (exit status 126 or 127), usually a typo.
	ReasonNotFound Reason = "command not found"
	// ReasonFullScreen: a full-screen program, whose "output" is screen redraws.
	ReasonFullScreen Reason = "full-screen program"
	// ReasonUnfinished: no end marker came, so neither the exit status nor the output can
	// be trusted.
	ReasonUnfinished Reason = "unfinished command"
)

// Filter decides which finished commands are passed on for reasoning. Trivial commands
// (cd, ls, clear) are not worth a model call. The policy is deliberately narrow: a command
// is dropped only when it cannot tell Kagegami anything. In particular a failure with a
// short message is kept, because "connection refused" is a finding.
type Filter struct {
	ignore [][]string // each entry as words
}

// NewFilter makes a filter from the ignore list of the configuration. An entry is a command
// name ("ls") or a command with leading arguments ("git status"). Blank entries are ignored.
func NewFilter(ignore []string) *Filter {
	f := &Filter{}
	for _, entry := range ignore {
		if words, ok := splitWords(strings.TrimSpace(entry)); ok && len(words) > 0 {
			f.ignore = append(f.ignore, words)
		}
	}
	return f
}

// Judge decides about a command that has finished. outputSize is the number of bytes of
// output that were recorded for it. The result is ReasonNone if it should be processed.
func (f *Filter) Judge(cmd string, end End, outputSize int64) Reason {
	switch {
	case end.Aborted:
		return ReasonUnfinished
	case end.AltScreen:
		return ReasonFullScreen
	case end.ExitCode == 126 || end.ExitCode == 127:
		return ReasonNotFound
	case f.allIgnored(cmd):
		return ReasonIgnored
	case outputSize == 0:
		return ReasonNoOutput
	}
	return ReasonNone
}

// allIgnored reports whether every simple command of the line is on the list. A line that
// the filter cannot read with certainty (substitutions, subshells, groups) is never ignored.
func (f *Filter) allIgnored(line string) bool {
	if len(f.ignore) == 0 {
		return false
	}
	segments, ok := splitSegments(line)
	if !ok {
		return false
	}
	seen := false
	for _, seg := range segments {
		words, ok := splitWords(seg)
		if !ok {
			return false
		}
		words = commandWords(words)
		if len(words) == 0 {
			continue // an empty part, as in "ls;"
		}
		if !f.matches(words) {
			return false
		}
		seen = true
	}
	return seen
}

func (f *Filter) matches(words []string) bool {
	for _, entry := range f.ignore {
		if len(words) >= len(entry) && equalWords(words[:len(entry)], entry) {
			return true
		}
	}
	return false
}

func equalWords(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// commandWords drops what comes before the command itself: variable assignments and the
// wrappers sudo, time, command and builtin (without options, which could change what runs),
// and reduces the command to its base name, so /bin/ls counts as ls. (A leading backslash,
// as in \ls, is already gone: the word splitter treats it as an escape.)
func commandWords(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if isAssignment(w) {
			words = words[1:]
			continue
		}
		switch w {
		case "sudo", "time", "command", "builtin":
			if len(words) > 1 && !strings.HasPrefix(words[1], "-") {
				words = words[1:]
				continue
			}
			return []string{w + " " + strings.Join(words[1:], " ")} // cannot be a plain command
		}
		break
	}
	if len(words) == 0 {
		return nil
	}
	first := words[0]
	if strings.Contains(first, "/") {
		first = path.Base(first)
	}
	return append([]string{first}, words[1:]...)
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range w[:eq] {
		letter := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := i > 0 && r >= '0' && r <= '9'
		if !letter && !digit {
			return false
		}
	}
	return true
}

// splitSegments cuts a command line at ; && || | & and newlines that are not quoted. It
// reports false for text it does not understand: command substitution, process
// substitution, subshells and groups, and unbalanced quotes.
func splitSegments(line string) (segments []string, ok bool) {
	var cur strings.Builder
	flush := func() {
		segments = append(segments, cur.String())
		cur.Reset()
	}
	quote := rune(0)
	escaped := false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case escaped:
			escaped = false
			cur.WriteRune(r)
		case quote == '\'':
			if r == '\'' {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\\':
			escaped = true
			cur.WriteRune(r)
		case quote == '"':
			if r == '"' {
				quote = 0
			}
			if r == '$' || r == '`' {
				return nil, false
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == '$' || r == '`' || r == '(' || r == ')' || r == '{' || r == '}':
			return nil, false
		case r == '&' && (i > 0 && runes[i-1] == '>' || i+1 < len(runes) && runes[i+1] == '>'):
			cur.WriteRune(r) // part of a redirection: 2>&1, &>file
		case r == ';' || r == '\n' || r == '|' || r == '&':
			// || and && are two operators in a row; each flush makes an empty segment, which
			// is skipped later
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 || escaped {
		return nil, false
	}
	flush()
	return segments, true
}

// splitWords splits one simple command into words the way a shell does for plain text:
// whitespace separates, quotes and backslashes group. Redirections stay words of their own
// and are of no interest. It reports false for unbalanced quotes.
func splitWords(seg string) (words []string, ok bool) {
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	runes := []rune(seg)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes) && strings.ContainsRune("$`\"\\", runes[i+1]):
				i++ // inside double quotes a backslash only escapes these
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			if i+1 == len(runes) {
				return nil, false
			}
			i++
			cur.WriteRune(runes[i])
			inWord = true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}

// wrappers run another program given on the same line. Those that take options with a value
// are listed with the options that do.
var wrappers = map[string][]string{
	"sudo": {"-u", "-g", "-h", "-p", "-C", "-T", "-r", "-t", "-U", "-D", "-R"}, "doas": {"-u", "-C"},
	"time": nil, "command": nil, "builtin": nil, "env": {"-u", "-C", "-S"}, "nohup": nil, "nice": {"-n"},
	"proxychains": {"-f"}, "proxychains4": {"-f"}, "torsocks": nil, "stdbuf": nil, "timeout": nil,
}

// CommandNames lists the programs that a command line runs, by base name and in order: for
// "cd /tmp && sudo -u kali /usr/bin/nmap -sV x | tee out" that is cd, nmap, tee. It is meant
// for choosing a parser, so it is lenient: a line that the strict reader refuses (command
// substitution and the like) is cut at separators and read as well as it can be.
func CommandNames(line string) []string {
	segments, ok := splitSegments(line)
	if !ok {
		segments = strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(";|&\n(){}`", r) })
	}
	var names []string
	for _, seg := range segments {
		words, ok := splitWords(seg)
		if !ok {
			words = strings.Fields(seg)
		}
		if name := programName(words); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// programName finds the program in the words of one simple command.
func programName(words []string) string {
	rest := programWords(words)
	if len(rest) == 0 {
		return ""
	}
	return path.Base(strings.TrimPrefix(rest[0], `\`))
}

// programWords returns the words of one simple command from the program on, after variable
// assignments and wrappers (with their options).
func programWords(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if isAssignment(w) {
			words = words[1:]
			continue
		}
		valued, isWrapper := wrappers[path.Base(w)]
		if !isWrapper {
			break
		}
		words = words[1:]
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			opt := words[0]
			words = words[1:]
			if slicesContains(valued, opt) && len(words) > 0 {
				words = words[1:]
			}
		}
	}
	return words
}

// CommandArgs returns the arguments of the first command on the line that runs program, or
// nil if none does. It reads the line as CommandNames does.
func CommandArgs(line, program string) []string {
	segments, ok := splitSegments(line)
	if !ok {
		segments = strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(";|&\n(){}`", r) })
	}
	for _, seg := range segments {
		words, ok := splitWords(seg)
		if !ok {
			words = strings.Fields(seg)
		}
		if rest := programWords(words); len(rest) > 0 && path.Base(strings.TrimPrefix(rest[0], `\`)) == program {
			return rest[1:]
		}
	}
	return nil
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
