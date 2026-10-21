// Package nmap reads the normal text output of nmap, the one printed to the terminal.
package nmap

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
)

// Parser reads what nmap prints: hosts, ports with their service and version, the OS guess,
// and the output of scripts. A person runs nmap without -oX, so the text on the screen is what
// there is; its layout has been stable for many versions.
type Parser struct{}

// New returns the parser.
func New() *Parser { return &Parser{} }

// Name implements recon.Parser.
func (*Parser) Name() string { return "nmap" }

// Match implements recon.Parser.
func (*Parser) Match(cmd recon.Command) bool { return recon.Runs(cmd.Line, "nmap") }

const (
	// maxNote bounds the text of one script's output kept as a note.
	maxNote = 4000
	// maxEntries stops a parser that is fed an endless output.
	maxEntries = recon.MaxEntries / 2
)

var (
	reportRE  = regexp.MustCompile(`^Nmap scan report for (?:(\S+) \(([0-9A-Fa-f:.]+)\)|([0-9A-Fa-f:.]+))( \[host down\])?$`)
	portRE    = regexp.MustCompile(`^(\d{1,5})/(tcp|udp|sctp)\s+(\S+)\s+(\S+)(?:\s+(.*))?$`)
	osRE      = regexp.MustCompile(`\bOS: ([^;]+)`)
	scriptRE  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*):(?: (.*))?$`)
	pathRE    = regexp.MustCompile(`^(/\S*): .+$`)
	uncertain = regexp.MustCompile(`\?$`)
)

// Parse implements recon.Parser. Lines it does not understand are skipped.
func (*Parser) Parse(ctx context.Context, _ recon.Command, output string) (recon.StateDelta, error) {
	s := &state{}
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if i%1024 == 0 && ctx.Err() != nil {
			return s.delta, ctx.Err()
		}
		s.line(strings.TrimRight(line, " \t\r"))
		if s.full() {
			s.flush()
			return s.delta, fmt.Errorf("too many entries, the rest of the output is ignored")
		}
	}
	s.flush()
	return s.delta, nil
}

// state is the parser's position in the output.
type state struct {
	delta recon.StateDelta

	host     string // address of the current host, "" before the first report
	inPorts  bool   // inside the PORT table
	hostOnly bool   // inside "Host script results"

	port  *portKey // the port the script lines belong to
	name  string   // the script being read
	out   []string // its lines
	paths bool     // whether the script is http-enum
}

type portKey struct {
	num   int
	proto string
}

func (s *state) full() bool {
	d := s.delta
	return len(d.Hosts)+len(d.Services)+len(d.WebPaths)+len(d.Notes) >= maxEntries
}

func (s *state) line(line string) {
	switch {
	case reportRE.MatchString(line):
		s.flush()
		s.report(reportRE.FindStringSubmatch(line))
		return
	case strings.HasPrefix(line, "|"):
		s.script(line)
		return
	}
	s.flush()
	switch {
	case line == "":
		s.inPorts = false
	case strings.HasPrefix(line, "PORT ") || strings.HasPrefix(line, "PORT\t"):
		s.inPorts, s.hostOnly = s.host != "", false
	case strings.HasPrefix(line, "Host script results:"):
		s.inPorts, s.hostOnly, s.port = false, true, nil
	case s.inPorts && portRE.MatchString(line):
		s.servicePort(portRE.FindStringSubmatch(line))
	case strings.HasPrefix(line, "Service Info:"):
		if m := osRE.FindStringSubmatch(line); m != nil {
			s.setOS(strings.TrimSpace(m[1]))
		}
	case strings.HasPrefix(line, "OS details:"):
		s.setOS(strings.TrimSpace(strings.TrimPrefix(line, "OS details:")))
	case strings.HasPrefix(line, "Running:") && s.host != "":
		s.setOSIfEmpty(strings.TrimSpace(strings.TrimPrefix(line, "Running:")))
	case strings.HasPrefix(line, "Host is up"):
		s.setStatus("up")
	case strings.HasPrefix(line, "Host is down"), strings.HasPrefix(line, "Note: Host seems down"):
		s.setStatus("down")
	}
}

func (s *state) report(m []string) {
	hostname, ip := m[1], m[2]
	if ip == "" {
		ip = m[3]
	}
	s.host, s.inPorts, s.hostOnly, s.port = ip, false, false, nil
	h := recon.HostDelta{IP: ip, Hostname: hostname}
	if m[4] != "" {
		h.Status = "down"
	}
	s.delta.Hosts = append(s.delta.Hosts, h)
}

func (s *state) lastHost() *recon.HostDelta {
	if s.host == "" || len(s.delta.Hosts) == 0 {
		return nil
	}
	return &s.delta.Hosts[len(s.delta.Hosts)-1]
}

func (s *state) setStatus(status string) {
	if h := s.lastHost(); h != nil {
		h.Status = status
	}
}

func (s *state) setOS(os string) {
	if h := s.lastHost(); h != nil && os != "" {
		h.OS = os
	}
}

func (s *state) setOSIfEmpty(os string) {
	if h := s.lastHost(); h != nil && h.OS == "" {
		h.OS = os
	}
}

func (s *state) servicePort(m []string) {
	num, _ := strconv.Atoi(m[1])
	name := uncertain.ReplaceAllString(m[4], "")
	s.delta.Services = append(s.delta.Services, recon.ServiceDelta{
		HostIP: s.host, Port: num, Proto: m[2], State: m[3], Name: name, Version: strings.TrimSpace(m[5]),
	})
	s.port = &portKey{num, m[2]}
}

// script reads a line of script output. A script starts with "| name:" or "|_name: value"
// (the name right after the bar); its other lines are indented, and the one that starts with
// "|_" is the last.
func (s *state) script(line string) {
	if s.host == "" || len(line) < 2 {
		return
	}
	last := line[1] == '_'
	body := line[2:]
	if m := scriptRE.FindStringSubmatch(body); m != nil {
		s.flush()
		s.name, s.paths = m[1], m[1] == "http-enum"
		if m[2] != "" {
			s.out = append(s.out, strings.TrimSpace(m[2]))
		}
	} else if s.name != "" {
		if text := strings.TrimSpace(body); text != "" {
			s.out = append(s.out, text)
		}
	}
	if last {
		s.flush()
	}
}

// flush ends the script being read and records it.
func (s *state) flush() {
	name, out := s.name, s.out
	s.name, s.out = "", nil
	if name == "" || len(out) == 0 || name == "fingerprint-strings" {
		return
	}
	text := strings.Join(out, "; ")
	if strings.Contains(text, "ERROR: Script execution failed") {
		return
	}
	if s.paths && s.port != nil {
		found := false
		for _, l := range out {
			if m := pathRE.FindStringSubmatch(l); m != nil {
				found = true
				s.delta.WebPaths = append(s.delta.WebPaths, recon.WebPathDelta{
					HostIP: s.host, Port: s.port.num, Proto: s.port.proto, Path: m[1], Note: strings.TrimPrefix(l, m[1]+": "),
				})
			}
		}
		if found {
			return // the paths say it all; a note would only repeat them
		}
	}
	where := s.host
	if s.port != nil && !s.hostOnly {
		where = fmt.Sprintf("%s %d/%s", s.host, s.port.num, s.port.proto)
	}
	if len(text) > maxNote {
		text = text[:maxNote] + "..."
	}
	s.delta.Notes = append(s.delta.Notes, recon.NoteDelta{Text: fmt.Sprintf("nmap %s %s: %s", name, where, text)})
}
