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

func press(m *Model, k string) tea.Cmd {
	var key tea.Msg
	switch k {
	case "ctrl+c":
		key = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		key = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
	}
	_, cmd := m.Update(key)
	return cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestTheHelpBlockOpensAndClosesWithItsKey(t *testing.T) {
	m := newModel(t, Config{})
	press(m, "?")
	if len(m.events) != 1 || m.events[0].Block != blockHelp || !strings.Contains(plain(m.events[0].Lines), "ask for the next step") {
		t.Fatalf("events = %+v", m.events)
	}
	press(m, "?")
	if len(m.events) != 0 {
		t.Error("the same key closes it")
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

func TestQAndCtrlCQuit(t *testing.T) {
	m := newModel(t, Config{})
	if m.Init() != nil {
		t.Error("the panel has nothing to start")
	}
	if !isQuit(press(m, "q")) || !isQuit(press(m, "ctrl+c")) {
		t.Error("q and ctrl+c quit")
	}
	if press(m, "x") != nil || len(m.events) != 0 {
		t.Error("a key with no meaning does nothing")
	}
}

func TestAnEmptyPanelSaysHowToGetHelp(t *testing.T) {
	m := newModel(t, Config{})
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "press ? for the keys") {
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
		{Kind: KindBlock, Block: blockHelp, Lines: helpLines()},
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
	if !strings.Contains(live, "run a command in the other pane") || !strings.Contains(live, "? shows the keys") {
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

	m.Update(tea.WindowSizeMsg{Width: 50, Height: 6})
	for i := range 30 {
		m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "cmd-" + string(rune('a'+i%26))}}})
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) != 6 || !strings.Contains(lines[4], "cmd-") {
		t.Errorf("view =\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "cmd-d") { // the 30th of 30 is index 29: 'd'
		t.Error("the newest entry must be at the bottom")
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

func keyRun(m *Model, k string) { run(m, press(m, k)) }

func liveModel(t *testing.T, b Backend) *Model {
	t.Helper()
	return newModel(t, Config{Live: true, Backend: b})
}

func lastEvent(m *Model) Event { return m.events[len(m.events)-1] }

func TestWithoutABackendTheKeysSayNothingIsConnected(t *testing.T) {
	m := liveModel(t, nil)
	for _, k := range []string{"m", "p", "P", "n"} {
		keyRun(m, k)
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
	keyRun(m, "m")
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "ffuf"}}})

	if len(m.events) != 3 || m.events[1].Block != blockMap {
		t.Fatalf("entries = %+v", m.events)
	}
	block := plain(m.events[1].Lines)
	if !strings.HasPrefix(block, "map\n") || !strings.Contains(block, "22/tcp  open  ssh") {
		t.Errorf("block =\n%s", block)
	}
	keyRun(m, "m")
	if m.has(blockMap) || len(m.events) != 2 || b.maps != 1 {
		t.Errorf("the same key must take it away without asking again: %d entries, %d asks", len(m.events), b.maps)
	}
}

func TestAnOpenMapIsRefreshedInPlaceWhenTheMapChanges(t *testing.T) {
	b := &fakeBackend{mapText: "one host"}
	m := liveModel(t, b)
	keyRun(m, "m")
	m.Update(AppendMsg{Events: []Event{{Kind: KindCommand, Text: "later"}}})
	b.mapText = "two hosts"
	run(m, func() tea.Msg { _, c := m.Update(MapChangedMsg{}); return c() })

	if b.maps != 2 || len(m.events) != 2 || m.events[0].Block != blockMap || !strings.Contains(plain(m.events[0].Lines), "two hosts") {
		t.Errorf("maps=%d entries=%+v", b.maps, m.events)
	}
	keyRun(m, "m")
	if _, cmd := m.Update(MapChangedMsg{}); cmd != nil || b.maps != 2 {
		t.Error("a map that is not shown is not fetched")
	}
}

func TestMAsksOnceWhileTheMapIsOnItsWay(t *testing.T) {
	b := &fakeBackend{mapText: "m"}
	m := liveModel(t, b)
	first := press(m, "m")
	if second := press(m, "m"); second != nil {
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
	cmd := press(m, "p")
	if lastEvent(m).Turn == nil || lastEvent(m).Turn.State != turnThinking {
		t.Fatal("while the plan is on its way the stream shows that the assistant is working")
	}
	run(m, cmd)
	if len(m.events) != 1 || m.events[0].Block != blockPipeline || !strings.Contains(plain(m.events[0].Lines), "web content discovery") {
		t.Fatalf("entries = %+v", m.events)
	}
	keyRun(m, "p")
	if m.has(blockPipeline) || len(b.pipelines) != 1 {
		t.Errorf("p again takes it away and asks nothing: calls %v", b.pipelines)
	}

	keyRun(m, "p")
	b.pipeText = "a newer plan"
	keyRun(m, "P")
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
	keyRun(m, "p")
	if len(m.events) != 1 || m.events[0].Kind != KindSystem || !strings.Contains(m.events[0].Text, "the case has no scope: set one") {
		t.Errorf("entries = %+v", m.events)
	}
	keyRun(m, "m")
	if len(m.events) != 2 || m.has(blockMap) {
		t.Errorf("a map that failed to load is not shown: %+v", m.events)
	}
	b.err = nil
	b.mapText = "ok"
	keyRun(m, "m")
	if !m.has(blockMap) {
		t.Error("after a failure the key must work again")
	}
}

func TestNAnswersWithASpinnerThenTypedTextAndItsRisk(t *testing.T) {
	b := &fakeBackend{next: Suggestion{Rationale: "port 80 serves nginx", Command: "gobuster dir -u http://10.0.0.5", Risk: "medium", Confidence: "high"}}
	m := liveModel(t, b)
	cmd := press(m, "n")
	turn := lastEvent(m).Turn
	if turn == nil || turn.State != turnThinking || !strings.Contains(plain(renderEvent(lastEvent(m), 80)), "thinking") {
		t.Fatal("the answer starts as a spinner")
	}
	if again := press(m, "n"); again != nil || len(m.events) != 1 {
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
	keyRun(m, "n")
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
	keyRun(m, "n")
	if len(m.events) != 1 || m.events[0].Kind != KindSystem || !strings.Contains(m.events[0].Text, "connection refused") {
		t.Fatalf("entries = %+v", m.events)
	}
	b.err = nil
	b.next = Suggestion{Rationale: "r", Command: "c", Risk: "low", Confidence: "low"}
	keyRun(m, "n")
	if b.nexts != 2 {
		t.Error("after an error the key must work again")
	}
}

func TestTheSpinnerTimerStopsWhenNothingWaits(t *testing.T) {
	m := liveModel(t, &fakeBackend{next: Suggestion{Rationale: "r", Command: "c"}})
	cmd := press(m, "n")
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

func TestTheHelpListsTheKeysAndNotTheKindsOfEntry(t *testing.T) {
	out := plain(helpLines())
	for _, want := range []string{"n  ask for the next step", "m  the map", "p  the pipeline", "P  ask for a new pipeline", "q  quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "the stream") || strings.Contains(out, "assistant") {
		t.Errorf("the legend of entry kinds is visual noise and is gone:\n%s", out)
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
	keyRun(m, "n")
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
	pending := press(m, "n")
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
	keyRun(m, "n")
	keyRun(m, "n")
	if n := len(m.events); n != 2 {
		t.Errorf("a person who presses n twice sees both failures, got %d entries", n)
	}
}

func TestCCopiesTheLatestSuggestedCommand(t *testing.T) {
	var copied []string
	b := &fakeBackend{next: Suggestion{Rationale: "r", Command: "gobuster dir -u http://10.0.0.5", Risk: "low", Confidence: "low"}}
	m := newModel(t, Config{Live: true, Backend: b, Copy: func(s string) error { copied = append(copied, s); return nil }})

	keyRun(m, "c")
	if len(copied) != 0 || !strings.Contains(lastEvent(m).Text, "no suggested command to copy") {
		t.Fatalf("nothing to copy yet: %v, %+v", copied, lastEvent(m))
	}
	keyRun(m, "n")
	for m.typing {
		m.typeTick()
	}
	cmd := press(m, "c")
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
