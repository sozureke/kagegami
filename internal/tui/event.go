package tui

import "time"

// Kind says what an entry of the stream is; each has its colour and a one-character marker, never a box.
type Kind int

// The kinds of entry in the stream (DESIGN.md, section 2.1).
const (
	KindCommand   Kind = iota // a command the person ran
	KindOutput                // what the command printed, as highlights
	KindNote                  // a note the person wrote
	KindFinding               // something that belongs in the report
	KindAssistant             // an answer of tier 2
	KindSystem                // Kagegami changed something itself
	KindBlock                 // a block opened by a key: the map, the pipeline, the help
)

// The glyphs of the lines Kagegami writes about its own actions: single width and monochrome, so
// that nothing shifts the columns.
const (
	GlyphMap      = "⌖"
	GlyphReport   = "✎"
	GlyphPipeline = "→"
)

// Event is one entry of the stream.
type Event struct {
	Kind Kind
	Time time.Time
	Text string
	// Glyph is the marker of a system line.
	Glyph string
	// Words are the parts of an output line to bring out.
	Words []string
	// Turn is the answer of the assistant, which changes while it is typed.
	Turn *Turn
	// Block names a block (map, pipeline, help), so that its key can remove it again.
	Block string
	// Lines are the lines of a block, already styled.
	Lines []string
}
