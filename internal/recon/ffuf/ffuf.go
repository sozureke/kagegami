// Package ffuf reads the terminal output of ffuf.
package ffuf

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/terminal"
)

// Parser reads what ffuf prints: the URL from its banner (or from -u), and one line per hit.
type Parser struct{}

// New returns the parser.
func New() *Parser { return &Parser{} }

// Name implements recon.Parser.
func (*Parser) Name() string { return "ffuf" }

// Match implements recon.Parser.
func (*Parser) Match(cmd recon.Command) bool { return recon.Runs(cmd.Line, "ffuf") }

const (
	maxEntries = recon.MaxEntries / 2
	keyword    = "FUZZ"
)

var (
	urlRE    = regexp.MustCompile(`^\s*:: URL\s+: (\S+)\s*$`)
	resultRE = regexp.MustCompile(`^(.*?)\s*\[Status: (\d{3}), Size: (\d+), Words: (\d+), Lines: (\d+)(?:, Duration: [^\]]*)?\]$`)
	wordRE   = regexp.MustCompile(`^[^\s:]\S*$`)
)

// Parse implements recon.Parser.
func (*Parser) Parse(ctx context.Context, cmd recon.Command, output string) (recon.StateDelta, error) {
	args := terminal.CommandArgs(cmd.Line, "ffuf")
	raw := recon.FlagValue(args, "-u", "--u", "-url", "--url")
	lines := strings.Split(output, "\n")
	for _, l := range lines {
		if m := urlRE.FindStringSubmatch(l); m != nil {
			raw = m[1]
			break
		}
	}
	target, ok := recon.ParseWebURL(strings.ReplaceAll(raw, keyword, "x"))
	if !ok {
		return recon.StateDelta{}, nil // without the URL there is no host to put the hits on
	}
	pathFuzz := strings.Contains(pathOf(raw), keyword)
	silent := recon.HasFlag(args, "-s", "--s")

	var d recon.StateDelta
	var vhosts []string
	seen := map[string]bool{}
	for i, line := range lines {
		if i%1024 == 0 && ctx.Err() != nil {
			return d, ctx.Err()
		}
		line = strings.TrimRight(line, " \t")
		var name string
		var status *int
		if m := resultRE.FindStringSubmatch(line); m != nil {
			name = strings.TrimSpace(m[1])
			code, _ := strconv.Atoi(m[2])
			status = &code
		} else if silent && wordRE.MatchString(strings.TrimSpace(line)) {
			name = strings.TrimSpace(line)
		} else {
			continue
		}
		if len(d.WebPaths)+len(vhosts) >= maxEntries {
			return d, fmt.Errorf("too many results, the rest of the output is ignored")
		}
		if !pathFuzz {
			vhosts = append(vhosts, describe(name, status))
			continue
		}
		path := strings.Replace(pathOf(raw), keyword, name, 1)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		d.WebPaths = append(d.WebPaths, target.PathDelta(path, status, ""))
	}
	if len(vhosts) > 0 {
		text := fmt.Sprintf("ffuf (not a path scan) on %s:%d answered for: %s", hostOf(target), target.Port, strings.Join(vhosts, ", "))
		if len(text) > 4000 {
			text = text[:4000] + "..."
		}
		d.Notes = append(d.Notes, recon.NoteDelta{Text: text})
	}
	return d, nil
}

func describe(name string, status *int) string {
	if status == nil {
		return name
	}
	return fmt.Sprintf("%s (%d)", name, *status)
}

func hostOf(t recon.WebTarget) string {
	if t.IP != "" {
		return t.IP
	}
	return t.Host
}

// pathOf returns the path part of a URL that may contain the keyword anywhere.
func pathOf(raw string) string {
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return ""
	}
	path := rest[i:]
	if j := strings.IndexAny(path, "?#"); j >= 0 {
		path = path[:j]
	}
	return path
}
