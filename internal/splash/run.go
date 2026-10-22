package splash

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

var hintOnce sync.Once

// colorHint says what to tell a person whose terminal does not show 24-bit colour, or "" if it does.
// Inside tmux it also says how to let tmux pass the colour on.
func colorHint(profile colorprofile.Profile, env []string) string {
	if profile >= colorprofile.TrueColor || profile == colorprofile.NoTTY {
		return ""
	}
	hint := "kagegami: this terminal does not report 24-bit colour, so the animation uses 256 colours. If it supports 24-bit colour, set COLORTERM=truecolor."
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "TMUX="); ok && v != "" {
			hint += "\nInside tmux, add this line to ~/.tmux.conf and restart tmux:\n    set -as terminal-features ',*:RGB'"
			break
		}
	}
	return hint
}

// Run plays the animation of the art over the whole terminal. Any key skips it. It returns when the
// animation has ended (or when it is skipped, or ctx is done); after that nothing is left running.
// A terminal that is not a terminal makes it return an error, and the caller may go on without it.
func Run(ctx context.Context, art string, cfg Config) error {
	return run(ctx, art, cfg, os.Stdin, os.Stdout, os.Stderr, os.Environ())
}

func run(ctx context.Context, art string, cfg Config, in io.Reader, out, errOut io.Writer, env []string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	profile := colorprofile.Detect(os.Stdout, env)
	if out != io.Writer(os.Stdout) {
		profile = colorprofile.TrueColor
	}
	if hint := colorHint(profile, env); hint != "" {
		hintOnce.Do(func() { fmt.Fprintln(errOut, hint) })
	}
	m := &model{art: art, cfg: cfg}
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithColorProfile(profile), tea.WithFPS(cfg.FPS)).Run()
	return err
}

type tickMsg struct{}

// model is the Bubble Tea side of the animation: a timer that steps the state, and a view.
type model struct {
	art string
	cfg Config
	// state is nil until the size of the terminal is known: the art is fitted to it first.
	state         *State
	fitted        string
	width, height int
	// seed is kept so that a resize does not change the glitches
	seed uint64
	wait int
}

func (m *model) tick() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(m.cfg.FPS), func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Init() tea.Cmd { return m.tick() }

// fit fits the art to a terminal of this size. A state that was already running is rebuilt and
// brought to the same frame, so a resize does not restart the animation.
func (m *model) fit(width, height int) {
	m.width, m.height = width, height
	cfg := m.cfg
	cfg.Seed = m.seed
	m.fitted = Fit(m.art, width, height, cfg)
	old := m.state
	m.state = NewState(m.fitted, cfg)
	m.seed = m.state.Seed()
	if old != nil {
		for range old.tick {
			m.state.Step(cfg)
		}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m, tea.Quit
	case tea.WindowSizeMsg:
		if m.state == nil || msg.Width != m.width || msg.Height != m.height {
			m.fit(msg.Width, msg.Height)
		}
	case tickMsg:
		if m.state == nil {
			// no size has come yet; after a second, go on with a common one
			if m.wait++; m.wait >= m.cfg.FPS {
				m.fit(80, 24)
			}
			return m, m.tick()
		}
		m.state.Step(m.cfg)
		if m.state.Done() {
			if !m.cfg.Loop {
				return m, tea.Quit
			}
			m.state = NewState(m.fitted, m.cfg) // a seed of 0 gives the next round its own glitches
			m.seed = m.state.Seed()
		}
		return m, m.tick()
	}
	return m, nil
}

func (m *model) View() tea.View {
	content := ""
	if m.state != nil {
		content = render(Frame(m.state, m.cfg), m.width, m.height)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// render puts the cells in the middle of a terminal of the given size; what does not fit is cut.
// Cells of one colour are written together, to keep the output short.
func render(cells [][]Cell, width, height int) string {
	h := len(cells)
	if h == 0 || width <= 0 || height <= 0 {
		return ""
	}
	w := len(cells[0])
	top, left := (height-h)/2, (width-w)/2
	var b strings.Builder
	for ty := range height {
		if ty > 0 {
			b.WriteByte('\n')
		}
		y := ty - top
		if y < 0 || y >= h {
			continue
		}
		b.WriteString(strings.Repeat(" ", max(left, 0)))
		var cur RGB
		open := false
		for tx := max(-left, 0); tx < w && tx+left < width; tx++ {
			c := cells[y][tx]
			if c.Ch == ' ' {
				if open {
					b.WriteString("\x1b[0m")
					open = false
				}
				b.WriteByte(' ')
				continue
			}
			if !open || c.RGB != cur {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
				cur, open = c.RGB, true
			}
			b.WriteRune(c.Ch)
		}
		if open {
			b.WriteString("\x1b[0m")
		}
	}
	return b.String()
}
