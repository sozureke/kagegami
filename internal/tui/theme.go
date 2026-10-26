package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

var (
	chromeHi  = lipgloss.Color("#eceef2")
	chromeMid = lipgloss.Color("#aab0bb")
	chromeLo  = lipgloss.Color("#6b707c")
	textTone  = lipgloss.Color("#d6dae1")
	userTone  = lipgloss.Color("#c9a86a")
	findTone  = lipgloss.Color("#e0555f")
	blockBG   = lipgloss.Color("#15171b")
)

func fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var (
	styleHi     = fg(chromeHi).Bold(true)
	styleMid    = fg(chromeMid)
	styleLo     = fg(chromeLo)
	styleText   = fg(textTone)
	styleUser   = fg(userTone)
	styleFind   = fg(findTone).Bold(true)
	styleDim    = fg(chromeLo).Faint(true)
	styleBlock  = lipgloss.NewStyle().Foreground(chromeHi).Background(blockBG).Border(lipgloss.RoundedBorder()).BorderForeground(chromeLo).Padding(0, 1)
	stylePrompt = fg(userTone).Bold(true)
	styleCursor = lipgloss.NewStyle().Foreground(blockBG).Background(chromeHi)
)
