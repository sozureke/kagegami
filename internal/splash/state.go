package splash

import (
	"math"
	"math/rand/v2"
	"strings"
)

// dens is a scale of glyphs from empty to dense. A character appears by climbing it up to its own
// place, and dissolves by climbing down.
const dens = " .'`,:;-~\"!_^<>=+*/\\|()[]{}ilItrjfvxzcuonsabdeghkpqyw1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZ#%&$@"

// junk is what noise, pulses and decoding are made of.
const junk = "!@#$%^&*()_+-=[]{}|;:,.<>/?~0123456789"

var (
	densRunes = []rune(dens)
	junkRunes = []rune(junk)
)

// State is a point in the animation. Frame draws it, Step moves it on by one frame. The only
// randomness a frame uses comes from the seed, the frame number and the position of the cell, so a
// state with a given seed always draws the same.
type State struct {
	art  [][]rune
	w, h int
	dist [][]float64
	// start is the time, in frames, at which each cell begins to appear; a rebuilt patch resets it.
	start [][]float64
	// outAt is when each cell begins to dissolve, as a fraction of the fade-out.
	outAt [][]float64
	// dn is the place of each character on the scale.
	dn   [][]int
	maxD float64

	seed uint64
	rng  *rand.Rand
	tick int
	// t is the time of the appearance, in frames: it grows by Speed on every tick.
	t       float64
	appearT float64
	// outStart and outDur are the start and the length of the dissolve, in seconds.
	outStart, outDur float64
	outU             float64
	done             bool

	pulses []float64
	burst  struct{ left, y0, y1, dx int }
	line   struct{ y, dx int }
	flick  struct {
		left   int
		factor float64
	}
}

// NewState prepares the animation of an art. The lines are padded with spaces to the longest one;
// a space is always empty. cfg must be valid.
func NewState(art string, cfg Config) *State {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(art, "\r", ""), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	s := &State{h: len(lines), line: struct{ y, dx int }{y: -1}}
	for _, l := range lines {
		s.w = max(s.w, len([]rune(l)))
	}
	s.seed = cfg.Seed
	if s.seed == 0 {
		s.seed = rand.Uint64() //nolint:gosec // a seed for a visual effect, not a secret
	}
	s.rng = rand.New(rand.NewPCG(s.seed, s.seed^0x9e3779b97f4a7c15)) //nolint:gosec // the animation needs repeatable noise, not secrecy

	s.art = make([][]rune, s.h)
	for y, l := range lines {
		row := []rune(l)
		for len(row) < s.w {
			row = append(row, ' ')
		}
		s.art[y] = row
	}

	cx, cy := cfg.CenterX/100*float64(max(s.w-1, 0)), cfg.CenterY/100*float64(max(s.h-1, 0))
	s.dist = grid[float64](s.w, s.h)
	s.start = grid[float64](s.w, s.h)
	s.outAt = grid[float64](s.w, s.h)
	s.dn = grid[int](s.w, s.h)
	for y := range s.h {
		for x := range s.w {
			d := math.Hypot((float64(x)-cx)*cfg.CellAspect, float64(y)-cy)
			s.dist[y][x] = d
			s.maxD = math.Max(s.maxD, d)
		}
	}
	rings := float64(cfg.Rings)
	r := s.maxD / rings
	for y := range s.h {
		for x := range s.w {
			d := s.dist[y][x]
			band, frac := 0.0, 0.0
			if r > 0 {
				band = math.Min(rings-1, math.Floor(d/r))
				frac = d/r - band
			}
			s.start[y][x] = band*cfg.Pulse + frac*cfg.Pulse*0.45 + unit(s.seed, x, y, 1, 0)*cfg.Jitter
			frac01 := 0.0
			if s.maxD > 0 {
				frac01 = d / s.maxD
			}
			s.outAt[y][x] = (1-frac01)*0.55 + unit(s.seed, x, y, 2, 0)*0.05
			s.dn[y][x] = len(densRunes) - 1
			for i, g := range densRunes {
				if g == s.art[y][x] {
					s.dn[y][x] = i
					break
				}
			}
			if s.art[y][x] != ' ' {
				s.appearT = math.Max(s.appearT, s.start[y][x]+cfg.Fade)
			}
		}
	}

	// the appearance, the hold and the dissolve, in seconds, within TotalMaxSec
	s.outDur = math.Min(cfg.FadeOutSec, cfg.TotalMaxSec)
	appear := s.appearT / cfg.Speed / float64(cfg.FPS)
	s.outStart = math.Max(0, math.Min(appear+cfg.HoldSec, cfg.TotalMaxSec-s.outDur))
	s.done = s.w == 0 || s.h == 0
	return s
}

func grid[T any](w, h int) [][]T {
	g := make([][]T, h)
	for y := range g {
		g[y] = make([]T, w)
	}
	return g
}

// Size returns the width and the height of the art.
func (s *State) Size() (w, h int) { return s.w, s.h }

// Done says whether the animation has ended, with every cell dissolved.
func (s *State) Done() bool { return s.done }

// Seed returns the seed in use, which is the one that was asked for or the one that was picked.
func (s *State) Seed() uint64 { return s.seed }

// Step moves the animation on by one frame.
func (s *State) Step(cfg Config) {
	if s.done {
		return
	}
	s.tick++
	s.t += cfg.Speed
	secs := float64(s.tick) / float64(cfg.FPS)
	if secs >= s.outStart {
		s.outU = math.Min((secs-s.outStart)/s.outDur, 1)
		s.done = s.outU >= 1
	}
	if s.t >= s.appearT {
		s.stepPulses(cfg)
		s.stepGlitch(cfg)
	}
}

func (s *State) stepPulses(cfg Config) {
	grow := s.maxD * cfg.PulseSpeed / 1600 * cfg.Speed
	live := s.pulses[:0]
	for _, r := range s.pulses {
		if r += grow; r-cfg.PulseWidth/10 <= s.maxD {
			live = append(live, r)
		}
	}
	s.pulses = live
	if s.rng.Float64() < cfg.PulseFreq/100*0.03 {
		s.pulses = append(s.pulses, 0)
	}
}

func (s *State) stepGlitch(cfg Config) {
	k := cfg.Glitch / 100
	if k <= 0 {
		return
	}
	if s.burst.left > 0 {
		s.burst.left--
	} else if s.rng.Float64() < 0.03*k {
		s.burst.left = 4 + s.rng.IntN(10)
		span := 2 + s.rng.IntN(max(2, s.h/3))
		s.burst.y0 = s.rng.IntN(max(1, s.h))
		s.burst.y1 = min(s.h, s.burst.y0+span)
		s.burst.dx = (2 + s.rng.IntN(7)) * (1 - 2*s.rng.IntN(2))
	}
	s.line.y = -1
	if s.rng.Float64() < 0.06*k {
		s.line.y = s.rng.IntN(max(1, s.h))
		s.line.dx = (1 + s.rng.IntN(5)) * (1 - 2*s.rng.IntN(2))
	}
	if s.rng.Float64() < 0.015*k {
		w, h := 4+s.rng.IntN(17), 2+s.rng.IntN(5)
		x0, y0 := s.rng.IntN(max(1, s.w)), s.rng.IntN(max(1, s.h))
		for y := y0; y < min(s.h, y0+h); y++ {
			for x := x0; x < min(s.w, x0+w); x++ {
				s.start[y][x] = s.t + s.rng.Float64()*12
			}
		}
	}
	if s.flick.left > 0 {
		s.flick.left--
	} else if s.rng.Float64() < 0.04*k {
		s.flick.left = 1 + s.rng.IntN(3)
		s.flick.factor = 0.55
		if s.rng.IntN(2) == 0 {
			s.flick.factor = 1.35
		}
	}
}

// rowShift is how far a row is pushed sideways in this frame, and whether the push is a glitch.
func (s *State) rowShift(y int) (dx int, glitching bool) {
	if s.burst.left > 0 && y >= s.burst.y0 && y < s.burst.y1 {
		dx, glitching = s.burst.dx, true
	}
	if s.line.y == y {
		dx, glitching = s.line.dx, true
	}
	return dx, glitching
}

// unit is a number from 0 up to 1 that depends only on its arguments.
func unit(seed uint64, a, b, c, d int) float64 {
	return float64(mix(seed, a, b, c, d)>>11) / (1 << 53)
}

func mix(seed uint64, a, b, c, d int) uint64 {
	z := seed
	for _, v := range [4]int{a, b, c, d} {
		z += uint64(int64(v))*0x9e3779b97f4a7c15 + 0x7f4a7c15 //nolint:gosec // wrap-around is the point of a hash
		z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
		z = (z ^ z>>27) * 0x94d049bb133111eb
		z ^= z >> 31
	}
	return z
}
