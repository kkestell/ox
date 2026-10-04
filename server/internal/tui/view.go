package tui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/client"
	"ox/internal/control"
)

// Every region pads its content by one row above and below and two columns on
// each side.
const marginX, marginY = 2, 1

// layout is the rows and line counts the last frame gave each pageable
// region.
type layout struct {
	height, lines                     int
	approvalHeight, approvalBodyLines int
}

// screen is everything a frame shows.
type screen struct {
	view             *transcript
	input            *textarea.Model
	commands         []string
	approval         *client.Permission
	approvalSelected int
	approvalScroll   int
	picker           *picker
	settings, usage  string
	showThinking     bool
	toolOutput       toolOutput
	now              time.Time
}

// inset returns r without h columns on each side and v rows above and below.
func inset(r image.Rectangle, h, v int) image.Rectangle {
	inner := image.Rect(r.Min.X+h, r.Min.Y+v, r.Max.X-h, r.Max.Y-v)
	inner.Max.X = max(inner.Max.X, inner.Min.X)
	inner.Max.Y = max(inner.Max.Y, inner.Min.Y)
	return inner
}

// newFrame returns a frame buffer that measures text as the rest of the
// client does, by grapheme cluster.
func newFrame(width, height int) uv.ScreenBuffer {
	buf := uv.NewScreenBuffer(width, height)
	buf.Method = ansi.GraphemeWidth
	return buf
}

// put draws a row on row y of area, clipped to it.
func put(buf uv.ScreenBuffer, area image.Rectangle, y int, row string) {
	if y < 0 || y >= area.Dy() {
		return
	}
	y += area.Min.Y
	uv.NewStyledString(row).Draw(buf, image.Rect(area.Min.X, y, area.Max.X, y+1))
}

// fill gives every cell in area without a foreground the text color and every
// cell without a background bg.
func fill(buf uv.ScreenBuffer, area image.Rectangle, bg color.Color) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			cell := buf.CellAt(x, y)
			if cell == nil {
				continue
			}
			if cell.Style.Fg == nil {
				cell.Style.Fg = textColor
			}
			if cell.Style.Bg == nil {
				cell.Style.Bg = bg
			}
		}
	}
}

// cursorIn returns column x of row y in area, kept inside it.
func cursorIn(area image.Rectangle, x, y int) image.Point {
	return image.Pt(area.Min.X+min(x, max(area.Dx()-1, 0)), area.Min.Y+min(y, max(area.Dy()-1, 0)))
}

// draw draws the screen and returns the cursor position and the layout.
func draw(buf uv.ScreenBuffer, s screen) (image.Point, layout) {
	area := buf.Bounds()
	height := area.Dy()
	rowWidth := inset(area, marginX, marginY).Dx()
	// A picker fills the screen, hiding the approval dialog and the composer,
	// and the cursor sits in its search input.
	if s.picker != nil {
		padded := inset(area, pickerPaddingX, pickerPaddingY)
		rows := pickerRows(height)
		for y, row := range pickerLines(s.picker, padded.Dx(), rows) {
			put(buf, padded, y, row)
		}
		fill(buf, area, background)
		return cursorIn(padded, width(s.picker.query), 0), layout{height: rows}
	}
	s.input.SetWidth(rowWidth)
	var dialogLines []string
	if s.approval != nil {
		dialogLines = approvalLines(s.view, s.approval, s.approvalSelected, rowWidth)
	}
	// The composer holds the input, a blank row, and the status line.
	inputHeight := s.input.Height()
	composerHeight := inputHeight + 4
	composerTop := max(height-composerHeight, 0)
	dialogHeight := 0
	if dialogLines != nil {
		dialogHeight = min(len(dialogLines)+2, composerTop)
	}
	space := composerTop - dialogHeight
	// region is rows rows from row y, clipped to the screen.
	region := func(y, rows int) image.Rectangle {
		top := min(y, height)
		return image.Rect(area.Min.X, area.Min.Y+top, area.Max.X, area.Min.Y+top+min(rows, height-top))
	}
	viewArea := region(0, space)
	dialogArea := region(space, dialogHeight)
	composerArea := region(composerTop, composerHeight)

	view := inset(viewArea, marginX, marginY)
	viewHeight := view.Dy()
	total, lines := s.view.visibleRows(rowWidth, s.showThinking, s.toolOutput, s.now, viewHeight)
	for y, row := range lines {
		put(buf, view, y, row)
	}
	if s.view.newActivity && viewHeight > 0 {
		notice := "new activity"
		left := max(rowWidth-len(notice), 0) / 2
		put(buf, view, viewHeight-1, fg(lightYellow).Render(strings.Repeat(" ", left)+notice))
	}

	l := layout{height: viewHeight, lines: total}
	if dialogLines != nil {
		dialog := inset(dialogArea, marginX, marginY)
		options := len(s.approval.Options)
		bodyEnd := len(dialogLines) - options - 1
		body := dialogLines[2:bodyEnd]
		l.approvalHeight = max(dialog.Dy()-3-options, 0)
		l.approvalBodyLines = len(body)
		first := min(s.approvalScroll, max(len(body)-l.approvalHeight, 0))
		put(buf, dialog, 0, dialogLines[0])
		for y, row := range body[first:min(first+l.approvalHeight, len(body))] {
			put(buf, dialog, y+2, row)
		}
		footer := 2 + l.approvalHeight
		if len(body) > l.approvalHeight {
			hint := "↑ Page Up for earlier"
			if first+l.approvalHeight < len(body) {
				hint = "↓ Page Down for more"
			}
			put(buf, dialog, footer, dimStyle.Render(hint))
		}
		for y, row := range dialogLines[bodyEnd+1:] {
			put(buf, dialog, footer+1+y, row)
		}
	}

	inner := inset(composerArea, marginX, marginY)
	for y, row := range strings.Split(s.input.View(), "\n") {
		put(buf, inner, y, row)
	}
	cursor := s.input.Cursor().Position
	if ghost := ghostText(s.input.Value(), atEnd(s.input), s.commands); ghost != "" {
		put(buf, image.Rect(inner.Min.X+cursor.X, inner.Min.Y, inner.Max.X, inner.Max.Y), cursor.Y, dimStyle.Render(ghost))
	}
	put(buf, inner, inputHeight+1, fg(gray).Render(justified(s.settings, s.usage, rowWidth)))

	fill(buf, viewArea, background)
	fill(buf, dialogArea, approval)
	fill(buf, composerArea, composer)
	return cursorIn(inner, cursor.X, cursor.Y), l
}

func pickerLines(p *picker, rowWidth, rows int) []string {
	errorLine := ""
	if p.err != "" {
		errorLine = fg(red).Render(clip(p.err, rowWidth))
	}
	search := p.query
	if p.query == "" {
		search = dimStyle.Render("Search")
	}
	lines := []string{search, errorLine}
	if p.models == nil && len(p.sessions) == 0 {
		return append(lines, "No saved sessions")
	}
	var names []string
	if p.models != nil {
		names = modelNames(p.models, rowWidth)
	} else {
		names = sessionNames(p.sessions, rowWidth)
	}
	for i := p.first; i < min(p.first+rows, len(p.matches)); i++ {
		row := p.matches[i]
		s := fg(gray)
		if i == p.selected {
			s = fg(bright)
		}
		if p.models != nil && p.models[row].favorite {
			s = s.Bold(true)
		}
		lines = append(lines, s.Render(names[row]))
	}
	return lines
}

// approvalLines returns the approval dialog's rows: the heading, a blank row,
// the body, a blank row, and one row per option. draw slices the body and the
// options by position. The request's tool call may carry only part of the
// call, so the transcript's copy fills in the rest.
func approvalLines(view *transcript, request *client.Permission, selected, rowWidth int) []string {
	call := &toolCall{id: request.ToolCall.ToolCallId}
	if known := view.tool(call.id); known != nil {
		copied := *known
		call = &copied
	}
	call.apply(request.ToolCall, request.Name)
	// Shell content names everything being approved.
	var heading string
	var body []string
	switch call.name {
	case "shell":
		heading = "Would you like to run the following command?"
		body = contentLines(call, rowWidth, "  ", "  ", false)
	case "shell_process":
		heading = "Would you like to send the following input?"
		body = contentLines(call, rowWidth, "  ", "  ", false)
	default:
		heading = "Would you like to allow the following?"
		body = describedLines(call, rowWidth)
	}
	lines := append([]string{heading, ""}, body...)
	lines = append(lines, "")
	for i, option := range request.Options {
		marker := " "
		if i == selected {
			marker = "›"
		}
		lines = append(lines, fmt.Sprintf("%s %d. %s", marker, i+1, control.Escape(option.Name)))
	}
	return lines
}

// selectOption returns the first config option in the category when it is a
// select option.
func selectOption(options []protocol.SessionConfigOption, category protocol.SessionConfigOptionCategory) *protocol.SessionConfigOptionSelect {
	for _, option := range options {
		var optionCategory *protocol.SessionConfigOptionCategory
		switch {
		case option.Select != nil:
			optionCategory = option.Select.Category
		case option.Boolean != nil:
			optionCategory = option.Boolean.Category
		}
		if optionCategory != nil && *optionCategory == category {
			return option.Select
		}
	}
	return nil
}

// choices returns the choices of a select option, with any groups flattened.
func choices(option *protocol.SessionConfigOptionSelect) []protocol.SessionConfigSelectOption {
	switch {
	case option.Options.Ungrouped != nil:
		return *option.Options.Ungrouped
	case option.Options.Grouped != nil:
		var all []protocol.SessionConfigSelectOption
		for _, group := range *option.Options.Grouped {
			all = append(all, group.Options...)
		}
		return all
	}
	return nil
}

// settingsSummary is the status line's left side: the names of the mode,
// model, and effort in use.
func settingsSummary(options []protocol.SessionConfigOption) string {
	var names []string
	for _, category := range []protocol.SessionConfigOptionCategory{
		protocol.SessionConfigOptionCategoryMode, protocol.SessionConfigOptionCategoryModel, protocol.SessionConfigOptionCategoryThoughtLevel,
	} {
		option := selectOption(options, category)
		if option == nil {
			continue
		}
		name := string(option.CurrentValue)
		for _, choice := range choices(option) {
			if choice.Value == option.CurrentValue {
				name = choice.Name
				break
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, " • ")
}

// usageSummary is the status line's right side: the context used and the
// session cost.
func usageSummary(usage *protocol.SessionUsageUpdate) string {
	percent, cost := 0.0, 0.0
	if usage != nil {
		if usage.Size != 0 {
			percent = math.Round(float64(usage.Used) * 100 / float64(usage.Size))
		}
		if usage.Cost != nil {
			cost = usage.Cost.Amount
		}
	}
	return strconv.FormatFloat(percent, 'f', -1, 64) + "% • $" + strconv.FormatFloat(cost, 'f', 2, 64)
}
