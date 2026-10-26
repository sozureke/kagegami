package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 22, 15, 4, 0, 0, time.UTC) }

func plain(lines []string) string { return ansi.Strip(strings.Join(lines, "\n")) }

func newModel(t *testing.T, cfg Config) *Model {
	t.Helper()
	cfg.Now = fixedNow
	m := New(cfg)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// pressKey sends one key to the model and returns the command.
func pressKey(m *Model, k tea.KeyPressMsg) tea.Cmd {
	_, cmd := m.Update(k)
	return cmd
}

// typeText types each character into the input (no Enter).
func typeText(m *Model, text string) {
	for _, r := range text {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// submit enters a whole line and presses Enter, as the tester would, and returns the command.
func submit(m *Model, line string) tea.Cmd {
	typeText(m, line)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

// cmdRun submits a line and carries out the command it returns.
func cmdRun(m *Model, line string) { run(m, submit(m, line)) }

func ctrlC(m *Model) tea.Cmd { return pressKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestHelpOpensAndClosesTheHelpBlock(t *testing.T) {
	m := newModel(t, Config{})
	submit(m, "/help")
	if len(m.events) != 1 || m.events[0].Block != blockHelp || !strings.Contains(plain(m.events[0].Lines), "/ask") {
		t.Fatalf("events = %+v", m.events)
	}
	submit(m, "/help")
	if len(m.events) != 0 {
		t.Error("/help again closes it")
	}
}

func TestTextHangsUnderTheGutter(t *testing.T) {
	long := "gobuster dir -u http://10.10.11.42 -w /usr/share/wordlists/dirb/common.txt -t 50"
	lines := renderEvent(Event{Kind: KindCommand, Time: fixedNow(), Text: long}, 40)
	if len(lines) < 2 {
		t.Fatalf("a long command must wrap: %v", lines)
	}
	first := ansi.Strip(lines[0])
	if !strings.HasPrefix(first, "15:04 $ gobuster") {
		t.Errorf("first line = %q", first)
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(ansi.Strip(l), strings.Repeat(" ", gutterWidth)) {
			t.Errorf("a continuation line does not hang from the text: %q", ansi.Strip(l))
		}
	}
}

func TestNotesAndFindingsPutTheirMarkerInTheTimeSlot(t *testing.T) {
	note := ansi.Strip(renderEvent(Event{Kind: KindNote, Time: fixedNow(), Text: "look at the backup"}, 60)[0])
	find := ansi.Strip(renderEvent(Event{Kind: KindFinding, Time: fixedNow(), Text: "open listing"}, 60)[0])
	if !strings.HasPrefix(strings.TrimLeft(note, " "), GlyphReport) || strings.Contains(note, "15:04") {
		t.Errorf("note = %q", note)
	}
	if !strings.HasPrefix(strings.TrimLeft(find, " "), "!") || strings.Contains(find, "15:04") {
		t.Errorf("finding = %q", find)
	}
}

func TestEmphasizeKeepsTheTextAndBringsOutTheWords(t *testing.T) {
	text := "22/tcp open ssh · 80/tcp open http"
	got := emphasize(text, []string{"open"})
	if ansi.Strip(got) != text {
		t.Errorf("text changed: %q", ansi.Strip(got))
	}
	if got == styleDim.Render(text) {
		t.Error("the words are not brought out")
	}
	if ansi.Strip(emphasize(text, nil)) != text || ansi.Strip(emphasize(text, []string{""})) != text {
		t.Error("no words, no change")
	}
}

func TestTheTurnKnowsWhenItNeedsATimer(t *testing.T) {
	turn := NewTurn()
	if !turn.Busy() || turn.Ticks() != spinTick {
		t.Error("a thinking turn needs the spinner timer")
	}
	turn.Spun()
	turn.Answer("ab", "c")
	if turn.Ticks() != typeTick {
		t.Error("a typing turn needs the typing timer")
	}
	turn.Typed()
	if r, c := turn.Shown(); r != "a" || c != "" {
		t.Errorf("shown = %q, %q", r, c)
	}
	turn.Typed()
	turn.Typed()
	if r, c := turn.Shown(); r != "ab" || c != "c" || turn.Busy() {
		t.Errorf("shown = %q, %q, busy = %v", r, c, turn.Busy())
	}
	turn.Typed() // typing past the end changes nothing
	if _, c := turn.Shown(); c != "c" {
		t.Error("a finished turn stays as it is")
	}
}

func TestCtrlCAndExitQuitGentlyAndTypingDoesNot(t *testing.T) {
	m := newModel(t, Config{SessionName: "htb"})
	if m.Init() != nil {
		t.Error("the panel has nothing to start")
	}
	typeText(m, "q") // a bare letter goes into the input, it does not quit
	if m.in.empty() || isQuit(pressKey(m, tea.KeyPressMsg{Code: 'x', Text: "x"})) || len(m.events) != 0 {
		t.Error("typing is not a command")
	}
	m.in = input{} // clear what was typed

	// ctrl+c does not quit at once; it shows the farewell and schedules the quit (do not invoke the
	// tick command, which blocks until it fires).
	if cmd := ctrlC(m); cmd == nil || !m.quitting {
		t.Fatal("ctrl+c must start the gentle exit")
	}
	if !strings.Contains(lastEvent(m).Text, "resume it with:  kagegami --case htb") {
		t.Errorf("the farewell must name how to resume: %q", lastEvent(m).Text)
	}
	if _, next := m.Update(quitMsg{}); !isQuit(next) {
		t.Error("after the farewell the program quits")
	}
	// while quitting, keys do nothing
	if pressKey(m, tea.KeyPressMsg{Code: 'a', Text: "a"}) != nil {
		t.Error("keys are ignored once the panel is quitting")
	}
}

func TestExitCommandAlsoQuitsGently(t *testing.T) {
	m := newModel(t, Config{SessionName: "box"})
	if cmd := submit(m, "/exit"); cmd == nil || !m.quitting || !strings.Contains(lastEvent(m).Text, "box") {
		t.Errorf("/exit shows the farewell then quits: %q", lastEvent(m).Text)
	}
	if _, next := m.Update(quitMsg{}); !isQuit(next) {
		t.Error("the quit follows")
	}
}

func TestAnEmptyPanelSaysHowToGetHelp(t *testing.T) {
	m := newModel(t, Config{})
	got := ansi.Strip(m.View().Content)
	if !strings.Contains(got, "type a message") || !strings.Contains(got, "/help") || !strings.Contains(got, "❯") {
		t.Errorf("view = %q", got)
	}
}

func TestEveryLineOfTheViewFitsThePane(t *testing.T) {
	turn := NewTurn()
	turn.Answer("/backup/ lists its directory and nginx serves it on :80, so the listing shows what is in it", "curl -s http://10.10.11.42/backup/")
	for range 400 {
		turn.Typed()
	}
	events := []Event{
		{Kind: KindCommand, Time: fixedNow(), Text: "gobuster dir -u http://10.10.11.42 -w /usr/share/wordlists/dirb/common.txt"},
		{Kind: KindOutput, Text: "/admin (301) · /login (200) · /backup (200)", Words: []string{"/admin"}},
		{Kind: KindSystem, Time: fixedNow(), Glyph: GlyphMap, Text: ":80 gained 3 paths, from gobuster"},
		{Kind: KindFinding, Text: "/backup/ lists its directory (medium)"},
		{Kind: KindNote, Text: "check whether the backup holds a database dump"},
		{Kind: KindAssistant, Time: fixedNow(), Turn: turn},
		{Kind: KindBlock, Block: blockHelp, Lines: commandHelp()},
	}
	for _, w := range []int{14, 20, 33, 60, 120} {
		m := New(Config{Now: fixedNow})
		m.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		m.events = events
		v := m.View()
		lines := strings.Split(v.Content, "\n")
		if len(lines) > 12 {
			t.Errorf("width %d: %d lines for a pane of 12", w, len(lines))
		}
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("width %d: a line is %d columns: %q", w, lipgloss.Width(l), ansi.Strip(l))
			}
		}
		if !v.AltScreen {
			t.Error("the panel takes the whole pane")
		}
	}
}

func TestATurnShowsItsSpinnerThenItsTextAndCommand(t *testing.T) {
	turn := NewTurn()
	ev := Event{Kind: KindAssistant, Time: fixedNow(), Turn: turn}
	if !strings.Contains(plain(renderEvent(ev, 80)), "thinking") {
		t.Error("a thinking turn shows the spinner")
	}
	turn.Answer("the reason", "curl -s http://x/")
	for turn.Busy() {
		turn.Typed()
	}
	out := plain(renderEvent(ev, 80))
	if !strings.Contains(out, "the reason") || !strings.Contains(out, "curl -s http://x/") {
		t.Errorf("finished turn:\n%s", out)
	}
}

func TestAppendMsgAddsEntriesAndStampsTheTime(t *testing.T) {
	m := newModel(t, Config{Live: true})
	m.Update(AppendMsg{Events: []Event{
		{Kind: KindCommand, Text: "nmap 10.0.0.5"},
		{Kind: KindSystem, Glyph: GlyphMap, Text: "map: +1 host"},
		{Kind: KindOutput, Text: "22/tcp open"},
	}})
	if len(m.events) != 3 {
		t.Fatalf("%d entries", len(m.events))
	}
	if m.events[0].Time.IsZero() || m.events[1].Time.IsZero() {
		t.Error("commands and system lines come with the time they were added")
	}
	if !m.events[2].Time.IsZero() {
		t.Error("an output line has no time of its own")
	}
	m.Update(EndedMsg{Reason: "the watched pane was closed"})
	last := m.events[len(m.events)-1]
	if last.Kind != KindSystem || !strings.Contains(last.Text, "the watch ended: the watched pane was closed") {
		t.Errorf("last entry = %+v", last)
	}
}

func TestTheEmptyLivePanelInvitesACommand(t *testing.T) {
	live := ansi.Strip(newModel(t, Config{Live: true}).View().Content)
	if !strings.Contains(live, "watching your shell") || !strings.Contains(live, "/help") {
		t.Errorf("live view = %q", live)
	}
	if strings.Contains(ansi.Strip(newModel(t, Config{}).View().Content), "other pane") {
		t.Error("a panel that watches nothing must not ask for commands")
	}
}

func TestTheOutlineFollowsTheFocusAndFitsThePane(t *testing.T) {
	m := newModel(t, Config{Live: true})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "nmap -sV 10.0.0.5"}}})

	dim := m.View().Content
	m.Update(FocusMsg{Focused: true})
	bright := m.View().Content
	if dim == bright {
		t.Error("the outline must look different when the pane has the keyboard")
	}
	if ansi.Strip(dim) != ansi.Strip(bright) {
		t.Error("focus changes the colour only, never the layout")
	}

	lines := strings.Split(ansi.Strip(bright), "\n")
	if len(lines) != 8 {
		t.Fatalf("%d lines for a pane of 8", len(lines))
	}
	for i, l := range lines {
		if lipgloss.Width(l) != 40 {
			t.Errorf("line %d is %d columns, want 40: %q", i, lipgloss.Width(l), l)
		}
	}
	if !strings.HasPrefix(lines[0], "╭─ kagegami ") || !strings.HasSuffix(lines[0], "╮") || !strings.HasPrefix(lines[7], "╰") || !strings.HasSuffix(lines[7], "╯") {
		t.Errorf("outline = %q ... %q", lines[0], lines[7])
	}
	if !strings.Contains(lines[1], "$ nmap -sV 10.0.0.5") || !strings.HasPrefix(lines[1], "│ ") || !strings.HasSuffix(lines[1], " │") {
		t.Errorf("first line of the stream = %q", lines[1])
	}
	m.Update(FocusMsg{Focused: false})
	if m.View().Content != dim {
		t.Error("losing the focus must give back the dim outline")
	}
}

func TestANarrowPaneHasNoOutlineAndALongStreamKeepsItsLastLines(t *testing.T) {
	m := newModel(t, Config{})
	m.Update(tea.WindowSizeMsg{Width: minFrame - 1, Height: 6})
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "ls"}}})
	if strings.Contains(ansi.Strip(m.View().Content), "╭") {
		t.Error("a pane too narrow for an outline keeps the room for the text")
	}

	m.Update(tea.WindowSizeMsg{Width: 50, Height: 8})
	for i := range 30 {
		m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "cmd-" + string(rune('a'+i%26))}}})
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) != 8 {
		t.Fatalf("view =\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[len(lines)-2], "❯") {
		t.Errorf("the input is the last inner line: %q", lines[len(lines)-2])
	}
	stream := lines[1 : len(lines)-3] // between the top outline and the blank + input + bottom outline
	if last := stream[len(stream)-1]; !strings.Contains(last, "cmd-d") {
		t.Errorf("the newest command is at the bottom of the stream: %q", last)
	}
	if strings.Contains(strings.Join(stream, "\n"), "$ cmd-"+string(rune('a'+30-1-len(stream)))) {
		t.Error("more than the last lines were kept")
	}
}

// fakeBackend answers the keys of the panel and counts how often it was asked.
type fakeBackend struct {
	mapText   string
	pipeText  string
	next      Suggestion
	err       error
	maps      int
	pipelines []bool
	nexts     int
	autos     []bool
	asked     []string
	// onboard is what Onboard answers; the zero value says the case has a scope already.
	onboard    Setup
	onboarded  []string
	configure  Setup
	configured []Settings
}

func (f *fakeBackend) Map(context.Context) (string, error) { f.maps++; return f.mapText, f.err }
func (f *fakeBackend) Pipeline(_ context.Context, fresh bool) (string, error) {
	f.pipelines = append(f.pipelines, fresh)
	return f.pipeText, f.err
}

func (f *fakeBackend) Next(_ context.Context, auto bool) (Suggestion, error) {
	f.nexts++
	f.autos = append(f.autos, auto)
	return f.next, f.err
}

func (f *fakeBackend) Onboard(_ context.Context, msg string) (Setup, error) {
	f.onboarded = append(f.onboarded, msg)
	return f.onboard, f.err
}

func (f *fakeBackend) Configure(_ context.Context, s Settings) (Setup, error) {
	f.configured = append(f.configured, s)
	return f.configure, f.err
}

func (f *fakeBackend) Ask(_ context.Context, q string) (Suggestion, error) {
	f.asked = append(f.asked, q)
	return f.next, f.err
}

// run carries out a command and feeds what it returns to the model. The timers are left to the test,
// which moves them on by hand, so that nothing waits and nothing loops.
func run(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			run(m, c)
		}
	case spinMsg, typeMsg:
	default:
		_, next := m.Update(msg)
		run(m, next)
	}
}

func liveModel(t *testing.T, b Backend) *Model {
	t.Helper()
	return newModel(t, Config{Live: true, Backend: b})
}

func lastEvent(m *Model) Event { return m.events[len(m.events)-1] }

func TestWithoutABackendTheKeysSayNothingIsConnected(t *testing.T) {
	m := liveModel(t, nil)
	for _, line := range []string{"/map", "/pipeline", "/pipeline new", "/next"} {
		cmdRun(m, line)
	}
	if len(m.events) != 4 {
		t.Fatalf("%d entries", len(m.events))
	}
	for _, e := range m.events {
		if e.Kind != KindSystem || !strings.Contains(e.Text, "nothing is connected") {
			t.Errorf("entry = %+v", e)
		}
	}
}

func TestMShowsTheMapAndTheSameKeyTakesItAway(t *testing.T) {
	b := &fakeBackend{mapText: "10.0.0.5  box.htb\n├─ 22/tcp  open  ssh\n"}
	m := liveModel(t, b)
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "nmap 10.0.0.5"}}})
	cmdRun(m, "/map")
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "ffuf"}}})

	if len(m.events) != 3 || m.events[1].Block != blockMap {
		t.Fatalf("entries = %+v", m.events)
	}
	block := plain(m.events[1].Lines)
	if !strings.HasPrefix(block, "map\n") || !strings.Contains(block, "22/tcp  open  ssh") {
		t.Errorf("block =\n%s", block)
	}
	cmdRun(m, "/map")
	if m.has(blockMap) || len(m.events) != 2 || b.maps != 1 {
		t.Errorf("the same key must take it away without asking again: %d entries, %d asks", len(m.events), b.maps)
	}
}

func TestAnOpenMapIsRefreshedInPlaceWhenTheMapChanges(t *testing.T) {
	b := &fakeBackend{mapText: "one host"}
	m := liveModel(t, b)
	cmdRun(m, "/map")
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "later"}}})
	b.mapText = "two hosts"
	run(m, func() tea.Msg { _, c := m.Update(MapChangedMsg{}); return c() })

	if b.maps != 2 || len(m.events) != 2 || m.events[0].Block != blockMap || !strings.Contains(plain(m.events[0].Lines), "two hosts") {
		t.Errorf("maps=%d entries=%+v", b.maps, m.events)
	}
	cmdRun(m, "/map")
	if _, cmd := m.Update(MapChangedMsg{}); cmd != nil || b.maps != 2 {
		t.Error("a map that is not shown is not fetched")
	}
}

func TestMAsksOnceWhileTheMapIsOnItsWay(t *testing.T) {
	b := &fakeBackend{mapText: "m"}
	m := liveModel(t, b)
	first := submit(m, "/map")
	if second := submit(m, "/map"); second != nil {
		t.Error("a second press while the map is being fetched must do nothing")
	}
	run(m, first)
	if b.maps != 1 || len(m.events) != 1 {
		t.Errorf("asks = %d, entries = %d", b.maps, len(m.events))
	}
}

func TestPShowsTheLatestPipelineAndPCapitalAsksForANewOne(t *testing.T) {
	b := &fakeBackend{pipeText: "1. [high] web content discovery\n   $ gobuster dir -u http://10.0.0.5\n"}
	m := liveModel(t, b)
	cmd := submit(m, "/pipeline")
	if lastEvent(m).Turn == nil || lastEvent(m).Turn.State != turnThinking {
		t.Fatal("while the plan is on its way the stream shows that the assistant is working")
	}
	run(m, cmd)
	if len(m.events) != 1 || m.events[0].Block != blockPipeline || !strings.Contains(plain(m.events[0].Lines), "web content discovery") {
		t.Fatalf("entries = %+v", m.events)
	}
	cmdRun(m, "/pipeline")
	if m.has(blockPipeline) || len(b.pipelines) != 1 {
		t.Errorf("p again takes it away and asks nothing: calls %v", b.pipelines)
	}

	cmdRun(m, "/pipeline")
	b.pipeText = "a newer plan"
	cmdRun(m, "/pipeline new")
	if len(b.pipelines) != 3 || b.pipelines[0] || b.pipelines[1] || !b.pipelines[2] {
		t.Errorf("calls = %v: p asks for the latest, P for a fresh one", b.pipelines)
	}
	if len(m.events) != 1 || !strings.Contains(plain(m.events[0].Lines), "a newer plan") {
		t.Errorf("the new plan replaces the old one: %+v", m.events)
	}
}

func TestAFailedBlockLeavesALineAndNoSpinner(t *testing.T) {
	b := &fakeBackend{err: errors.New("the case has no scope:\n set one")}
	m := liveModel(t, b)
	cmdRun(m, "/pipeline")
	if len(m.events) != 1 || m.events[0].Kind != KindSystem || !strings.Contains(m.events[0].Text, "the case has no scope: set one") {
		t.Errorf("entries = %+v", m.events)
	}
	cmdRun(m, "/map")
	if len(m.events) != 2 || m.has(blockMap) {
		t.Errorf("a map that failed to load is not shown: %+v", m.events)
	}
	b.err = nil
	b.mapText = "ok"
	cmdRun(m, "/map")
	if !m.has(blockMap) {
		t.Error("after a failure the key must work again")
	}
}

func TestNAnswersWithASpinnerThenTypedTextAndItsRisk(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "port 80 serves nginx", Command: "gobuster dir -u http://10.0.0.5", Risk: "medium", Confidence: "high"}}
	m := liveModel(t, b)
	cmd := submit(m, "/next")
	turn := lastEvent(m).Turn
	if turn == nil || turn.State != turnThinking || !strings.Contains(plain(renderEvent(lastEvent(m), 80)), "thinking") {
		t.Fatal("the answer starts as a spinner")
	}
	if again := submit(m, "/next"); again != nil || len(m.events) != 1 {
		t.Error("one question at a time")
	}
	run(m, cmd)
	if turn.State != turnRationale || !m.typing {
		t.Fatalf("the answer must start typing: state %v", turn.State)
	}
	for m.typing {
		m.typeTick()
	}
	out := plain(renderEvent(lastEvent(m), 80))
	for _, want := range []string{"port 80 serves nginx", "gobuster dir -u http://10.0.0.5", "risk: medium · confidence: high"} {
		if !strings.Contains(out, want) {
			t.Errorf("the finished answer lacks %q:\n%s", want, out)
		}
	}
	if b.nexts != 1 {
		t.Errorf("%d questions", b.nexts)
	}
}

func TestNWithNothingToSuggestShowsTheReasonAndNoCommandBox(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "no services are known; scan the target first", NeedsRecon: true}}
	m := liveModel(t, b)
	cmdRun(m, "/next")
	for m.typing {
		m.typeTick()
	}
	out := plain(renderEvent(lastEvent(m), 80))
	if !strings.Contains(out, "scan the target first") || strings.Contains(out, "╭") || strings.Contains(out, "risk:") {
		t.Errorf("answer:\n%s", out)
	}
}

func TestNThatFailsLeavesTheErrorInTheStreamAndFreesTheKey(t *testing.T) {
	b := &fakeBackend{err: errors.New("connection refused")}
	m := liveModel(t, b)
	cmdRun(m, "/next")
	if len(m.events) != 1 || m.events[0].Kind != KindSystem || !strings.Contains(m.events[0].Text, "connection refused") {
		t.Fatalf("entries = %+v", m.events)
	}
	b.err = nil
	b.next = Suggestion{Rationale: "r", Command: "c", Risk: "low", Confidence: "low"}
	cmdRun(m, "/next")
	if b.nexts != 2 {
		t.Error("after an error the key must work again")
	}
}

func TestTheSpinnerTimerStopsWhenNothingWaits(t *testing.T) {
	m := liveModel(t, &fakeBackend{next: Suggestion{Rationale: "r", Command: "c"}})
	cmd := submit(m, "/next")
	if _, next := m.spinTick(); next == nil {
		t.Error("the spinner goes on while an answer is awaited")
	}
	run(m, cmd)
	for m.typing {
		m.typeTick()
	}
	if _, next := m.spinTick(); next != nil || m.spinning {
		t.Error("with nothing awaited the spinner timer stops")
	}
}

func TestTheHelpListsTheSlashCommands(t *testing.T) {
	out := plain(commandHelp())
	for _, want := range []string{"/ask", "/next", "/map", "/pipeline", "/copy", "/clear", "/help", "/exit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "a block opened by a key") {
		t.Errorf("the help lists commands, not entry kinds:\n%s", out)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in    string
		name  string
		args  string
		isCmd bool
	}{
		{"/map", "map", "", true},
		{"/pipeline new", "pipeline", "new", true},
		{"/ask what is on 8000", "ask", "what is on 8000", true},
		{"/MAP", "map", "", true},
		{"/ask   ", "ask", "", true},
		{"not a command", "", "", false},
		{"/", "", "", false},
		{"/ not a command", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		c, ok := parseCommand(tt.in)
		if ok != tt.isCmd || (ok && (c.name != tt.name || c.args != tt.args)) {
			t.Errorf("parseCommand(%q) = %+v, %v", tt.in, c, ok)
		}
	}
}

func TestALongLineOfABlockIsWrappedNotCut(t *testing.T) {
	long := "why: nothing has been found on the web server yet, so a scan of its paths is the cheapest way forward"
	ev := Event{Kind: KindBlock, Block: blockPipeline, Lines: []string{styleMid.Render(long)}}
	lines := renderEvent(ev, 40)
	if len(lines) < 3 {
		t.Fatalf("a long line must wrap: %v", lines)
	}
	flat := strings.Join(strings.Fields(plain(lines)), " ")
	if !strings.Contains(flat, "cheapest way forward") || strings.Contains(flat, "…") {
		t.Errorf("text was lost or cut:\n%s", plain(lines))
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 40 {
			t.Errorf("a line is %d columns wide: %q", lipgloss.Width(l), ansi.Strip(l))
		}
	}
}

func autoModel(t *testing.T, b Backend) *Model {
	t.Helper()
	return newModel(t, Config{Live: true, Backend: b, Auto: true, AutoDelay: time.Millisecond})
}

// commandDone tells the model a command was read and returns the end of the wait it starts.
func commandDone(m *Model) autoMsg {
	_, cmd := m.Update(CommandDoneMsg{})
	if cmd == nil {
		return autoMsg{gen: -1}
	}
	msg, _ := cmd().(autoMsg)
	return msg
}

func TestEveryReadCommandIsLookedAtByTheAgentWithoutAnyKey(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "the scan shows a web server on 8000", Command: "gobuster dir -u http://127.0.0.1:8000", Risk: "low", Confidence: "high"}}
	m := autoModel(t, b)

	wait := commandDone(m)
	if b.nexts != 0 {
		t.Fatal("the agent waits a moment for another command before it asks")
	}
	_, cmd := m.Update(wait)
	if lastEvent(m).Turn == nil {
		t.Fatal("after the wait the assistant starts to answer, and nobody pressed a key")
	}
	run(m, cmd)
	for m.typing {
		m.typeTick()
	}
	if b.nexts != 1 || len(b.autos) != 1 || !b.autos[0] {
		t.Errorf("questions = %d, auto flags = %v: the panel must say it asks because a command finished", b.nexts, b.autos)
	}
	if !strings.Contains(plain(renderEvent(lastEvent(m), 80)), "gobuster dir -u http://127.0.0.1:8000") {
		t.Errorf("answer:\n%s", plain(renderEvent(lastEvent(m), 80)))
	}
}

func TestTheKeyAsksWithoutSayingACommandJustFinished(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "c"}}
	m := liveModel(t, b)
	cmdRun(m, "/next")
	if len(b.autos) != 1 || b.autos[0] {
		t.Errorf("auto flags = %v: a person's question is not a reaction to a command", b.autos)
	}
}

func TestACommandAfterACommandIsOneQuestionAboutTheLast(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "c", Risk: "low", Confidence: "low"}}
	m := autoModel(t, b)
	first, second, third := commandDone(m), commandDone(m), commandDone(m)

	for _, stale := range []autoMsg{first, second} {
		if _, cmd := m.Update(stale); cmd != nil || len(m.events) != 0 {
			t.Fatal("the end of an older wait does nothing: another command followed")
		}
	}
	_, cmd := m.Update(third)
	run(m, cmd)
	if b.nexts != 1 {
		t.Errorf("three commands in a row were asked about %d times, want once", b.nexts)
	}
}

func TestACommandThatArrivesWhileTheAgentThinksIsAskedAboutRightAfter(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "c", Risk: "low", Confidence: "low"}}
	m := autoModel(t, b)
	_, firstAsk := m.Update(commandDone(m))
	if b.nexts != 0 || lastEvent(m).Turn.State != turnThinking {
		t.Fatal("the first question is on its way")
	}

	_, during := m.Update(commandDone(m)) // a command was read while the model is busy
	if during != nil || b.nexts != 0 {
		t.Fatal("no second question while one is on its way")
	}
	run(m, firstAsk) // the first answer arrives; the model is asked again at once, about what came since
	if b.nexts != 2 {
		t.Errorf("%d questions: the command that came in meanwhile was never looked at", b.nexts)
	}
	for m.typing {
		m.typeTick()
	}
	if got := len(b.autos); got != 2 || !b.autos[1] {
		t.Errorf("auto flags = %v", b.autos)
	}
}

func TestACommandThatArrivesWhileThePersonAsksIsAskedAboutRightAfter(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "c"}}
	m := autoModel(t, b)
	pending := submit(m, "/next")
	m.Update(commandDone(m))
	run(m, pending)
	if b.nexts != 2 {
		t.Errorf("%d questions, want the person's and then the one about the new command", b.nexts)
	}
}

func TestWithAutoOffACommandDoesNotMakeTheAgentAsk(t *testing.T) {
	b := &fakeBackend{}
	m := newModel(t, Config{Live: true, Backend: b})
	if wait := commandDone(m); wait.gen != -1 || b.nexts != 0 || len(m.events) != 0 {
		t.Errorf("wait=%+v nexts=%d entries=%d", wait, b.nexts, len(m.events))
	}
	if wait := commandDone(autoModel(t, nil)); wait.gen != -1 {
		t.Error("with no backend there is nothing to ask")
	}
}

func TestAModelThatIsDownIsSaidOnceNotAfterEveryCommand(t *testing.T) {
	b := &fakeBackend{err: errors.New("ollama is not running")}
	m := autoModel(t, b)
	for range 3 {
		_, cmd := m.Update(commandDone(m))
		run(m, cmd)
	}
	count := func() int {
		n := 0
		for _, e := range m.events {
			if e.Kind == KindSystem && strings.Contains(e.Text, "ollama is not running") {
				n++
			}
		}
		return n
	}
	if count() != 1 || b.nexts != 3 {
		t.Errorf("the failure was shown %d times after %d questions, want once", count(), b.nexts)
	}

	b.err = nil
	b.next = Suggestion{Rationale: "r", Command: "c", Risk: "low", Confidence: "low"}
	_, cmd := m.Update(commandDone(m))
	run(m, cmd)
	for m.typing {
		m.typeTick()
	}
	b.err = errors.New("ollama is not running")
	_, cmd = m.Update(commandDone(m))
	run(m, cmd)
	if count() != 2 {
		t.Errorf("after an answer got through, a new failure is news again: shown %d times", count())
	}
}

func TestAKeyThatAsksIsNeverMuteAboutAFailure(t *testing.T) {
	b := &fakeBackend{err: errors.New("boom")}
	m := liveModel(t, b)
	cmdRun(m, "/next")
	cmdRun(m, "/next")
	if n := len(m.events); n != 2 {
		t.Errorf("a person who presses n twice sees both failures, got %d entries", n)
	}
}

func TestCCopiesTheLatestSuggestedCommand(t *testing.T) {
	var copied []string
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "gobuster dir -u http://10.0.0.5", Risk: "low", Confidence: "low"}}
	m := newModel(t, Config{Live: true, Backend: b, Copy: func(s string) error { copied = append(copied, s); return nil }})

	cmdRun(m, "/copy")
	if len(copied) != 0 || !strings.Contains(lastEvent(m).Text, "no suggested command to copy") {
		t.Fatalf("nothing to copy yet: %v, %+v", copied, lastEvent(m))
	}
	cmdRun(m, "/next")
	for m.typing {
		m.typeTick()
	}
	cmd := submit(m, "/copy")
	var seen []tea.Msg
	var walk func(c tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, x := range batch {
				walk(x)
			}
			return
		}
		seen = append(seen, msg)
		if _, ok := msg.(copiedMsg); ok {
			m.Update(msg)
		}
	}
	walk(cmd)
	if len(copied) != 1 || copied[0] != "gobuster dir -u http://10.0.0.5" {
		t.Errorf("copied = %v", copied)
	}
	if len(seen) != 2 {
		t.Errorf("both the system clipboard and the terminal's own (OSC 52) are asked: %d messages", len(seen))
	}
	if got := lastEvent(m).Text; got != "copied: gobuster dir -u http://10.0.0.5" {
		t.Errorf("last entry = %q", got)
	}
}

func TestACopyThatFailsSaysSoAndNamesTheFallback(t *testing.T) {
	m := newModel(t, Config{Live: true})
	m.Update(copiedMsg{text: "ls", err: errors.New("no clipboard tool found")})
	if got := lastEvent(m).Text; !strings.Contains(got, "no clipboard tool found") || !strings.Contains(got, "the terminal was asked") {
		t.Errorf("entry = %q", got)
	}
}

func TestANoticeIsTheFirstLineOfTheStream(t *testing.T) {
	m := newModel(t, Config{Live: true, Notice: "a model is not on this machine"})
	if len(m.events) != 1 || m.events[0].Kind != KindSystem || m.events[0].Text != "a model is not on this machine" || m.events[0].Time.IsZero() {
		t.Fatalf("entries = %+v", m.events)
	}
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "not on this machine") {
		t.Errorf("view = %q", got)
	}
	if len(newModel(t, Config{Live: true}).events) != 0 {
		t.Error("with no notice the stream starts empty")
	}
}

func TestTheInputEditsAndSubmitsALine(t *testing.T) {
	m := newModel(t, Config{})
	typeText(m, "nmap")
	if string(m.in.runes) != "nmap" || m.in.cursor != 4 {
		t.Fatalf("after typing: %q cursor %d", string(m.in.runes), m.in.cursor)
	}
	// left twice, insert, so "nmXap"
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	typeText(m, "X")
	if string(m.in.runes) != "nmXap" {
		t.Errorf("insert in the middle: %q", string(m.in.runes))
	}
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if string(m.in.runes) != "nmap" {
		t.Errorf("backspace: %q", string(m.in.runes))
	}
	pressKey(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) // ctrl+u clears
	if !m.in.empty() {
		t.Error("ctrl+u clears the line")
	}
	typeText(m, "run a full scan")
	pressKey(m, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}) // ctrl+w deletes a word
	if string(m.in.runes) != "run a full " {
		t.Errorf("ctrl+w: %q", string(m.in.runes))
	}
}

func TestTypingAMessageAsksTheAssistantAndShowsItInTheStream(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "port 8000 is a Python http server", Command: "curl -s http://10.0.0.5:8000/", Risk: "low", Confidence: "medium"}}
	m := liveModel(t, b)
	cmd := submit(m, "what is on port 8000?")
	if len(m.events) != 1 || m.events[0].Kind != KindUser || m.events[0].Text != "what is on port 8000?" {
		t.Fatalf("the question is shown as the tester's own: %+v", m.events)
	}
	run(m, cmd)
	if lastEvent(m).Turn == nil {
		t.Fatal("the assistant starts to answer")
	}
	for m.typing {
		m.typeTick()
	}
	if len(b.asked) != 1 || b.asked[0] != "what is on port 8000?" {
		t.Errorf("asked = %v", b.asked)
	}
	out := plain(renderEvent(lastEvent(m), 80))
	if !strings.Contains(out, "port 8000 is a Python http server") || !strings.Contains(out, "curl -s http://10.0.0.5:8000/") {
		t.Errorf("answer:\n%s", out)
	}
}

func TestSlashAskIsTheSameAsTyping(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "c"}}
	m := liveModel(t, b)
	cmdRun(m, "/ask is there a web server")
	for m.typing {
		m.typeTick()
	}
	if len(b.asked) != 1 || b.asked[0] != "is there a web server" {
		t.Errorf("asked = %v", b.asked)
	}
	submit(m, "/ask")
	if !strings.Contains(lastEvent(m).Text, "/ask needs a question") || len(b.asked) != 1 {
		t.Errorf("an empty /ask explains itself: %q", lastEvent(m).Text)
	}
}

func TestAnUnknownCommandIsSaidAndClearWipesTheStream(t *testing.T) {
	m := liveModel(t, &fakeBackend{})
	submit(m, "/wut")
	if !strings.Contains(lastEvent(m).Text, "no command /wut") {
		t.Errorf("entry = %q", lastEvent(m).Text)
	}
	submit(m, "/clear")
	if len(m.events) != 0 {
		t.Errorf("/clear wipes the stream: %d entries", len(m.events))
	}
}

func TestANoticeAndAGreetingAreTheFirstLines(t *testing.T) {
	m := newModel(t, Config{Notice: "a warning", Greeting: "set your target"})
	if len(m.events) != 2 || m.events[0].Text != "a warning" || m.events[1].Text != "set your target" {
		t.Fatalf("events = %+v", m.events)
	}
}

func TestTheInputShowsACursorOnlyWhenFocused(t *testing.T) {
	in := &input{}
	in.handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	focused := in.view(40, true)
	blurred := in.view(40, false)
	if strings.TrimRight(ansi.Strip(focused), " ") != strings.TrimRight(ansi.Strip(blurred), " ") {
		t.Error("focus changes the look, not the text")
	}
	if focused == blurred {
		t.Error("the cursor shows only with the keyboard")
	}
	// a long line scrolls so the cursor stays in view
	long := &input{}
	typeTextInput(long, strings.Repeat("x", 100))
	v := ansi.Strip(long.view(20, true))
	if lipgloss.Width(v) > 20 {
		t.Errorf("the input is %d wide for a pane of 20", lipgloss.Width(v))
	}
}

func typeTextInput(in *input, text string) {
	for _, r := range text {
		in.handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestASlashOpensTheListOfCommandsAndNarrowsAsYouType(t *testing.T) {
	m := newModel(t, Config{Backend: &fakeBackend{}})
	if strings.Contains(ansi.Strip(m.View().Content), "/pipeline") {
		t.Fatal("the list is closed until a slash is typed")
	}
	typeText(m, "/")
	screen := ansi.Strip(m.View().Content)
	for _, c := range commandList {
		if !strings.Contains(screen, c.usage) {
			t.Errorf("after a slash the list lacks %q:\n%s", c.usage, screen)
		}
	}
	typeText(m, "p")
	screen = ansi.Strip(m.View().Content)
	if !strings.Contains(screen, "/pipeline") || strings.Contains(screen, "/clear") {
		t.Errorf("after /p only the commands that start with p remain:\n%s", screen)
	}
	typeText(m, "x") // nothing starts with px
	if strings.Contains(ansi.Strip(m.View().Content), "/pipeline") {
		t.Error("no command matches, so the list closes")
	}
	typeText(m, " ")
	if len(matchCommands("/ask what")) != 0 {
		t.Error("once the arguments are typed nothing is offered")
	}
}

func TestTheListIsDrivenByUpDownTabAndEnter(t *testing.T) {
	m := newModel(t, Config{Backend: &fakeBackend{}})
	typeText(m, "/")
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyDown}) // the second of the list, /target
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.in.text(); got != "/target " {
		t.Errorf("tab completes the chosen command: input = %q", got)
	}

	m = newModel(t, Config{Backend: &fakeBackend{}})
	typeText(m, "/")
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyUp}) // wraps round to the last, /exit
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.in.text(); got != "/exit" {
		t.Errorf("up from the first goes to the last: input = %q", got)
	}

	// enter on /ask, which needs a question, completes it and waits
	m = newModel(t, Config{Backend: &fakeBackend{}})
	typeText(m, "/")
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.in.text(); got != "/ask " || m.quitting {
		t.Errorf("enter on a command that needs an argument completes it: input = %q", got)
	}

	// enter on the start of a name runs the chosen command
	m = newModel(t, Config{Backend: &fakeBackend{}})
	typeText(m, "/he")
	pressKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.has(blockHelp) || !m.in.empty() {
		t.Errorf("enter on /he runs /help: help open = %v, input = %q", m.has(blockHelp), m.in.text())
	}
}

func TestTheListFitsASmallPane(t *testing.T) {
	m := newModel(t, Config{})
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 8})
	typeText(m, "/")
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) != 8 {
		t.Fatalf("%d lines for a pane of 8", len(lines))
	}
	if !strings.Contains(lines[len(lines)-2], "❯ /") {
		t.Errorf("the input stays on the last row: %q", lines[len(lines)-2])
	}
}

func TestTheFirstMessageOfACaseWithoutAScopeIsTheTaskAndStartsTheWork(t *testing.T) {
	b := &fakeBackend{onboard: Setup{Handled: true, Started: true, Summary: "task taken. target 10.10.10.5 · scope 10.10.10.5"}, next: Suggestion{Rationale: "start with a port scan", Command: "nmap -sV 10.10.10.5"}}
	m := newModel(t, Config{Backend: b})
	cmdRun(m, "get the user flag on 10.10.10.5")
	for m.typing || m.spinning {
		m.typeTick()
		m.spinning = false
	}
	if len(b.asked) != 0 {
		t.Errorf("the task is not a question: Ask was called with %v", b.asked)
	}
	if b.nexts != 1 || len(b.autos) != 1 || b.autos[0] {
		t.Errorf("once the target is set the first step is asked for: nexts = %d, autos = %v", b.nexts, b.autos)
	}
	var all []string
	for _, e := range m.events {
		all = append(all, e.Text)
	}
	if got := strings.Join(all, "\n"); !strings.Contains(got, "get the user flag") || !strings.Contains(got, "task taken. target 10.10.10.5") {
		t.Errorf("the stream shows the message and what was set:\n%s", got)
	}
}

func TestAMessageBeforeAnyTargetIsAnsweredWithoutTheModel(t *testing.T) {
	b := &fakeBackend{onboard: Setup{Handled: true, Summary: "give me the target"}}
	m := newModel(t, Config{Backend: b})
	cmdRun(m, "hello")
	if len(b.asked) != 0 || b.nexts != 0 {
		t.Error("with no target nothing is asked of the model")
	}
	if got := lastEvent(m).Text; got != "give me the target" {
		t.Errorf("last = %q", got)
	}
}

func TestACaseWithAScopeTakesMessagesAsQuestions(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "port 8000 is a dev server"}}
	m := newModel(t, Config{Backend: b})
	cmdRun(m, "what is on 8000?")
	if len(b.onboarded) != 1 || len(b.asked) != 1 || b.asked[0] != "what is on 8000?" {
		t.Errorf("onboarded = %v, asked = %v", b.onboarded, b.asked)
	}
}

func TestTargetGoalAndScopeCommandsChangeTheCase(t *testing.T) {
	b := &fakeBackend{configure: Setup{Summary: "target 10.10.10.5"}}
	m := newModel(t, Config{Backend: b})
	cmdRun(m, "/target 10.10.10.5")
	cmdRun(m, "/goal user and root flag")
	cmdRun(m, "/scope 10.10.10.0/24 10.10.20.5")
	cmdRun(m, "/scope add 192.168.1.50")
	cmdRun(m, "/scope")
	if len(b.configured) != 5 {
		t.Fatalf("%d changes: %+v", len(b.configured), b.configured)
	}
	if s := b.configured[0]; s.Target == nil || *s.Target != "10.10.10.5" {
		t.Errorf("/target = %+v", s)
	}
	if s := b.configured[1]; s.Goal == nil || *s.Goal != "user and root flag" {
		t.Errorf("/goal = %+v", s)
	}
	if s := b.configured[2]; s.Scope == nil || (*s.Scope)[0] != "10.10.10.0/24 10.10.20.5" {
		t.Errorf("/scope list = %+v", s)
	}
	if s := b.configured[3]; len(s.AddScope) != 1 || s.AddScope[0] != "192.168.1.50" || s.Scope != nil {
		t.Errorf("/scope add = %+v", s)
	}
	if s := b.configured[4]; s.Scope != nil || s.AddScope != nil || s.Target != nil || s.Goal != nil {
		t.Errorf("/scope alone only shows: %+v", s)
	}
	for _, line := range []string{"/target", "/goal"} {
		before := len(b.configured)
		cmdRun(m, line)
		if len(b.configured) != before {
			t.Errorf("%s without an argument must not change anything", line)
		}
	}
}

func TestACommandThatGivesTheCaseItsFirstScopeAsksForTheFirstStep(t *testing.T) {
	b := &fakeBackend{configure: Setup{Summary: "target 10.10.10.5 · scope 10.10.10.5", Started: true}}
	m := newModel(t, Config{Backend: b})
	cmdRun(m, "/target 10.10.10.5")
	if b.nexts != 1 {
		t.Errorf("nexts = %d", b.nexts)
	}
}
