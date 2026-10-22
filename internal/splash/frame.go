package splash

import (
	"math"
	"strings"
)

// Cell is one character of a frame with its colour. A space has no colour.
type Cell struct {
	Ch rune
	RGB
}

var (
	ghostRed  = RGB{255, 40, 60}
	ghostCyan = RGB{40, 230, 255}
)

// Frame draws the state: one Cell for each cell of the art, row by row. It does not touch the
// state or the terminal, and a state with a given seed always draws the same frame.
func Frame(s *State, cfg Config) [][]Cell {
	out := grid[Cell](s.w, s.h)
	for y := range out {
		for x := range out[y] {
			out[y][x].Ch = ' '
		}
	}
	if s.w == 0 || s.h == 0 {
		return out
	}
	base, err := ParseColor(cfg.Color)
	if err != nil {
		base = presets[ColorChrome]
	}
	chrome := strings.EqualFold(strings.TrimSpace(cfg.Color), ColorChrome)
	k := cfg.Glitch / 100

	// brightness of each cell, kept apart from the colour until the end
	bright := grid[float64](s.w, s.h)
	glitchRow := make([]bool, s.h)

	for y := range s.h {
		dx, g := s.rowShift(y)
		glitchRow[y] = g
		for x := range s.w {
			sx := x - dx
			if sx < 0 || sx >= s.w {
				continue
			}
			out[y][x].Ch, bright[y][x] = s.glyph(cfg, sx, y)
		}
	}

	if k > 0 {
		n := int(math.Ceil(float64(s.w*s.h) * (0.002 + 0.01*k)))
		for i := range n {
			x := int(unit(s.seed, s.tick, i, 3, 0) * float64(s.w))
			y := int(unit(s.seed, s.tick, i, 4, 0) * float64(s.h))
			if out[y][x].Ch != ' ' {
				out[y][x].Ch, bright[y][x] = pick(junkRunes, s.seed, s.tick, x, y, 5), 1
			}
		}
	}

	if cfg.RGBSplit > 0 {
		s.split(out, bright, glitchRow, cfg.RGBSplit/100)
	}

	flicker := 1.0
	if s.flick.left > 0 {
		flicker = s.flick.factor
	}
	for y := range s.h {
		for x := range s.w {
			c := &out[y][x]
			if c.Ch == ' ' {
				continue
			}
			f := bright[y][x] * flicker
			if cfg.Scanlines && y%2 == 1 {
				f *= 0.7
			}
			if cfg.Vignette {
				f *= s.vignette(cfg, x, y)
			}
			tint := base
			if chrome {
				tint = chromeAt(float64(y) / float64(max(s.h-1, 1)))
			}
			if c.RGB == ghostRed || c.RGB == ghostCyan {
				tint = c.RGB
			}
			c.RGB = RGB{scale(tint.R, f), scale(tint.G, f), scale(tint.B, f)}
		}
	}
	return out
}

// glyph is what a cell of the art shows: the character, and how bright it is, from 0 to 1.
func (s *State) glyph(cfg Config, x, y int) (rune, float64) {
	ch := s.art[y][x]
	if ch == ' ' {
		return ' ', 0
	}
	dn := s.dn[y][x]

	if s.outU > 0 {
		q := (s.outU - s.outAt[y][x]) / 0.38
		if q >= 1 {
			return ' ', 0
		}
		if q > 0 {
			i := min(max(int(math.Floor((1-q)*float64(dn))), 0), dn)
			return densRunes[i], 1 - q
		}
	}

	p := (s.t - s.start[y][x]) / cfg.Fade
	if p < 1 {
		if cfg.Mode == ModeDecode {
			return pick(junkRunes, s.seed, s.tick, x, y, 6), 0.3 + 0.7*math.Max(p, 0)
		}
		if p <= 0 {
			return ' ', 0
		}
		i := int(math.Floor(p*float64(dn))) + int(mix(s.seed, s.tick, x, y, 7)%3) - 1
		return densRunes[min(max(i, 0), dn)], 0.25 + 0.75*p
	}

	for _, r := range s.pulses {
		if math.Abs(s.dist[y][x]-r) < cfg.PulseWidth/10 && unit(s.seed, s.tick, x, y, 8) < 0.55 {
			return pick(junkRunes, s.seed, s.tick, x, y, 9), 1
		}
	}
	return ch, 1
}

// split gives a glitching row a red ghost to the left of a character and a cyan one to its right.
func (s *State) split(out [][]Cell, bright [][]float64, glitchRow []bool, chance float64) {
	for y := range s.h {
		if !glitchRow[y] {
			continue
		}
		row := append([]Cell(nil), out[y]...)
		for x := range s.w {
			if row[x].Ch != ' ' || unit(s.seed, s.tick, x, y, 10) >= chance {
				continue
			}
			switch {
			case x+1 < s.w && row[x+1].Ch != ' ':
				out[y][x] = Cell{Ch: row[x+1].Ch, RGB: ghostRed}
				bright[y][x] = 0.8 * bright[y][x+1]
			case x > 0 && row[x-1].Ch != ' ':
				out[y][x] = Cell{Ch: row[x-1].Ch, RGB: ghostCyan}
				bright[y][x] = 0.8 * bright[y][x-1]
			}
		}
	}
}

// vignette is 1 in the middle of the art and falls towards its edges.
func (s *State) vignette(cfg Config, x, y int) float64 {
	half := math.Hypot(float64(s.w-1)/2*cfg.CellAspect, float64(s.h-1)/2)
	if half == 0 {
		return 1
	}
	d := math.Hypot((float64(x)-float64(s.w-1)/2)*cfg.CellAspect, float64(y)-float64(s.h-1)/2) / half
	return 1 - 0.6*d*d
}

func pick(set []rune, seed uint64, a, b, c, salt int) rune {
	return set[mix(seed, a, b, c, salt)%uint64(len(set))]
}

func scale(c uint8, f float64) uint8 { return toByte(float64(c) * f) }

func toByte(v float64) uint8 {
	return uint8(math.Min(255, math.Max(0, math.Round(v)))) //nolint:gosec // clamped to 0..255 just above
}
