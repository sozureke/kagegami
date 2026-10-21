package tmux

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrNotInTmux means the program is not running inside a tmux pane.
	ErrNotInTmux = errors.New("not running inside tmux")
	// ErrInvalid is returned for an argument tmux would misread, such as a pane id that is
	// not of the form %N.
	ErrInvalid = errors.New("invalid tmux argument")
	// ErrNotOwned is returned when asked to close a pane this program did not create.
	ErrNotOwned = errors.New("the pane was not created by this program")
)

// PaneID identifies a pane, for example %3. A PaneID made by ParsePaneID is safe to pass
// on to tmux as a target.
type PaneID string

var paneIDRE = regexp.MustCompile(`^%[0-9]+$`)

// ParsePaneID checks that s looks like a pane id.
func ParsePaneID(s string) (PaneID, error) {
	if !paneIDRE.MatchString(s) {
		return "", fmt.Errorf("%w: pane id %q (expected %%N)", ErrInvalid, s)
	}
	return PaneID(s), nil
}

// KeySender types text into a pane. It is the one operation that writes to a pane the
// user works in, while the product is read-only by default, so it is kept apart: only
// tests and the future executor (execution mode approve-run) may use it. A test enforces
// this, as it does for secret.String.Reveal.
type KeySender interface {
	SendText(ctx context.Context, pane PaneID, text string, enter bool) error
}

// Client runs tmux commands. Pane arguments are validated, so nothing user-controlled
// reaches tmux as a target.
type Client struct {
	r      Runner
	getenv func(string) string

	mu    sync.Mutex
	owned map[PaneID]struct{} // panes created by SplitWindow in this process
}

var _ KeySender = (*Client)(nil)

// New returns a Client that runs commands through r and reads TMUX and TMUX_PANE through
// getenv.
func New(r Runner, getenv func(string) string) *Client {
	return &Client{r: r, getenv: getenv, owned: map[PaneID]struct{}{}}
}

// Version returns the version of the tmux that runs the commands.
func (c *Client) Version(ctx context.Context) (Version, error) {
	out, err := c.r.Run(ctx, "-V")
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(out)
}

// InsideTmux reports whether the program runs in a tmux pane. It reads the environment
// only and does not start tmux.
func (c *Client) InsideTmux() bool {
	return c.getenv("TMUX") != "" && c.getenv("TMUX_PANE") != ""
}

// CurrentPane returns the pane the program runs in.
func (c *Client) CurrentPane() (PaneID, error) {
	if !c.InsideTmux() {
		return "", ErrNotInTmux
	}
	return ParsePaneID(c.getenv("TMUX_PANE"))
}

// SplitOptions describe a new pane.
type SplitOptions struct {
	Target     PaneID // the pane to split
	Horizontal bool   // side by side (-h) instead of one above the other
	Percent    int    // size of the new pane in percent of the target; 0 means tmux's default
	Command    string // run by tmux through a shell; empty starts the default shell
	KeepFocus  bool   // do not switch to the new pane (-d)
}

// SplitWindow splits a pane and returns the id of the new one. The new pane is remembered
// as created by this program, which is what allows KillPane to close it later.
func (c *Client) SplitWindow(ctx context.Context, o SplitOptions) (PaneID, error) {
	if _, err := ParsePaneID(string(o.Target)); err != nil {
		return "", err
	}
	if o.Percent < 0 || o.Percent > 99 {
		return "", fmt.Errorf("%w: percent %d (expected 1-99, or 0 for the default)", ErrInvalid, o.Percent)
	}

	args := []string{"split-window", "-P", "-F", "#{pane_id}", "-t", string(o.Target)}
	if o.Horizontal {
		args = append(args, "-h")
	}
	if o.KeepFocus {
		args = append(args, "-d")
	}
	if o.Percent > 0 {
		args = append(args, "-l", strconv.Itoa(o.Percent)+"%")
	}
	if o.Command != "" {
		args = append(args, o.Command)
	}

	out, err := c.r.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	id, err := ParsePaneID(out)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.owned[id] = struct{}{}
	c.mu.Unlock()
	return id, nil
}

// KillPane closes a pane that this program created with SplitWindow. It refuses any other
// pane, and in particular the pane the program itself runs in, so a bug cannot close the
// user's working pane. A pane that is already gone is not an error.
func (c *Client) KillPane(ctx context.Context, pane PaneID) error {
	if _, err := ParsePaneID(string(pane)); err != nil {
		return err
	}
	if cur, err := c.CurrentPane(); err == nil && cur == pane {
		return fmt.Errorf("%w: %s is the pane this program runs in", ErrNotOwned, pane)
	}
	c.mu.Lock()
	_, ok := c.owned[pane]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotOwned, pane)
	}

	_, err := c.r.Run(ctx, "kill-pane", "-t", string(pane))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	c.mu.Lock()
	delete(c.owned, pane)
	c.mu.Unlock()
	return nil
}

// PipePane sends everything the pane prints to the shell command, replacing a pipe that is
// already open. The -o flag is deliberately not used: it toggles, so a second call would
// close the pipe instead of keeping it.
func (c *Client) PipePane(ctx context.Context, pane PaneID, shellCommand string) error {
	if _, err := ParsePaneID(string(pane)); err != nil {
		return err
	}
	if strings.TrimSpace(shellCommand) == "" {
		return fmt.Errorf("%w: an empty command would close the pipe, use StopPipe", ErrInvalid)
	}
	_, err := c.r.Run(ctx, "pipe-pane", "-t", string(pane), shellCommand)
	return err
}

// StopPipe closes the pipe of a pane, if there is one.
func (c *Client) StopPipe(ctx context.Context, pane PaneID) error {
	if _, err := ParsePaneID(string(pane)); err != nil {
		return err
	}
	_, err := c.r.Run(ctx, "pipe-pane", "-t", string(pane))
	return err
}

// Piped reports whether a pipe is open on the pane.
func (c *Client) Piped(ctx context.Context, pane PaneID) (bool, error) {
	out, err := c.paneFormat(ctx, pane, "#{pane_pipe}")
	return out == "1", err
}

// PaneExists reports whether the pane exists. It uses list-panes: display-message does not
// fail for a pane that is not there, it prints an empty line.
func (c *Client) PaneExists(ctx context.Context, pane PaneID) (bool, error) {
	if _, err := ParsePaneID(string(pane)); err != nil {
		return false, err
	}
	_, err := c.r.Run(ctx, "list-panes", "-t", string(pane), "-F", "#{pane_id}")
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// PaneSize returns the width and height of the pane in cells.
func (c *Client) PaneSize(ctx context.Context, pane PaneID) (width, height int, err error) {
	out, err := c.paneFormat(ctx, pane, "#{pane_width} #{pane_height}")
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscanf(out, "%d %d", &width, &height); err != nil {
		return 0, 0, fmt.Errorf("cannot read the pane size from %q: %w", out, err)
	}
	return width, height, nil
}

// paneFormat expands a constant tmux format for a pane that must exist.
func (c *Client) paneFormat(ctx context.Context, pane PaneID, format string) (string, error) {
	ok, err := c.PaneExists(ctx, pane)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: pane %s", ErrNotFound, pane)
	}
	return c.r.Run(ctx, "display-message", "-p", "-t", string(pane), format)
}

// CaptureOptions select what CapturePane returns.
type CaptureOptions struct {
	Escapes bool // keep the escape sequences of colors and attributes (-e)
	Join    bool // join wrapped lines (-J)
	Start   int  // first line; negative counts into the scrollback, 0 is the top of the screen
	End     int  // last line; 0 means the bottom of the screen
}

// CapturePane returns the visible text of the pane.
func (c *Client) CapturePane(ctx context.Context, pane PaneID, o CaptureOptions) (string, error) {
	if _, err := ParsePaneID(string(pane)); err != nil {
		return "", err
	}
	args := []string{"capture-pane", "-p", "-t", string(pane)}
	if o.Escapes {
		args = append(args, "-e")
	}
	if o.Join {
		args = append(args, "-J")
	}
	if o.Start != 0 {
		args = append(args, "-S", strconv.Itoa(o.Start))
	}
	if o.End != 0 {
		args = append(args, "-E", strconv.Itoa(o.End))
	}
	return c.r.Run(ctx, args...)
}

// SendText types text into the pane literally, as if the user had typed it; nothing in it
// is read as a key name. With enter set, Enter is pressed afterwards. See KeySender.
func (c *Client) SendText(ctx context.Context, pane PaneID, text string, enter bool) error {
	if _, err := ParsePaneID(string(pane)); err != nil {
		return err
	}
	if _, err := c.r.Run(ctx, "send-keys", "-t", string(pane), "-l", "--", text); err != nil {
		return err
	}
	if enter {
		_, err := c.r.Run(ctx, "send-keys", "-t", string(pane), "Enter")
		return err
	}
	return nil
}

// ShellQuote quotes s for a command string that tmux runs through a shell (the Command of
// SplitWindow, the shell command of PipePane).
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
