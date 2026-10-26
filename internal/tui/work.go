package tui

// workFrames are what the marker of a waiting answer shows while the assistant works: mostly open,
// with a quick blink, and round again. They loop on the ticks of the spinner, workEvery to a frame.
var workFrames = []string{"◉", "◉", "◉", "◎", "—", "◎"}

// workEvery is how many ticks of the spinner a frame lasts: 2 x 110 ms, so 220 ms.
const workEvery = 2

// workFrame is the frame for a count of ticks.
func workFrame(spin int) string { return workFrames[(spin/workEvery)%len(workFrames)] }
