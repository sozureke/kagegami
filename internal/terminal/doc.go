// Package terminal turns the raw stream of a pane into commands and their output.
//
// Parser cuts the stream at the markers that the shell hook prints (ADR-0006, ADR-0008). It
// accepts only markers that carry the capture nonce; everything else, including look-alikes in
// the output of a command, is ordinary output. The result does not depend on how the stream
// is split into Write calls.
package terminal
