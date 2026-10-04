package tui

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/client"
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
	input            *input
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

// patched returns a cell style with s applied over it.
func patched(cell uv.Style, s style) uv.Style {
	if s.fg != nil {
		cell.Fg = s.fg
	}
	if s.bg != nil {
		cell.Bg = s.bg
	}
	cell.Attrs |= s.attrs
	if s.underline {
		cell.Underline = uv.UnderlineSingle
	}
	return cell
}

// fill applies s over every cell in area.
func fill(buf *uv.Buffer, area image.Rectangle, s style) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if cell := buf.CellAt(x, y); cell != nil {
				cell.Style = patched(cell.Style, s)
			}
		}
	}
}

// put writes a line on row y of area, clipped to it, applying each span's
// style over the cells' styles.
func put(buf *uv.Buffer, area image.Rectangle, y int, l line) {
	if y < 0 || y >= area.Dy() {
		return
	}
	y += area.Min.Y
	x := area.Min.X
	for _, span := range l.spans {
		s := l.style.patch(span.style)
		full := false
		graphemes(span.text, func(cluster string, w int) {
			if full || w == 0 {
				return
			}
			if x+w > area.Max.X {
				full = true
				return
			}
			buf.SetCell(x, y, &uv.Cell{Content: cluster, Width: w, Style: patched(buf.CellAt(x, y).Style, s)})
			x += w
		})
	}
}

// cursorIn returns column x of row y in area, kept inside it.
func cursorIn(area image.Rectangle, x, y int) image.Point {
	return image.Pt(area.Min.X+min(x, max(area.Dx()-1, 0)), area.Min.Y+min(y, max(area.Dy()-1, 0)))
}

// draw draws the screen and returns the cursor position and the layout.
func draw(buf *uv.Buffer, s screen) (image.Point, layout) {
	area := buf.Bounds()
	// Unstyled cells would show the terminal's own colors.
	fill(buf, area, style{fg: textColor, bg: background})
	height := area.Dy()
	rowWidth := inset(area, marginX, marginY).Dx()
	// A picker fills the screen, hiding the approval dialog and the composer,
	// and the cursor sits in its search input.
	if s.picker != nil {
		padded := inset(area, pickerPaddingX, pickerPaddingY)
		rows := pickerRows(height)
		for y, l := range pickerLines(s.picker, padded.Dx(), rows) {
			put(buf, padded, y, l)
		}
		return cursorIn(padded, width(s.picker.query), 0), layout{height: rows}
	}
	input := s.input.rows(rowWidth)
	var dialogLines []line
	if s.approval != nil {
		dialogLines = approvalLines(s.view, s.approval, s.approvalSelected, rowWidth)
	}
	// The composer holds the input, a blank row, and the status line.
	composerHeight := len(input.lines) + 4
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
	view := region(0, space)
	dialog := region(space, dialogHeight)
	composerArea := region(composerTop, composerHeight)
	fill(buf, dialog, style{bg: approval})
	fill(buf, composerArea, style{bg: composer})

	view = inset(view, marginX, marginY)
	viewHeight := view.Dy()
	total, lines := s.view.visibleRows(rowWidth, s.showThinking, s.toolOutput, s.now, viewHeight)
	for y, l := range lines {
		put(buf, view, y, l)
	}
	if s.view.newActivity && viewHeight > 0 {
		notice := "new activity"
		left := max(rowWidth-len(notice), 0) / 2
		centered := strings.Repeat(" ", left) + notice + strings.Repeat(" ", max(rowWidth-len(notice)-left, 0))
		put(buf, view, viewHeight-1, styled(centered, fg(lightYellow)))
	}

	l := layout{height: viewHeight, lines: total}
	if dialogLines != nil {
		dialog = inset(dialog, marginX, marginY)
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
			put(buf, dialog, footer, styled(hint, fg(dim)))
		}
		for y, row := range dialogLines[bodyEnd+1:] {
			put(buf, dialog, footer+1+y, row)
		}
	}

	composerArea = inset(composerArea, marginX, marginY)
	ghost := s.input.ghostText(s.commands)
	for y, text := range input.lines {
		row := line{spans: []span{{text: text}}}
		if y == len(input.lines)-1 {
			row.spans = append(row.spans, span{ghost, fg(dim)})
		}
		put(buf, composerArea, y, row)
	}
	put(buf, composerArea, len(input.lines)+1, styled(justified(s.settings, s.usage, rowWidth), fg(gray)))
	return cursorIn(composerArea, input.column, input.row), l
}

func pickerLines(p *picker, rowWidth, rows int) []line {
	errorLine := line{}
	if p.err != "" {
		errorLine = styled(clip(p.err, rowWidth), fg(red))
	}
	search := line{spans: []span{{text: p.query}}}
	if p.query == "" {
		search = line{spans: []span{{"Search", fg(dim)}}}
	}
	// The model picker's list toggle ends at the search row's right edge.
	if p.models != nil && p.models.hasFavorites() {
		all, favorites := bright, dim
		if p.models.showingFavorites {
			all, favorites = dim, bright
		}
		padding := max(rowWidth-search.width()-width("Favorites / All"), 0)
		search.spans = append(search.spans,
			span{text: strings.Repeat(" ", padding)},
			span{"Favorites", fg(favorites)},
			span{" / ", fg(dim)},
			span{"All", fg(all)},
		)
	}
	lines := []line{search, errorLine}
	if p.models == nil && len(p.sessions) == 0 {
		return append(lines, styled("No saved sessions", style{}))
	}
	var names []string
	if p.models != nil {
		names = modelNames(p.models.all, rowWidth)
	} else {
		names = sessionNames(p.sessions, rowWidth)
	}
	for i := p.first; i < min(p.first+rows, len(p.matches)); i++ {
		row := p.matches[i]
		s := fg(gray)
		if i == p.selected {
			s = fg(bright)
		}
		// All shows favorites in bold.
		if p.models != nil && !p.models.showingFavorites && slices.Contains(p.models.favorites, row) {
			s = s.patch(bold)
		}
		lines = append(lines, styled(names[row], s))
	}
	return lines
}

// approvalLines returns the approval dialog's rows: the heading, a blank row,
// the body, a blank row, and one row per option. draw slices the body and the
// options by position. The request's tool call may carry only part of the
// call, so the transcript's copy fills in the rest.
func approvalLines(view *transcript, request *client.Permission, selected, rowWidth int) []line {
	call := &toolCall{id: request.ToolCall.ToolCallId}
	if known := view.tool(call.id); known != nil {
		copied := *known
		call = &copied
	}
	call.apply(request.ToolCall, request.Name)
	// Shell content names everything being approved.
	var heading string
	var body []line
	switch call.name {
	case "shell":
		heading = "Would you like to run the following command?"
		body = contentLines(call, rowWidth, "  ", "  ", style{})
	case "shell_process":
		heading = "Would you like to send the following input?"
		body = contentLines(call, rowWidth, "  ", "  ", style{})
	default:
		heading = "Would you like to allow the following?"
		body = describedLines(call, rowWidth, style{})
	}
	lines := append([]line{styled(heading, style{}), {}}, body...)
	lines = append(lines, line{})
	for i, option := range request.Options {
		marker := " "
		if i == selected {
			marker = "›"
		}
		lines = append(lines, styled(fmt.Sprintf("%s %d. %s", marker, i+1, option.Name), style{}))
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
