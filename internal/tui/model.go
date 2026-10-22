package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Config configures the panel.
type Config struct {
	Version string
	// Now returns the time of an entry; nil means the clock.
	Now func() time.Time
}

// Model is the panel: the stream, and the blocks its keys open.
type Model struct {
	cfg    Config
	width  int
	height int
	events []Event
}

// New returns the panel.
func New(cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Model{cfg: cfg, width: 80, height: 24}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m *Model) key(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.toggle(blockHelp, helpLines())
	case "m":
		m.system("the map block needs a case; the panel is not connected to one yet")
	case "p":
		m.system("the pipeline block needs a case; the panel is not connected to one yet")
	case "n":
		m.system("no model is connected to the panel yet")
	}
	return m, nil
}

func (m *Model) system(text string) {
	m.events = append(m.events, Event{Kind: KindSystem, Time: m.cfg.Now(), Glyph: GlyphPipeline, Text: text})
}

// toggle opens a block at the end of the stream, or closes it if it is open.
func (m *Model) toggle(name string, lines []string) {
	for i, e := range m.events {
		if e.Kind == KindBlock && e.Block == name {
			m.events = append(m.events[:i], m.events[i+1:]...)
			return
		}
	}
	m.events = append(m.events, Event{Kind: KindBlock, Block: name, Lines: lines})
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.stream())
	v.AltScreen = true
	v.WindowTitle = "kagegami"
	return v
}

// stream lays out the entries one under the other and keeps the last lines that fit.
func (m *Model) stream() string {
	if len(m.events) == 0 {
		return styleDim.Render("press ? for the keys")
	}
	var lines []string
	for _, e := range m.events {
		lines = append(lines, renderEvent(e, m.width)...)
	}
	if len(lines) > m.height {
		lines = lines[len(lines)-m.height:]
	}
	return strings.Join(lines, "\n")
}
