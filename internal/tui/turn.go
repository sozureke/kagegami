package tui

import "time"

// An assistant turn is a braille spinner while the model works, then the rationale typed out, then
// the command typed out under it. These are the only two animations of the stream, and each runs
// only while it is needed (DESIGN.md, section 4).
const (
	spinTick = 110 * time.Millisecond
	typeTick = 17 * time.Millisecond
)

// spinCycle is how many ticks pass before the marker of a waiting answer is back at its first frame.
const spinCycle = workEvery * 6

type turnState int

const (
	turnThinking turnState = iota
	turnRationale
	turnCommand
	turnDone
)

// Turn is the state of one answer of the assistant.
type Turn struct {
	State     turnState
	Spin      int
	Rationale []rune
	Command   []rune
	// Meta is a dim line under a finished answer, such as its risk.
	Meta  string
	shown int
}

// NewTurn starts a turn that is waiting for the model.
func NewTurn() *Turn { return &Turn{} }

// Answer gives the turn its text; typing starts.
func (t *Turn) Answer(rationale, command string) {
	t.Rationale, t.Command = []rune(rationale), []rune(command)
	t.State, t.shown = turnRationale, 0
}

// Busy says whether the turn still needs a timer.
func (t *Turn) Busy() bool { return t.State != turnDone }

// Spun moves the spinner on one frame.
func (t *Turn) Spun() { t.Spin = (t.Spin + 1) % spinCycle }

// Typed adds a character, moving on from the rationale to the command and then to the end.
func (t *Turn) Typed() {
	switch t.State {
	case turnRationale:
		t.shown++
		if t.shown >= len(t.Rationale) {
			t.State, t.shown = turnCommand, 0
		}
	case turnCommand:
		t.shown++
		if t.shown >= len(t.Command) {
			t.State = turnDone
		}
	}
}

// Shown returns what has been typed of the rationale and of the command.
func (t *Turn) Shown() (rationale, command string) {
	switch t.State {
	case turnThinking:
		return "", ""
	case turnRationale:
		return string(t.Rationale[:t.shown]), ""
	case turnCommand:
		return string(t.Rationale), string(t.Command[:t.shown])
	}
	return string(t.Rationale), string(t.Command)
}

// Ticks is how long the timer of the turn should wait before the next call of Spun or Typed.
func (t *Turn) Ticks() time.Duration {
	if t.State == turnThinking {
		return spinTick
	}
	return typeTick
}
