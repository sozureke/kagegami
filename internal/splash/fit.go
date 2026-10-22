package splash

import (
	"math"
	"slices"
	"strings"
)

// minCover is the share of a cell of the shrunk art that must be covered by ink for it to show a
// glyph. Below it the cell is empty: a block that is mostly empty stays empty, so the shrunk art
// keeps about the density of the original instead of growing darker.
const minCover = 0.4

// Fit returns the art as it should be drawn in a terminal of the given size. The empty margins
// around the drawing are cut first, which changes nothing in the drawing itself. Then it is shrunk
// by cfg.Scale and, if cfg.Fit is set, shrunk further only as far as the terminal needs (keeping a
// column free on each side). Art is never enlarged, and the lines come back padded to one width.
func Fit(art string, width, height int, cfg Config) string {
	rows := trim(padded(art))
	if len(rows) == 0 {
		return ""
	}
	h, w := len(rows), len(rows[0])
	f := math.Min(cfg.Scale, 1)
	if f <= 0 {
		f = 1
	}
	if cfg.Fit && width > 0 && height > 0 {
		f = math.Min(f, math.Min(float64(max(width-2, 1))/float64(w), float64(max(height, 1))/float64(h)))
	}
	if f >= 1 {
		return join(rows)
	}
	return join(shrink(rows, f))
}

func padded(art string) [][]rune {
	art = strings.TrimRight(strings.ReplaceAll(art, "\r", ""), "\n")
	if strings.TrimSpace(art) == "" {
		return nil
	}
	lines := strings.Split(art, "\n")
	w := 0
	for _, l := range lines {
		w = max(w, len([]rune(l)))
	}
	rows := make([][]rune, len(lines))
	for y, l := range lines {
		r := []rune(l)
		for len(r) < w {
			r = append(r, ' ')
		}
		rows[y] = r
	}
	return rows
}

// trim cuts the empty rows above and below the drawing and the empty columns left and right of it.
func trim(rows [][]rune) [][]rune {
	top, bottom, left, right := len(rows), -1, -1, -1
	for y, r := range rows {
		for x, ch := range r {
			if ch == ' ' {
				continue
			}
			top, bottom = min(top, y), max(bottom, y)
			if left < 0 || x < left {
				left = x
			}
			right = max(right, x)
		}
	}
	if bottom < 0 {
		return nil
	}
	out := make([][]rune, 0, bottom-top+1)
	for _, r := range rows[top : bottom+1] {
		out = append(out, r[left:right+1])
	}
	return out
}

func join(rows [][]rune) string {
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = string(r)
	}
	return strings.Join(lines, "\n")
}

// shrink draws the art smaller with only the glyphs it already uses, so that its texture stays the
// same. Every cell of the result covers a rectangle of the art, fractions of cells included, so the
// drawing is not bent by uneven blocks. A cell shows a glyph when enough of its rectangle is ink, and
// the glyph is the one of the art whose weight is closest to the mean weight of that ink.
func shrink(rows [][]rune, f float64) [][]rune {
	h, w := len(rows), len(rows[0])
	th, tw := max(1, int(math.Round(float64(h)*f))), max(1, int(math.Round(float64(w)*f)))
	pal := palette(rows)
	sy, sx := float64(h)/float64(th), float64(w)/float64(tw)

	out := make([][]rune, th)
	for ty := range th {
		out[ty] = make([]rune, tw)
		y0, y1 := float64(ty)*sy, float64(ty+1)*sy
		for tx := range tw {
			x0, x1 := float64(tx)*sx, float64(tx+1)*sx
			var area, ink float64
			share := map[rune]float64{}
			for y := int(y0); float64(y) < y1 && y < h; y++ {
				wy := math.Min(y1, float64(y+1)) - math.Max(y0, float64(y))
				for x := int(x0); float64(x) < x1 && x < w; x++ {
					a := wy * (math.Min(x1, float64(x+1)) - math.Max(x0, float64(x)))
					area += a
					if r := rows[y][x]; r != ' ' {
						ink += a
						share[r] += a
					}
				}
			}
			if area == 0 || ink/area < minCover {
				out[ty][tx] = ' '
				continue
			}
			// the glyph that covers most of the rectangle; on a tie, the denser one
			best := ' '
			for _, r := range pal {
				if share[r] > 0 && (best == ' ' || share[r] >= share[best]) {
					best = r
				}
			}
			out[ty][tx] = best
		}
	}
	return out
}

// palette is the glyphs the art uses, from the lightest to the densest.
func palette(rows [][]rune) []rune {
	seen := map[rune]bool{}
	var pal []rune
	for _, r := range rows {
		for _, ch := range r {
			if ch != ' ' && !seen[ch] {
				seen[ch] = true
				pal = append(pal, ch)
			}
		}
	}
	slices.SortFunc(pal, func(a, b rune) int {
		if d := glyphWeight(a) - glyphWeight(b); d != 0 {
			return d
		}
		return int(a) - int(b)
	})
	return pal
}

// glyphWeight is the place of a glyph on the scale from light to dense; a glyph that is not on it
// counts as the densest.
func glyphWeight(r rune) int {
	for i, g := range densRunes {
		if g == r {
			return i
		}
	}
	return len(densRunes) - 1
}
