package tui

import (
	"fmt"
	"image/color"

	"charm.land/glamour/v2/ansi"
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

// Markdown stylesheets. Messages show headings and code blocks without their
// markers or fences. Dim text, such as thinking, is dim everywhere. A user
// message carries its background on every cell.
var (
	messageStyles = markdownStyles(nil, lightYellow, gray, nil)
	dimStyles     = markdownStyles(dim, dim, dim, nil)
	userStyles    = markdownStyles(nil, lightYellow, gray, userMessage)
)

func markdownStyles(text, code, quote, bg color.Color) ansi.StyleConfig {
	yes := true
	quoteIndent := uint(1)
	quoteToken := "> "
	block := func(c color.Color) ansi.StyleBlock {
		return ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: hex(c)}}
	}
	return ansi.StyleConfig{
		Document:       ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: hex(text), BackgroundColor: hex(bg)}},
		Heading:        ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Bold: &yes, BlockSuffix: "\n"}},
		BlockQuote:     ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: hex(quote)}, Indent: &quoteIndent, IndentToken: &quoteToken},
		List:           ansi.StyleList{LevelIndent: 2},
		Item:           ansi.StylePrimitive{BlockPrefix: "- "},
		Enumeration:    ansi.StylePrimitive{BlockPrefix: ". "},
		Task:           ansi.StyleTask{Ticked: "[x] ", Unticked: "[ ] "},
		Emph:           ansi.StylePrimitive{Italic: &yes},
		Strong:         ansi.StylePrimitive{Bold: &yes},
		Strikethrough:  ansi.StylePrimitive{CrossedOut: &yes},
		Link:           ansi.StylePrimitive{Underline: &yes},
		LinkText:       ansi.StylePrimitive{Underline: &yes},
		HorizontalRule: ansi.StylePrimitive{Format: "\n---\n"},
		Code:           block(code),
		CodeBlock:      ansi.StyleCodeBlock{StyleBlock: block(code)},
		Table:          ansi.StyleTable{CenterSeparator: new("┼"), ColumnSeparator: new("│"), RowSeparator: new("─")},
	}
}

// hex is a color as Glamour reads it, or nil for none.
func hex(c color.Color) *string {
	if c == nil {
		return nil
	}
	r, g, b, _ := c.RGBA()
	return new(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
}
