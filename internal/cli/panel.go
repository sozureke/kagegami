package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sozureke/kagegami/internal/advice"
	"github.com/sozureke/kagegami/internal/agent"
	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/mapview"
	"github.com/sozureke/kagegami/internal/models"
	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/recon/ffuf"
	"github.com/sozureke/kagegami/internal/recon/gobuster"
	"github.com/sozureke/kagegami/internal/recon/nmap"
	"github.com/sozureke/kagegami/internal/recon/whatweb"
	"github.com/sozureke/kagegami/internal/splash"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/terminal"
	"github.com/sozureke/kagegami/internal/tui"
	"github.com/sozureke/kagegami/pkg/tmux"
)

// newPanelCmd is the panel that `kagegami watch` opens in the new pane. It is not meant to be
// typed: it does the watching and draws the stream.
func newPanelCmd(info BuildInfo) *cobra.Command {
	var paneFlag, caseName, noticeFlag string
	var noAnim bool
	cmd := &cobra.Command{
		Use:    "panel",
		Short:  help.Commands["panel"].Short,
		Long:   help.Commands["panel"].Long,
		Args:   noArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
				return errors.New("the panel needs a terminal")
			}
			pane, err := tmux.ParsePaneID(paneFlag)
			if err != nil {
				return &usageError{err: err}
			}
			return runPanel(cmd, info, pane, caseName, noAnim, noticeFlag)
		},
	}
	cmd.Flags().StringVar(&paneFlag, "pane", "", "the tmux pane to watch, such as %3")
	cmd.Flags().StringVar(&caseName, "case", "", "the case to record into")
	cmd.Flags().BoolVar(&noAnim, "no-anim", false, "skip the opening animation")
	cmd.Flags().StringVar(&noticeFlag, "notice", "", "a line to start the stream with, such as what was set up")
	return cmd
}

func runPanel(cmd *cobra.Command, info BuildInfo, pane tmux.PaneID, caseName string, noAnim bool, setupNotice string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	cfg, err := loadConfigOrDefault()
	if err != nil {
		return err
	}
	env, err := config.OSEnv()
	if err != nil {
		return err
	}
	paths, err := config.NewPaths(env)
	if err != nil {
		return err
	}
	name, db, err := resolveCase(paths, caseName)
	if err != nil {
		return err
	}

	// The capture rests on one property of this tmux (ADR-0007); a tmux that does not have it
	// would record nothing and say nothing, so find out before anything is drawn.
	if err := tmux.VerifyCapture(ctx, tmux.CheckOptions{}); err != nil {
		return fmt.Errorf("this tmux does not pass the markers of the capture on, so Kagegami cannot watch with it: %w", err)
	}

	st, err := store.Open(ctx, db)
	if err != nil {
		return fmt.Errorf("open the case: %w", err)
	}
	defer func() { _ = st.Close() }()
	rawDir := filepath.Join(filepath.Dir(db), "raw")

	set, err := buildModelsForAdvice(cmd, st)
	if err != nil {
		return err
	}
	tier1 := &agent.Tier1{
		Provider: set.Tier1,
		Options:  agent.DefaultTier1Options(),
		InScope:  func(ip string) bool { return currentScope(context.Background(), st).Contains(ip) },
	}

	if cfg.Splash.Enabled && !noAnim && os.Getenv("KAGEGAMI_NO_ANIM") == "" {
		_ = splash.Run(ctx, splash.Art, cfg.Splash) // for show: a terminal that cannot play it goes on without
	}

	filter := terminal.NewFilter(cfg.Capture.Ignore)
	backend := &panelBackend{st: st, name: name, rawDir: rawDir, set: set, filter: filter}
	notice := setupNotice
	if set.Remote {
		notice = strings.TrimPrefix(notice+"\n"+remoteNotice, "\n")
	}
	model := tui.New(tui.Config{
		Version: "kagegami " + info.Version, Live: true, Backend: backend, Context: ctx,
		Auto: cfg.Panel.AutoSuggest, Copy: copyToClipboard, Notice: notice,
		SessionName: name, Welcome: panelCard(ctx, st, info.Version, name, cfg.Model),
	})
	prog := tea.NewProgram(model, tea.WithContext(ctx))

	ag, err := agent.New(agent.Config{
		Store:    st,
		RawDir:   rawDir,
		Registry: recon.NewRegistry(nmap.New(), ffuf.New(), gobuster.New(), whatweb.New()),
		Filter:   filter,
		Tier1:    tier1,
		Scope:    func(ctx context.Context) agent.ScopeCheck { return currentScope(ctx, st) },
		OnUpdate: func(u agent.Update) {
			if evs := updateEvents(u); len(evs) > 0 {
				prog.Send(tui.AppendMsg{Events: evs})
			}
			if a := u.Added; a.Hosts+a.Services+a.WebPaths+a.Findings > 0 { // notes alone do not move the map
				prog.Send(tui.MapChangedMsg{})
			}
			if u.Handled && u.Skipped == terminal.ReasonNone && !ownCommand(u.Cmd) {
				prog.Send(tui.CommandDoneMsg{}) // the agent looks at what the command showed
			}
		},
	})
	if err != nil {
		return err
	}
	client := tmux.New(tmux.ExecRunner{}, os.Getenv)
	w, err := terminal.Watch(ctx, terminal.WatchConfig{Pane: pane, RunDir: paths.RunDir(), Pipe: client, Recorder: ag.Recorder()})
	if err != nil {
		return fmt.Errorf("watch pane %s: %w", pane, err)
	}

	wctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	forwarded := make(chan terminal.Event, 64)
	wg.Add(2)
	go func() { // the agent reads what the capture saw, and the panel shows each command
		defer wg.Done()
		_ = ag.Run(wctx, forwarded)
	}()
	go func() {
		defer wg.Done()
		defer close(forwarded)
		silent := false
		for e := range w.Events() { // read to the end: the capture waits for its reader
			if wctx.Err() != nil {
				continue
			}
			if evs := captureEvents(e, &silent); len(evs) > 0 {
				prog.Send(tui.AppendMsg{Events: evs})
			}
			select {
			case forwarded <- e:
			case <-wctx.Done():
			}
		}
	}()
	wg.Add(1)
	go func() { // the outline of the panel shows whether this pane has the keyboard
		defer wg.Done()
		watchFocus(wctx, client, prog, focusPoll)
	}()
	go func() {
		if err := w.Wait(); err != nil && wctx.Err() == nil {
			prog.Send(tui.EndedMsg{Reason: watchEndReason(err)})
		}
	}()

	_, runErr := prog.Run()
	cancel()
	closeErr := w.Close() // stops pipe-pane and removes the nonce and the pipe
	wg.Wait()
	return errors.Join(runErr, closeErr)
}

// focusPoll is how often the panel asks tmux whether it has the keyboard.
const focusPoll = 400 * time.Millisecond

type paneFocus interface {
	CurrentPane() (tmux.PaneID, error)
	PaneFocused(ctx context.Context, pane tmux.PaneID) (bool, error)
}

type sender interface{ Send(tea.Msg) }

// watchFocus tells the panel when its own pane gains or loses the keyboard. tmux passes focus to
// a program only if focus-events is on, which cannot be counted on, so the panel asks, and speaks
// only when the answer changes.
func watchFocus(ctx context.Context, c paneFocus, to sender, every time.Duration) {
	own, err := c.CurrentPane()
	if err != nil {
		return
	}
	var last, known bool
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if f, err := c.PaneFocused(ctx, own); err == nil && (!known || f != last) {
			last, known = f, true
			to.Send(tui.FocusMsg{Focused: f})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// currentScope reads the scope of the case as it is now, so that a change made in another pane (kagegami
// case set) is seen at once. A case that has none yet has an empty scope.
func currentScope(ctx context.Context, st *store.Store) agent.ScopeCheck {
	e, err := engagement.Get(ctx, st)
	if err != nil {
		return engagement.Scope(store.Engagement{})
	}
	return engagement.Scope(e)
}

// panelBackend is what the keys of the panel show: the map and the pipeline of the case, and the
// answers of tier 2.
type panelBackend struct {
	st     *store.Store
	name   string // the case, for what /target and the first message set
	rawDir string
	set    *models.Set
	filter *terminal.Filter
}

func (b *panelBackend) tier2() *agent.Tier2 {
	opts := agent.DefaultTier2Options()
	// what the tester ran in order to look around (cd, ls, clear) and Kagegami's own commands are
	// not what the model needs to see
	opts.Context.Skip = func(cmd string, exit int, size int64) bool {
		return ownCommand(cmd) || b.filter != nil && b.filter.Judge(cmd, terminal.End{ExitCode: exit}, size) != terminal.ReasonNone
	}
	return &agent.Tier2{Provider: b.set.Tier2, Store: b.st, RawDir: b.rawDir, Options: opts}
}

// panelCard is the welcome the panel opens with. The tips depend on where the case stands: with no
// scope the first thing to do is to name the target, and with one it is to run recon.
func panelCard(ctx context.Context, st *store.Store, version, name string, m config.Model) *tui.Card {
	c := &tui.Card{Title: "kagegami " + version, Facts: append([]string{"case " + name}, modelFacts(m)...)}
	if dir, err := os.Getwd(); err == nil {
		c.Facts = append(c.Facts, tildePath(dir))
	}

	e, err := engagement.Get(ctx, st)
	if err != nil || engagement.Scope(e).IsEmpty() {
		c.Heading = "Welcome!"
		c.Tips = []string{
			"What are we testing? Type the target: an address, a network or a range, like 10.10.10.5.",
			"Or describe the task with the address in it: get the user flag on 10.10.10.5",
			"/ lists the commands",
		}
		return c
	}
	c.Heading = "Welcome back!"
	c.Facts = append(c.Facts, "target "+engagement.Scope(e).String())
	c.Tips = []string{
		"Run your recon in the other pane: I read every command and say what is next.",
		"Ask me anything below, or / for the commands.",
	}
	return c
}

// modelFacts name the two models, one to a line, without the provider in front: "tier 1 · gpt-4o-mini".
func modelFacts(m config.Model) []string {
	short := func(spec string) string {
		if i := strings.LastIndex(spec, "/"); i >= 0 {
			return spec[i+1:]
		}
		return spec
	}
	t1, t2 := short(m.Tier1), short(m.Tier2Spec())
	if m.Tier2 == "" || m.Tier2 == m.Tier1 {
		return []string{"tier 1 · " + t1, "tier 2 · same model"}
	}
	return []string{"tier 1 · " + t1, "tier 2 · " + t2}
}

// tildePath writes a path under the home directory as ~/...
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return p
}

// ownCommand says whether a command is one of Kagegami's own (kagegami map, kagegami next ...): it is
// the tester looking at the tool, not at the target.
func ownCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	return len(fields) > 0 && filepath.Base(fields[0]) == "kagegami"
}

const (
	askNextRequest = "Suggest the next reconnaissance step."
	// askAfterCommand is what the panel asks when a command has just finished: the output is in
	// <recent>, and the answer starts with what it shows.
	askAfterCommand = "The last command in <recent> has just finished. Say what its output shows for the target, then give the next step."
)

func (b *panelBackend) Map(ctx context.Context) (string, error) {
	view, err := mapview.Load(ctx, b.st)
	if err != nil {
		return "", fmt.Errorf("read the map: %w", err)
	}
	var buf bytes.Buffer
	if err := mapview.Text(&buf, view, mapview.Options{}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (b *panelBackend) Pipeline(ctx context.Context, fresh bool) (string, error) {
	var buf bytes.Buffer
	if !fresh {
		runs, err := b.st.Repo().ListPipelineRuns(ctx)
		if err != nil {
			return "", err
		}
		if len(runs) > 0 {
			latest := runs[0]
			for _, r := range runs {
				if r.ID > latest.ID {
					latest = r
				}
			}
			var steps []advice.Step
			if err := json.Unmarshal([]byte(latest.Steps), &steps); err != nil {
				return "", fmt.Errorf("the stored plan is not readable: %w", err)
			}
			printPipeline(&buf, advice.Pipeline{Summary: latest.Summary, Steps: steps}, nil)
			return buf.String(), nil
		}
	}
	res, err := b.tier2().Plan(ctx, "Lay out a pipeline for this target.")
	if err != nil {
		return "", adviceError(err)
	}
	printPipeline(&buf, res.Pipeline, res.Dropped)
	return buf.String(), nil
}

func (b *panelBackend) Next(ctx context.Context, auto bool) (tui.Suggestion, error) {
	request := askNextRequest
	if auto {
		request = askAfterCommand
	}
	res, err := b.tier2().Next(ctx, request)
	if err != nil {
		return tui.Suggestion{}, adviceError(err)
	}
	return suggestion(res.Suggestion), nil
}

func (b *panelBackend) Ask(ctx context.Context, question string) (tui.Suggestion, error) {
	res, err := b.tier2().Next(ctx, "The tester asks: "+oneLineCmd(question)+"\nAnswer their question from the map, the journal and <recent>. If a command would help them, give it; if not, leave the command empty and answer in the rationale.")
	if err != nil {
		return tui.Suggestion{}, adviceError(err)
	}
	return suggestion(res.Suggestion), nil
}

func suggestion(s advice.Suggestion) tui.Suggestion {
	return tui.Suggestion{Rationale: s.Rationale, Command: s.Command, Risk: s.Risk, Confidence: s.Confidence, NeedsRecon: s.NeedsRecon}
}

// adviceError says in a sentence what a person can do about a failure of tier 2.
func adviceError(err error) error {
	switch {
	case errors.Is(err, advice.ErrNoScope):
		return errors.New("the case has no scope: set one with  kagegami case set --scope ...  in the other pane")
	case errors.Is(err, agent.ErrUnusable):
		return fmt.Errorf("the model gave no answer that could be used: %w", err)
	}
	return err
}

// captureEvents turns what the capture saw into entries of the stream. Only the warning about a
// missing hook comes from here, and it is shown once: a command is shown once the agent has judged
// it (see updateEvents), so that the ones that mean nothing never reach the person.
func captureEvents(e terminal.Event, silentShown *bool) []tui.Event {
	if e.Kind != terminal.HookSilent || *silentShown {
		return nil
	}
	*silentShown = true
	return []tui.Event{{Kind: tui.KindSystem, Time: e.At, Glyph: tui.GlyphPipeline, Text: "output is going by but no command is marked: load the shell hook with  eval \"$(kagegami hook zsh)\"  in this pane's shell"}}
}

// updateEvents turns what became of a command into entries of the stream. A command that was
// dropped as meaningless (cd, ls, clear, an empty line) is recorded and not shown. One that will be
// read is shown at once, when the agent has accepted it, and what came of the reading follows later:
// a line when it changed the map, and one when reading it failed.
func updateEvents(u agent.Update) []tui.Event {
	if u.Skipped != terminal.ReasonNone {
		return nil
	}
	var out []tui.Event
	if !u.Handled { // accepted, or one that could not even be recorded
		text := oneLineCmd(u.Cmd)
		if u.Exit != 0 {
			text += fmt.Sprintf("  (exit %d)", u.Exit)
		}
		out = append(out, tui.Event{Kind: tui.KindCommand, Text: text})
	}
	if u.Err != nil {
		out = append(out, tui.Event{Kind: tui.KindSystem, Glyph: tui.GlyphPipeline, Text: "could not read " + oneLineCmd(u.Cmd) + ": " + u.Err.Error()})
	}
	if u.Added.Total() > 0 {
		out = append(out, tui.Event{Kind: tui.KindSystem, Glyph: tui.GlyphMap, Text: describeAdded(u)})
	}
	return out
}

func describeAdded(u agent.Update) string {
	var parts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, "1 "+one)
		case n > 1:
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	add(u.Added.Hosts, "host", "hosts")
	add(u.Added.Services, "service", "services")
	add(u.Added.WebPaths, "path", "paths")
	add(u.Added.Findings, "finding", "findings")
	add(u.Added.Notes, "note", "notes")
	text := "map: +" + strings.Join(parts, ", +")
	if u.By != "" {
		text += ", from " + strings.TrimPrefix(u.By, "parser ")
	}
	return text
}

// oneLineCmd shows a command on one line, however it was typed.
func oneLineCmd(c string) string { return strings.Join(strings.Fields(c), " ") }

func watchEndReason(err error) string {
	switch {
	case errors.Is(err, terminal.ErrPaneGone):
		return "the watched pane was closed"
	case errors.Is(err, context.Canceled):
		return "stopped"
	}
	return err.Error()
}

// clipboardTools are the programs that put text on the clipboard of the system, in the order they
// are tried: macOS, Wayland, then X.
var clipboardTools = [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}

// copyToClipboard puts text on the clipboard of this machine. The text goes to the program's input,
// never through a shell. The panel runs on the machine whose clipboard is meant; over ssh the
// terminal's own clipboard (OSC 52, which the panel also asks for) is what remains.
func copyToClipboard(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // a stuck tool must not hold the panel
	defer cancel()
	for _, tool := range clipboardTools {
		path, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}
		c := exec.CommandContext(ctx, path, tool[1:]...) //nolint:gosec // one of the fixed tools above, found in PATH
		c.Stdin = strings.NewReader(text)
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s: %w", tool[0], err)
		}
		return nil
	}
	return errors.New("no clipboard tool found (pbcopy, wl-copy, xclip or xsel)")
}
