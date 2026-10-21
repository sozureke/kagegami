// Package gobuster reads the terminal output of gobuster in directory mode.
package gobuster

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/terminal"
)

// Parser reads what gobuster dir prints: the URL from the banner (or from -u) and one line per hit.
type Parser struct{}

// New returns the parser.
func New() *Parser { return &Parser{} }

// Name implements recon.Parser.
func (*Parser) Name() string { return "gobuster" }

// Match implements recon.Parser.
func (*Parser) Match(cmd recon.Command) bool { return recon.Runs(cmd.Line, "gobuster") }

const maxEntries = recon.MaxEntries / 2

var (
	urlRE    = regexp.MustCompile(`^\[\+\] Url:\s+(\S+)\s*$`)
	modeRE   = regexp.MustCompile(`^Starting gobuster in (.+) mode$`)
	resultRE = regexp.MustCompile(`^(\S+)\s+\(Status: (\d{3})\)(?: \[Size: (\d+)\])?(?: \[--> ([^\]]*)\])?$`)
)

// Parse implements recon.Parser.
func (*Parser) Parse(ctx context.Context, cmd recon.Command, output string) (recon.StateDelta, error) {
	args := terminal.CommandArgs(cmd.Line, "gobuster")
	raw := recon.FlagValue(args, "-u", "--url")
	mode := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
	}
	lines := strings.Split(output, "\n")
	for _, l := range lines {
		if m := urlRE.FindStringSubmatch(l); m != nil {
			raw = m[1]
		}
		if m := modeRE.FindStringSubmatch(l); m != nil {
			mode = map[string]string{"directory enumeration": "dir"}[m[1]]
			if mode == "" {
				mode = m[1]
			}
		}
	}
	if mode != "dir" {
		return recon.StateDelta{}, nil // DNS, vhost and the other modes print other things
	}
	target, ok := recon.ParseWebURL(raw)
	if !ok {
		return recon.StateDelta{}, nil
	}

	var d recon.StateDelta
	seen := map[string]bool{}
	for i, line := range lines {
		if i%1024 == 0 && ctx.Err() != nil {
			return d, ctx.Err()
		}
		m := resultRE.FindStringSubmatch(strings.TrimRight(line, " \t"))
		if m == nil {
			continue
		}
		if len(d.WebPaths) >= maxEntries {
			return d, fmt.Errorf("too many results, the rest of the output is ignored")
		}
		path := "/" + strings.TrimPrefix(m[1], "/")
		if seen[path] {
			continue
		}
		seen[path] = true
		code, _ := strconv.Atoi(m[2])
		note := ""
		if m[4] != "" {
			note = "redirects to " + m[4]
		}
		d.WebPaths = append(d.WebPaths, target.PathDelta(path, &code, note))
	}
	return d, nil
}
