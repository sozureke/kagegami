package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// The palette is chrome, not green, with one saturated colour for findings (DESIGN.md, section 6).
var (
	chromeHi  = lipgloss.Color("#eceef2")
	chromeMid = lipgloss.Color("#aab0bb")
	chromeLo  = lipgloss.Color("#6b707c")
	userTone  = lipgloss.Color("#c9a86a")
	findTone  = lipgloss.Color("#e0555f")
	doneTone  = lipgloss.Color("#9aa3ae")
	blockBG   = lipgloss.Color("#15171b")
)

func fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var (
	styleHi    = fg(chromeHi).Bold(true)
	styleMid   = fg(chromeMid)
	styleLo    = fg(chromeLo)
	styleUser  = fg(userTone)
	styleFind  = fg(findTone).Bold(true)
	styleDone  = fg(doneTone)
	styleDim   = fg(chromeLo).Faint(true)
	styleBlock = lipgloss.NewStyle().Foreground(chromeHi).Background(blockBG).Border(lipgloss.RoundedBorder()).BorderForeground(chromeLo).Padding(0, 1)
)
