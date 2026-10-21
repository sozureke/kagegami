// Package whatweb reads the terminal output of whatweb.
package whatweb

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
)

// Parser reads whatweb's one-line report per URL: the web server and what it runs.
type Parser struct{}

// New returns the parser.
func New() *Parser { return &Parser{} }

// Name implements recon.Parser.
func (*Parser) Name() string { return "whatweb" }

// Match implements recon.Parser.
func (*Parser) Match(cmd recon.Command) bool { return recon.Runs(cmd.Line, "whatweb") }

const (
	maxEntries = recon.MaxEntries / 4
	maxNote    = 3000
)

var lineRE = regexp.MustCompile(`^(https?://\S+) \[(\d{3})(?: [^\]]*)?\](?: (.*))?$`)

// Parse implements recon.Parser.
func (*Parser) Parse(ctx context.Context, _ recon.Command, output string) (recon.StateDelta, error) {
	var d recon.StateDelta
	seen := map[string]bool{}
	for i, line := range strings.Split(output, "\n") {
		if i%1024 == 0 && ctx.Err() != nil {
			return d, ctx.Err()
		}
		m := lineRE.FindStringSubmatch(strings.TrimRight(line, " \t"))
		if m == nil {
			continue
		}
		if len(d.Services)+len(d.Notes) >= maxEntries {
			return d, fmt.Errorf("too many results, the rest of the output is ignored")
		}
		target, ok := recon.ParseWebURL(m[1])
		if !ok {
			continue
		}
		plugins := splitPlugins(m[3])
		code, _ := strconv.Atoi(m[2])

		ip := target.IP
		if ip == "" {
			ip = single(plugins, "IP") // the report names the address the name resolved to
		}
		text := summary(m[1], m[2], plugins)
		if ip == "" {
			d.Notes = append(d.Notes, recon.NoteDelta{Text: text})
			continue
		}
		name := "http"
		if target.TLS {
			name = "https"
		}
		if key := fmt.Sprint(ip, target.Host, target.Port); !seen[key] {
			seen[key] = true
			d.Hosts = append(d.Hosts, recon.HostDelta{IP: ip, Hostname: target.Host})
			d.Services = append(d.Services, recon.ServiceDelta{
				HostIP: ip, Port: target.Port, Proto: "tcp", Name: name, State: "open", Version: single(plugins, "HTTPServer"),
			})
		}
		if target.Path != "" && target.Path != "/" {
			d.WebPaths = append(d.WebPaths, recon.WebPathDelta{HostIP: ip, Port: target.Port, Proto: "tcp", Path: target.Path, StatusCode: &code})
		}
		d.Notes = append(d.Notes, recon.NoteDelta{Text: text})
	}
	return d, nil
}

type plugin struct{ name, value string }

// splitPlugins splits "A[1], B[x, y], C" at the commas outside brackets.
func splitPlugins(s string) []plugin {
	var out []plugin
	depth, start := 0, 0
	flush := func(end int) {
		item := strings.TrimSpace(s[start:end])
		if item == "" {
			return
		}
		name, value := item, ""
		if i := strings.IndexByte(item, '['); i > 0 && strings.HasSuffix(item, "]") {
			name, value = item[:i], item[i+1:len(item)-1]
		}
		out = append(out, plugin{name, value})
	}
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return out
}

// single returns the value of the first plugin with the name. A value that whatweb prints in
// several brackets, as HTTPServer[Ubuntu Linux][Apache/2.4.41], is the last one for HTTPServer
// (the server) and the first for the rest.
func single(list []plugin, name string) string {
	for _, p := range list {
		if p.name != name {
			continue
		}
		parts := strings.Split(p.value, "][")
		if name == "HTTPServer" {
			return strings.TrimSpace(parts[len(parts)-1])
		}
		return strings.TrimSpace(parts[0])
	}
	return ""
}

func summary(url, status string, plugins []plugin) string {
	var parts []string
	for _, p := range plugins {
		switch p.name {
		case "Country", "IP":
			continue
		}
		if p.value == "" {
			parts = append(parts, p.name)
		} else {
			parts = append(parts, p.name+"["+p.value+"]")
		}
	}
	text := fmt.Sprintf("whatweb %s [%s]: %s", url, status, strings.Join(parts, ", "))
	if len(text) > maxNote {
		text = text[:maxNote] + "..."
	}
	return text
}
