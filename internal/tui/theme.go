package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Exact colors, so Ox looks the same under every terminal color scheme.
var (
	background  = rgb(20, 20, 20)
	composer    = rgb(28, 28, 28)
	approval    = rgb(36, 36, 36)
	userMessage = rgb(40, 40, 40)
	textColor   = rgb(212, 212, 212)
	bright      = rgb(255, 255, 255)
	gray        = rgb(160, 160, 160)
	dim         = rgb(112, 112, 112)
	faint       = rgb(80, 80, 80)
	red         = rgb(244, 112, 103)
	yellow      = rgb(229, 192, 123)
	lightYellow = rgb(240, 220, 154)
	green       = rgb(152, 195, 121)
)

func rgb(r, g, b uint8) color.Color { return color.RGBA{r, g, b, 255} }

// fg is a style with the foreground color.
func fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var dimStyle = fg(dim)
