package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Config configures the panel.
type Config struct {
	Version string
	// Live says the panel is watching a shell, so the empty-stream hint invites a command instead
	// of saying nothing is connected.
	Live bool
	// Backend provides what the keys n, m and p show; nil means they say that nothing is connected.
	Backend Backend
	// Auto makes the agent look at every command that has been read as soon as it is: it asks the
	// model what the output shows and what to do next, and puts the answer in the stream. false
	// leaves that to the key n.
	Auto bool
	// AutoDelay is how long the agent waits after a command for another one to follow, so that a
	// burst of commands is one question and not several; zero means 1 second.
	AutoDelay time.Duration
	// Copy puts a command on the clipboard of the system; nil leaves it to the terminal (OSC 52).
	Copy func(text string) error
	// Context ends the calls to the backend when the panel is closed; nil means they are not ended.
	Context context.Context
	// Notice is a line the stream starts with, such as a warning that matters for the whole session.
	Notice string
	// SessionName is what the session is called, shown on exit so it can be resumed; empty hides it.
	SessionName string
	// Greeting is shown under the notice when the panel starts, to tell the tester what to do first.
	Greeting string
	// Now returns the time of an entry; nil means the clock.
	Now func() time.Time
}

// AppendMsg adds entries to the stream. The panel sends it as commands are captured and as the map
// is updated. Send it to a running program with (*tea.Program).Send.
type AppendMsg struct{ Events []Event }

// MapChangedMsg says the map has changed, so an open map block shows it again.
type MapChangedMsg struct{}

// CommandDoneMsg says a command has been read and its output is in the case: the agent looks at it.
type CommandDoneMsg struct{}

// FocusMsg says whether the pane the panel runs in has the keyboard. The panel sends it as the focus
// moves between panes, because tmux tells a program about that only when it is set up to.
type FocusMsg struct{ Focused bool }

// EndedMsg says the watch is over; the panel sends it so the stream shows why and the program can be
// left for the person to close.
type EndedMsg struct{ Reason string }

// The answers of the backend, which come back as messages.
type (
	blockMsg struct {
		name string
		text string
		err  error
		// turn is the "thinking" entry to take away when the block is ready, if there is one.
		turn *Turn
	}
	nextMsg struct {
		turn *Turn
		s    Suggestion
		err  error
		// auto says the question was asked by the panel, not by a key.
		auto bool
	}
	// autoMsg is the end of a quiet period after a change of the map.
	autoMsg   struct{ gen int }
	copiedMsg struct {
		text string
		err  error
	}
	spinMsg struct{}
	typeMsg struct{}
	// quitMsg ends the program after the farewell has been shown.
	quitMsg struct{}
)

// Model is the panel: the stream, and the blocks its keys open.
type Model struct {
	cfg    Config
	width  int
	height int
	events []Event
	// focused is whether this pane has the keyboard; the outline is bright then.
	focused bool

	spinning bool
	typing   bool
	// loading holds the blocks that are being fetched, so that a second press does not ask twice.
	loading map[string]bool
	// autoGen counts the commands that were read; a wait that ends with an older count is stale.
	autoGen int
	// again says a command was read while an answer was on its way: ask once more when it is shown,
	// about everything that came in meanwhile.
	again bool
	// lastAutoErr is the last failure of an automatic question that was shown, so that a model that
	// is down is said once and not after every command.
	lastAutoErr string
	// in is the line the tester types.
	in input
	// quitting says the farewell is shown and the program is about to end.
	quitting bool
}

// New returns the panel.
func New(cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}
	m := &Model{cfg: cfg, width: 80, height: 24, loading: map[string]bool{}}
	if cfg.Notice != "" {
		m.system(cfg.Notice)
	}
	if cfg.Greeting != "" {
		m.system(cfg.Greeting)
	}
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m.onKey(msg)
	case FocusMsg:
		m.focused = msg.Focused
	case AppendMsg:
		m.appendEntries(msg.Events)
	case EndedMsg:
		m.system("the watch ended: " + msg.Reason)
	case MapChangedMsg:
		if m.has(blockMap) {
			return m, m.fetch(blockMap, nil, func(ctx context.Context) (string, error) { return m.cfg.Backend.Map(ctx) })
		}
	case CommandDoneMsg:
		if !m.cfg.Auto || m.cfg.Backend == nil {
			return m, nil
		}
		m.autoGen++
		gen := m.autoGen
		delay := m.cfg.AutoDelay
		if delay <= 0 {
			delay = time.Second
		}
		return m, tea.Tick(delay, func(time.Time) tea.Msg { return autoMsg{gen: gen} })
	case autoMsg:
		if msg.gen != m.autoGen { // another command followed: wait for the last one
			return m, nil
		}
		return m, m.askNext(true)
	case copiedMsg:
		if msg.err != nil {
			m.system("could not copy to the system clipboard (" + friendly(msg.err) + "); the terminal was asked to copy it")
		} else {
			m.system("copied: " + msg.text)
		}
	case blockMsg:
		m.gotBlock(msg)
	case nextMsg:
		return m, m.gotNext(msg)
	case spinMsg:
		return m.spinTick()
	case typeMsg:
		return m.typeTick()
	case quitMsg:
		return m, tea.Quit
	}
	return m, nil
}

// onKey sends a key to the input, except ctrl+c, which starts a gentle exit. A submitted line is a
// command or a message.
func (m *Model) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, nil
	}
	if k.String() == "ctrl+c" {
		return m, m.quit()
	}
	line, submitted, _ := m.in.handle(k)
	if !submitted {
		return m, nil
	}
	return m.submit(line)
}

// submit acts on a line the tester entered: a slash command, or, otherwise, a message to the assistant.
func (m *Model) submit(line string) (tea.Model, tea.Cmd) {
	line = strings.TrimSpace(line)
	if line == "" {
		return m, nil
	}
	if c, ok := parseCommand(line); ok {
		return m.runCommand(c)
	}
	return m, m.ask(line)
}

// runCommand carries out a slash command.
func (m *Model) runCommand(c command) (tea.Model, tea.Cmd) {
	switch c.name {
	case cmdExit:
		return m, m.quit()
	case cmdHelp:
		m.toggle(blockHelp, commandHelp())
	case cmdClear:
		m.events = m.events[:0]
	case cmdMap:
		return m, m.openMap()
	case cmdPipeline:
		return m, m.openPipeline(strings.EqualFold(c.args, cmdNew))
	case cmdNext:
		return m, m.askNext(false)
	case cmdCopy:
		return m, m.copy()
	case cmdAsk:
		if c.args == "" {
			m.system("/ask needs a question, for example: /ask what is on port 8000")
			return m, nil
		}
		return m, m.ask(c.args)
	default:
		m.system("no command /" + c.name + "; /help lists them")
	}
	return m, nil
}

// quit shows the farewell and ends the program a moment later, so the exit is not abrupt.
func (m *Model) quit() tea.Cmd {
	if m.quitting {
		return nil
	}
	m.quitting = true
	m.system(m.farewell())
	return tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return quitMsg{} })
}

func (m *Model) farewell() string {
	if m.cfg.SessionName == "" {
		return "bye."
	}
	return "session " + m.cfg.SessionName + " saved. resume it with:  kagegami watch --case " + m.cfg.SessionName
}

// ask puts the tester's message in the stream and asks the assistant about it.
func (m *Model) ask(question string) tea.Cmd {
	m.events = append(m.events, Event{Kind: KindUser, Time: m.cfg.Now(), Text: question})
	if m.cfg.Backend == nil {
		m.system("nothing is connected to the panel yet")
		return nil
	}
	t := NewTurn()
	m.events = append(m.events, Event{Kind: KindAssistant, Time: m.cfg.Now(), Turn: t})
	ctx := m.cfg.Context
	call := func() tea.Msg {
		s, err := m.cfg.Backend.Ask(ctx, question)
		return nextMsg{turn: t, s: s, err: err}
	}
	return tea.Batch(m.startSpinning(), call)
}

// appendEntries adds entries, stamping any that came without a time.
func (m *Model) appendEntries(events []Event) {
	for _, e := range events {
		if e.Time.IsZero() && (e.Kind == KindCommand || e.Kind == KindSystem || e.Kind == KindFinding || e.Kind == KindNote) {
			e.Time = m.cfg.Now()
		}
		m.events = append(m.events, e)
	}
}

// copy puts the command of the latest suggestion on the clipboard.
func (m *Model) copy() tea.Cmd {
	text := ""
	for i := len(m.events) - 1; i >= 0; i-- {
		if t := m.events[i].Turn; t != nil && len(t.Command) > 0 {
			text = string(t.Command)
			break
		}
	}
	if text == "" {
		m.system("there is no suggested command to copy yet: press n")
		return nil
	}
	cp := m.cfg.Copy
	system := func() tea.Msg {
		if cp == nil {
			return copiedMsg{text: text}
		}
		return copiedMsg{text: text, err: cp(text)}
	}
	return tea.Batch(tea.SetClipboard(text), system)
}

func (m *Model) noBackend() bool {
	if m.cfg.Backend == nil {
		m.system("nothing is connected to the panel yet")
		return true
	}
	return false
}

// openMap shows the map where the key was pressed, or takes it away if it is shown.
func (m *Model) openMap() tea.Cmd {
	if m.remove(blockMap) || m.noBackend() || m.loading[blockMap] {
		return nil
	}
	return m.fetch(blockMap, nil, func(ctx context.Context) (string, error) { return m.cfg.Backend.Map(ctx) })
}

// openPipeline shows the latest plan, asking the model for one if there is none; fresh asks for a
// new one even if there is. A plan that is shown is taken away by the same key.
func (m *Model) openPipeline(fresh bool) tea.Cmd {
	if m.noBackend() || m.loading[blockPipeline] {
		return nil
	}
	if !fresh && m.remove(blockPipeline) {
		return nil
	}
	m.remove(blockPipeline)
	t := NewTurn()
	m.events = append(m.events, Event{Kind: KindAssistant, Time: m.cfg.Now(), Turn: t})
	return tea.Batch(m.startSpinning(), m.fetch(blockPipeline, t, func(ctx context.Context) (string, error) {
		return m.cfg.Backend.Pipeline(ctx, fresh)
	}))
}

// fetch asks the backend for a block off the loop of the panel.
func (m *Model) fetch(name string, t *Turn, get func(context.Context) (string, error)) tea.Cmd {
	m.loading[name] = true
	ctx := m.cfg.Context
	return func() tea.Msg {
		text, err := get(ctx)
		return blockMsg{name: name, text: text, err: err, turn: t}
	}
}

// gotBlock puts a block that arrived in the stream: in place if it is shown already (the map that
// was refreshed), and otherwise at the end.
func (m *Model) gotBlock(msg blockMsg) {
	delete(m.loading, msg.name)
	if msg.turn != nil {
		m.dropTurn(msg.turn)
	}
	if msg.err != nil {
		m.system(friendly(msg.err))
		return
	}
	lines := blockLines(msg.name, msg.text)
	for i := range m.events {
		if m.events[i].Kind == KindBlock && m.events[i].Block == msg.name {
			m.events[i].Lines = lines
			return
		}
	}
	m.events = append(m.events, Event{Kind: KindBlock, Block: msg.name, Lines: lines})
}

// askNext starts an answer of the assistant: a spinner while the model works, then the text typed
// out. auto says the panel asks by itself: it stays silent about what is missing (no backend) and
// does not pile up questions.
func (m *Model) askNext(auto bool) tea.Cmd {
	if m.cfg.Backend == nil {
		if !auto {
			m.noBackend()
		}
		return nil
	}
	for _, e := range m.events {
		if e.Turn != nil && e.Turn.State == turnThinking {
			if auto {
				m.again = true // asked again as soon as this one is answered
			}
			return nil // one question at a time
		}
	}
	t := NewTurn()
	m.events = append(m.events, Event{Kind: KindAssistant, Time: m.cfg.Now(), Turn: t})
	ctx := m.cfg.Context
	next := func() tea.Msg {
		s, err := m.cfg.Backend.Next(ctx, auto)
		return nextMsg{turn: t, s: s, err: err, auto: auto}
	}
	return tea.Batch(m.startSpinning(), next)
}

func (m *Model) gotNext(msg nextMsg) tea.Cmd {
	if msg.err != nil {
		m.dropTurn(msg.turn)
		text := friendly(msg.err)
		if msg.auto && text == m.lastAutoErr {
			return nil // already said, and nothing has changed since
		}
		if msg.auto {
			m.lastAutoErr = text
		}
		m.system(text)
		return m.askAgain()
	}
	m.lastAutoErr = ""
	msg.turn.Meta = ""
	if !msg.s.NeedsRecon {
		msg.turn.Meta = "risk: " + msg.s.Risk + " · confidence: " + msg.s.Confidence
	}
	msg.turn.Answer(msg.s.Rationale, msg.s.Command)
	return tea.Batch(m.startTyping(), m.askAgain())
}

// askAgain asks the question again if commands came in while the last answer was on its way.
func (m *Model) askAgain() tea.Cmd {
	if !m.again {
		return nil
	}
	m.again = false
	return m.askNext(true)
}

// friendly turns an error of the backend into a line for the stream.
func friendly(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }

// has says whether a block is in the stream.
func (m *Model) has(name string) bool {
	for _, e := range m.events {
		if e.Kind == KindBlock && e.Block == name {
			return true
		}
	}
	return false
}

// remove takes a block out of the stream, and says whether it was there.
func (m *Model) remove(name string) bool {
	for i, e := range m.events {
		if e.Kind == KindBlock && e.Block == name {
			m.events = append(m.events[:i], m.events[i+1:]...)
			return true
		}
	}
	return false
}

func (m *Model) dropTurn(t *Turn) {
	for i, e := range m.events {
		if e.Turn == t {
			m.events = append(m.events[:i], m.events[i+1:]...)
			return
		}
	}
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

// spinTick moves the spinner of every waiting answer on, and stops its timer when none is left.
func (m *Model) spinTick() (tea.Model, tea.Cmd) {
	live := false
	for _, e := range m.events {
		if e.Turn != nil && e.Turn.State == turnThinking {
			e.Turn.Spun()
			live = true
		}
	}
	if !live {
		m.spinning = false
		return m, nil
	}
	return m, tea.Tick(spinTick, func(time.Time) tea.Msg { return spinMsg{} })
}

// typeTick types one more character of every answer that is being typed, and stops its timer at the end.
func (m *Model) typeTick() (tea.Model, tea.Cmd) {
	live := false
	for _, e := range m.events {
		if t := e.Turn; t != nil && (t.State == turnRationale || t.State == turnCommand) {
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
	v := tea.NewView(m.screen())
	v.AltScreen = true
	v.WindowTitle = "kagegami"
	return v
}

// screen is the stream and the input line inside the outline.
func (m *Model) screen() string {
	boxed := m.width >= minFrame && m.height >= 4
	w, h := m.width, m.height
	if boxed {
		w, h = m.width-4, m.height-2 // the outline and a space on each side, and the top and bottom lines
	}
	if h < 1 {
		return frame(nil, m.width, m.height, m.focused, "kagegami")
	}
	// the last inner row is the input, with a blank row above it to set it apart
	streamRows := max(h-2, 1)
	rows := m.stream(w, streamRows)
	for len(rows) < streamRows {
		rows = append(rows, "")
	}
	if h >= 2 {
		rows = append(rows, "")
	}
	rows = append(rows, m.in.view(w, m.focused))
	return frame(rows, m.width, m.height, m.focused, "kagegami")
}

// stream lays the entries out one under the other and keeps the last lines that fit.
func (m *Model) stream(width, height int) []string {
	if len(m.events) == 0 {
		hint := "type a message and press Enter, or /help for the commands"
		if m.cfg.Live {
			hint = "watching your shell. type to ask the assistant, /help for the commands"
		}
		return strings.Split(styleDim.Render(lipgloss.NewStyle().Width(max(width, 1)).Render(hint)), "\n")
	}
	var lines []string
	for _, e := range m.events {
		lines = append(lines, renderEvent(e, width)...)
	}
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	return lines
}
