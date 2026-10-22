package tui

import "time"

// A demo plays a short, made-up session so that the panel can be tried without a case.
type demoStep struct {
	after time.Duration
	ev    Event
}

func sys(glyph, text string) Event { return Event{Kind: KindSystem, Glyph: glyph, Text: text} }

func demoScript() []demoStep {
	return []demoStep{
		{500 * time.Millisecond, Event{Kind: KindCommand, Text: "nmap -sV -sC 10.10.11.42"}},
		{900 * time.Millisecond, Event{Kind: KindOutput, Text: "22/tcp open ssh · 80/tcp open http · 3306/tcp open mysql", Words: []string{"open"}}},
		{400 * time.Millisecond, sys(GlyphMap, "host 10.10.11.42 added, 3 services, from nmap")},
		{1500 * time.Millisecond, Event{Kind: KindCommand, Text: "gobuster dir -u http://10.10.11.42 -w common.txt"}},
		{1000 * time.Millisecond, Event{Kind: KindOutput, Text: "/admin (301) · /login (200) · /backup (200)", Words: []string{"/admin", "/login", "/backup"}}},
		{400 * time.Millisecond, sys(GlyphMap, ":80 gained 3 paths, from gobuster")},
		{1200 * time.Millisecond, Event{Kind: KindFinding, Text: "/backup/ lists its directory (medium)"}},
		{800 * time.Millisecond, Event{Kind: KindNote, Text: "check whether the backup holds a database dump"}},
	}
}

const (
	demoRationale = "/backup/ lists its directory and nginx 1.18.0 serves it on :80; fetching the listing shows what is in it."
	demoCommand   = "curl -s http://10.10.11.42/backup/"
	demoThinking  = 1400 * time.Millisecond
)
