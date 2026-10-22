package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Config configures the panel.
type Config struct {
	Version string
	// Demo plays a made-up session, so that the panel can be tried without a case.
	Demo bool
	// NoAnim skips the opening animation.
	NoAnim bool
	// Now returns the time of an entry; nil means the clock.
	Now func() time.Time
}

type phase int

const (
	phaseSplash phase = iota
	phaseStream
)

// Messages. Each timer is a single tick that asks for the next one only while it is needed.
type (
	splashMsg struct{}
	spinMsg   struct{}
	typeMsg   struct{}
	demoMsg   struct{ index int }
	answerMsg struct{ turn *Turn }
)

// Model is the panel: the opening animation, then the stream.
type Model struct {
	cfg    Config
	phase  phase
	frame  int
	width  int
	height int

	events []Event
	spin   int

	spinning bool
	typing   bool
}

// New returns the panel.
func New(cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Model{cfg: cfg, width: 80, height: 24}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	if m.cfg.NoAnim {
		return m.enterStream()
	}
	return tea.Tick(splashTick, func(time.Time) tea.Msg { return splashMsg{} })
}

func (m *Model) enterStream() tea.Cmd {
	m.phase = phaseStream
	if !m.cfg.Demo {
		return nil
	}
	return m.demoAfter(0)
}

func (m *Model) demoAfter(i int) tea.Cmd {
	script := demoScript()
	if i >= len(script) {
		return nil
	}
	return tea.Tick(script[i].after, func(time.Time) tea.Msg { return demoMsg{index: i} })
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg.String())
	case splashMsg:
		if m.phase != phaseSplash {
			return m, nil
		}
		m.frame++
		if m.frame >= splashFrames || m.width < splashWidth()+2 {
			return m, m.enterStream()
		}
		return m, tea.Tick(splashTick, func(time.Time) tea.Msg { return splashMsg{} })
	case demoMsg:
		ev := demoScript()[msg.index].ev
		ev.Time = m.cfg.Now()
		m.events = append(m.events, ev)
		return m, m.demoAfter(msg.index + 1)
	case spinMsg:
		return m.spinTick()
	case typeMsg:
		return m.typeTick()
	case answerMsg:
		msg.turn.Answer(demoRationale, demoCommand)
		return m, m.startTyping()
	}
	return m, nil
}

func (m *Model) key(s string) (tea.Model, tea.Cmd) {
	if s == "ctrl+c" || s == "q" {
		return m, tea.Quit
	}
	if m.phase == phaseSplash {
		return m, m.enterStream() // any other key skips the animation
	}
	switch s {
	case "?":
		return m, m.toggle(blockHelp, func(int) []string { return helpLines() })
	case "m":
		if !m.cfg.Demo {
			return m, m.system("the map block needs a case; it is not connected yet")
		}
		return m, m.toggle(blockMap, func(int) []string { return demoMapLines() })
	case "p":
		if !m.cfg.Demo {
			return m, m.system("the pipeline block needs a case; it is not connected yet")
		}
		return m, m.toggle(blockPipeline, demoPipelineLines)
	case "n":
		return m, m.ask()
	}
	return m, nil
}

func (m *Model) system(text string) tea.Cmd {
	m.events = append(m.events, Event{Kind: KindSystem, Time: m.cfg.Now(), Glyph: GlyphPipeline, Text: text})
	return nil
}

// toggle opens a block at the end of the stream, or closes it if it is open.
func (m *Model) toggle(name string, build func(spin int) []string) tea.Cmd {
	for i, e := range m.events {
		if e.Kind == KindBlock && e.Block == name {
			m.events = append(m.events[:i], m.events[i+1:]...)
			return nil
		}
	}
	m.events = append(m.events, Event{Kind: KindBlock, Block: name, Lines: build(m.spin), build: build})
	if name == blockPipeline {
		return m.startSpinning()
	}
	return nil
}

func (m *Model) ask() tea.Cmd {
	if !m.cfg.Demo {
		return m.system("no model is connected to the panel yet")
	}
	t := NewTurn()
	m.events = append(m.events, Event{Kind: KindAssistant, Time: m.cfg.Now(), Turn: t})
	return tea.Batch(m.startSpinning(), tea.Tick(demoThinking, func(time.Time) tea.Msg { return answerMsg{turn: t} }))
}

func (m *Model) startSpinning() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return tea.Tick(spinTick, func(time.Time) tea.Msg { return spinMsg{} })
}

func (m *Model) startTyping() tea.Cmd {
	if m.typing {
		return nil
	}
	m.typing = true
	return tea.Tick(typeTick, func(time.Time) tea.Msg { return typeMsg{} })
}

// spinTick moves every spinner on, and stops the timer when none is left on screen.
func (m *Model) spinTick() (tea.Model, tea.Cmd) {
	m.spin++
	live := false
	for i := range m.events {
		e := &m.events[i]
		if e.Kind == KindAssistant && e.Turn != nil && e.Turn.State == turnThinking {
			e.Turn.Spun()
			live = true
		}
		if e.Kind == KindBlock && e.Block == blockPipeline && e.build != nil {
			e.Lines = e.build(m.spin)
			live = true
		}
	}
	if !live {
		m.spinning = false
		return m, nil
	}
	return m, tea.Tick(spinTick, func(time.Time) tea.Msg { return spinMsg{} })
}

// typeTick types one more character of every turn that is being typed.
func (m *Model) typeTick() (tea.Model, tea.Cmd) {
	live := false
	for i := range m.events {
		if t := m.events[i].Turn; t != nil && (t.State == turnRationale || t.State == turnCommand) {
			t.Typed()
			live = live || t.State == turnRationale || t.State == turnCommand
		}
	}
	if !live {
		m.typing = false
		return m, nil
	}
	return m, tea.Tick(typeTick, func(time.Time) tea.Msg { return typeMsg{} })
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var content string
	if m.phase == phaseSplash {
		content = splashView(m.frame, m.cfg.Version, m.width, m.height)
	} else {
		content = m.stream()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "kagegami"
	return v
}

// stream lays out the entries one under the other and keeps the last lines that fit.
func (m *Model) stream() string {
	var lines []string
	for _, e := range m.events {
		lines = append(lines, renderEvent(e, m.width)...)
	}
	if len(lines) > m.height {
		lines = lines[len(lines)-m.height:]
	}
	return strings.Join(lines, "\n")
}
