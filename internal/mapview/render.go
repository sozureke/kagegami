package mapview

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Options choose how much the tree shows.
type Options struct {
	// All shows closed and filtered ports, every path and every note.
	All bool
}

const (
	maxPathsShown = 15
	maxNotesShown = 5
	maxNoteWidth  = 110
)

// JSON writes the whole map as JSON, indented. Secrets are masks.
func JSON(w io.Writer, v View) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Text writes the map as a tree. Everything that came from a target is cleaned of control and
// direction characters first: a banner must not be able to write to the person's terminal.
func Text(w io.Writer, v View, o Options) error {
	b := &builder{}
	if len(v.Hosts) == 0 && len(v.Findings) == 0 && len(v.Credentials) == 0 && len(v.Notes) == 0 {
		b.line("The map is empty.")
		return b.flush(w)
	}
	for _, h := range v.Hosts {
		b.line("%s  %s  %s  [%s]", clean(h.IP), dash(clean(h.Hostname)), dash(clean(h.OS)), clean(h.Status))
		var shown []Service
		hidden := 0
		for _, s := range h.Services {
			if !o.All && (s.State == "closed" || strings.HasSuffix(s.State, "|filtered") || s.State == "filtered") {
				hidden++
				continue
			}
			shown = append(shown, s)
		}
		for i, s := range shown {
			branch, pad := "├─", "│  "
			if i == len(shown)-1 && hidden == 0 {
				branch, pad = "└─", "   "
			}
			b.line("%s %-10s %-9s %-10s %s", branch, fmt.Sprintf("%d/%s", s.Port, clean(s.Proto)), clean(s.State), clean(s.Name), clean(s.Version))
			paths := s.Paths
			more := 0
			if !o.All && len(paths) > maxPathsShown {
				paths, more = paths[:maxPathsShown], len(paths)-maxPathsShown
			}
			for _, p := range paths {
				code := ""
				if p.StatusCode != nil {
					code = fmt.Sprint(*p.StatusCode)
				}
				b.line("%s   %-30s %-4s %s", pad, clean(p.Path), code, clean(p.Note))
			}
			if more > 0 {
				b.line("%s   ... %d more paths (--all shows them)", pad, more)
			}
		}
		if hidden > 0 {
			b.line("└─ %d closed or filtered ports hidden (--all shows them)", hidden)
		}
	}
	if len(v.Findings) > 0 {
		b.line("")
		b.line("Findings")
		for _, f := range v.Findings {
			b.line("  ! [%s] %s", clean(f.Severity), clean(f.Title))
		}
	}
	if len(v.Credentials) > 0 {
		b.line("")
		b.line("Credentials")
		for _, c := range v.Credentials {
			b.line("  %-16s %-6s %-9s %s", dash(clean(c.Username)), c.Secret.String(), clean(c.Kind), clean(c.WorksOn))
		}
	}
	if len(v.Notes) > 0 {
		b.line("")
		notes := v.Notes
		if !o.All && len(notes) > maxNotesShown {
			b.line("Notes (the last %d of %d, --all shows them)", maxNotesShown, len(notes))
			notes = notes[len(notes)-maxNotesShown:]
		} else {
			b.line("Notes")
		}
		for _, n := range notes {
			text := clean(n.Text)
			if !o.All {
				text = shorten(text, maxNoteWidth)
			}
			b.line("  · %s", text)
		}
	}
	return b.flush(w)
}

type builder struct{ sb strings.Builder }

func (b *builder) line(format string, a ...any) {
	fmt.Fprintf(&b.sb, strings.TrimRight(format, " ")+"\n", a...)
}

func (b *builder) flush(w io.Writer) error {
	_, err := io.WriteString(w, trimLines(b.sb.String()))
	return err
}

// trimLines removes the spaces that padding leaves at the ends of lines.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shorten(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	return string(r[:width-1]) + "…"
}

// clean removes what could act on a terminal or disguise text: control characters, the
// escape character, and the characters that change the direction of text.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == utf8.RuneError:
			return -1
		case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2069, r == 0xFEFF:
			return -1
		}
		return r
	}, s)
}
