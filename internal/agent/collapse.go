package agent

import (
	"fmt"
	"strings"
)

// A run of lines of one shape longer than maxRun is folded: the first foldKeep lines and the last
// one stay, with a line that says how many were left out.
const (
	maxRun   = 6
	foldKeep = 3
)

// collapse folds long runs of lines that differ only in their numbers, such as the thousand lines
// "[-] 10.0.0.5:1042/tcp closed" of a scanner that reports every port it tried. They fill the room
// the model has and bury the one line that is not like the others; the first and the last of the
// run say what the run was. A line that differs in any word is a line of another shape, so a list
// of different services or paths is not folded.
//
// What is lost is the numbers of the folded lines. If the folded lines were what mattered (a long
// list of open ports, all in one shape), the model does not see them; the output stays on disk.
func collapse(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); {
		j := i + 1
		shape := shapeOf(lines[i])
		for j < len(lines) && shape != "" && shapeOf(lines[j]) == shape {
			j++
		}
		n := j - i
		if n > maxRun {
			note := fmt.Sprintf("[... %d similar lines omitted ...]", n-foldKeep-1)
			if omitted := lines[i+foldKeep : j-1]; len(strings.Join(omitted, "\n")) > len(note) {
				out = append(out, lines[i:i+foldKeep]...)
				out = append(out, note)
				out = append(out, lines[j-1])
				i = j
				continue
			}
		}
		out = append(out, lines[i:j]...)
		i = j
	}
	return strings.Join(out, "\n")
}

// shapeOf is the line with every run of digits replaced by one '#'. An empty line has no shape, so
// blank lines are never folded together.
func shapeOf(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	var b strings.Builder
	inDigits := false
	for _, r := range line {
		if r >= '0' && r <= '9' {
			if !inDigits {
				b.WriteByte('#')
			}
			inDigits = true
			continue
		}
		inDigits = false
		b.WriteRune(r)
	}
	return b.String()
}
