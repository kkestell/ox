package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/x/ansi"
	protocol "github.com/coder/acp-go-sdk"
)

// countAfter is how long the thinking placeholder waits before counting.
const countAfter = 10 * time.Second

// truncatedRows is how many content rows a truncated call shows.
const truncatedRows = 5

// toolOutput is how much of a named call's content the transcript shows.
type toolOutput int

const (
	summary toolOutput = iota
	truncated
	full
)

func (o toolOutput) next() toolOutput { return (o + 1) % 3 }

// toolCall is the transcript's copy of a tool call.
type toolCall struct {
	id    protocol.ToolCallId
	title string
	// name is the tool's name, or empty for a nameless call such as a
	// replayed turn error.
	name     string
	status   protocol.ToolCallStatus
	content  []protocol.ToolCallContent
	rawInput any
}

// apply sets the fields the update carries.
func (c *toolCall) apply(update protocol.ToolCallUpdate, name string) {
	if update.Title != nil {
		c.title = *update.Title
	}
	if update.Status != nil {
		c.status = *update.Status
	}
	if update.Content != nil {
		c.content = update.Content
	}
	if update.RawInput != nil {
		c.rawInput = update.RawInput
	}
	if name != "" {
		c.name = name
	}
}

type itemKind int

const (
	userItem itemKind = iota
	thinkingItem
	responseItem
	toolProgressItem
	toolItem
	noticeItem
)

type item struct {
	kind itemKind
	text string
	// started and ended time a thinking item; ended is zero while it runs.
	started, ended time.Time
	call           *toolCall
	color          color.Color
	// hiddenSummary keeps the original item available in expanded modes.
	hiddenSummary bool
	// rows caches the item's rows while rendered is set.
	rows     []string
	rendered bool
}

func (i *item) thinking() bool { return i.kind == thinkingItem && i.ended.IsZero() }

// rowsKey is what an item's rows depend on besides the item.
type rowsKey struct {
	width        int
	showThinking bool
	output       toolOutput
}

// transcript is the conversation shown above the composer and how far it is
// scrolled.
type transcript struct {
	items    []item
	cacheKey rowsKey
	// tools maps the current turn's call IDs to their items.
	tools map[protocol.ToolCallId]int
	// batchStart bounds the current assistant response, including interleaved
	// thought and message chunks. progressAt is the turn's summary item.
	batchStart    int
	batchHasTools bool
	progressAt    int
	hasProgress   bool
	// userChunkOpen is set when the last update was a user message chunk.
	userChunkOpen bool
	// top is the first row shown while scrolled is set; otherwise the view
	// follows the end.
	top         int
	scrolled    bool
	newActivity bool
}

func (t *transcript) push(i item) {
	t.items = append(t.items, i)
}

func (t *transcript) last() *item {
	if len(t.items) == 0 {
		return nil
	}
	return &t.items[len(t.items)-1]
}

func (t *transcript) user(text string, now time.Time) {
	t.userChunkOpen = false
	t.endThinking(now)
	clear(t.tools)
	t.resetToolLoop()
	t.push(item{kind: userItem, text: text})
	t.batchStart = len(t.items)
	t.changed()
}

func (t *transcript) notice(text string, c color.Color, now time.Time) {
	t.userChunkOpen = false
	t.endThinking(now)
	t.push(item{kind: noticeItem, text: text, color: c})
	t.changed()
}

func (t *transcript) endTurn(now time.Time) {
	t.userChunkOpen = false
	t.endThinking(now)
	clear(t.tools)
	t.resetToolLoop()
	t.batchStart = len(t.items)
}

func (t *transcript) resetToolLoop() {
	t.batchStart = len(t.items)
	t.batchHasTools, t.hasProgress = false, false
}

// Replayed batches may start without a usage update between them.
func (t *transcript) startAssistant() {
	if t.batchHasTools {
		t.batchStart = len(t.items)
		t.batchHasTools = false
	}
}

// update adds a session update to the transcript. name is the tool name Ox
// adds to tool calls and their updates.
func (t *transcript) update(update protocol.SessionUpdate, name string, now time.Time) {
	continueUser := t.userChunkOpen
	t.userChunkOpen = update.UserMessageChunk != nil
	last := t.last()
	switch {
	case update.UserMessageChunk != nil:
		t.endThinking(now)
		text := content(update.UserMessageChunk.Content)
		if last != nil && last.kind == userItem && continueUser {
			last.text += text
			last.rendered = false
		} else {
			clear(t.tools)
			t.resetToolLoop()
			t.push(item{kind: userItem, text: text})
			t.batchStart = len(t.items)
		}
	case update.AgentThoughtChunk != nil:
		t.startAssistant()
		text := content(update.AgentThoughtChunk.Content)
		if last != nil && last.thinking() {
			last.text += text
			last.rendered = false
		} else {
			t.push(item{kind: thinkingItem, text: text, started: now})
		}
	case update.AgentMessageChunk != nil:
		t.startAssistant()
		t.endThinking(now)
		text := content(update.AgentMessageChunk.Content)
		if last != nil && last.kind == responseItem {
			last.text += text
			last.rendered = false
		} else {
			t.push(item{kind: responseItem, text: text})
		}
	case update.ToolCall != nil:
		t.endThinking(now)
		created := update.ToolCall
		call := &toolCall{
			id: created.ToolCallId, title: created.Title, name: name, status: created.Status,
			content: created.Content, rawInput: created.RawInput,
		}
		t.setTool(call)
		if call.name != "" {
			t.associateTool(t.tools[call.id], call)
		}
	case update.ToolCallUpdate != nil:
		t.endThinking(now)
		changed := toolCallUpdate(update.ToolCallUpdate)
		if index, ok := t.tools[changed.ToolCallId]; ok {
			t.items[index].call.apply(changed, name)
			t.items[index].rendered = false
			if t.hasProgress && t.items[t.progressAt].call == t.items[index].call {
				t.items[t.progressAt].rendered = false
			}
		} else {
			call := &toolCall{id: changed.ToolCallId}
			call.apply(changed, name)
			t.setTool(call)
			if call.name != "" {
				t.associateTool(t.tools[call.id], call)
			}
		}
	case update.UsageUpdate != nil:
		t.endThinking(now)
		t.batchHasTools = false
		t.batchStart = len(t.items)
	default:
		t.endThinking(now)
		return
	}
	t.changed()
}

// associateTool collapses a batch while preserving its original items for
// expanded modes. Invalidate rows when hiding items that may already be on screen.
func (t *transcript) associateTool(index int, call *toolCall) {
	if !t.batchHasTools {
		var text strings.Builder
		for i := t.batchStart; i < len(t.items); i++ {
			it := &t.items[i]
			if it.kind != thinkingItem && it.kind != responseItem {
				continue
			}
			it.hiddenSummary = true
			it.rendered = false
			if it.kind == responseItem {
				text.WriteString(it.text)
			}
		}
		if !t.hasProgress {
			t.progressAt = len(t.items)
			t.push(item{kind: toolProgressItem})
			t.hasProgress = true
		}
		progress := &t.items[t.progressAt]
		progress.text = text.String()
		progress.call = nil
		if strings.TrimSpace(progress.text) == "" {
			progress.call = call
		}
		progress.rendered = false
		t.batchHasTools = true
	}
	t.items[index].hiddenSummary = true
	t.items[index].rendered = false
}

// setTool replaces the current turn's call with the same ID, or adds the call.
func (t *transcript) setTool(call *toolCall) {
	if index, ok := t.tools[call.id]; ok {
		t.items[index] = item{kind: toolItem, call: call}
		return
	}
	if t.tools == nil {
		t.tools = map[protocol.ToolCallId]int{}
	}
	t.tools[call.id] = len(t.items)
	t.push(item{kind: toolItem, call: call})
}

func toolCallUpdate(u *protocol.SessionToolCallUpdate) protocol.ToolCallUpdate {
	return protocol.ToolCallUpdate{
		Meta: u.Meta, Content: u.Content, Kind: u.Kind, Locations: u.Locations, RawInput: u.RawInput,
		RawOutput: u.RawOutput, Status: u.Status, Title: u.Title, ToolCallId: u.ToolCallId,
	}
}

// tool returns the current turn's call with the ID, or nil.
func (t *transcript) tool(id protocol.ToolCallId) *toolCall {
	if index, ok := t.tools[id]; ok {
		return t.items[index].call
	}
	return nil
}

func (t *transcript) endThinking(now time.Time) {
	if last := t.last(); last != nil && last.thinking() {
		last.ended = now
		last.rendered = false
	}
}

// changed notes a change that arrived while the view is scrolled up.
func (t *transcript) changed() {
	if t.scrolled {
		t.newActivity = true
	}
}

func (t *transcript) pageUp(height, total int) {
	if total <= height {
		return
	}
	if !t.scrolled {
		t.top = total - height
	}
	t.top = max(t.top-page(height), 0)
	t.scrolled = true
}

func (t *transcript) pageDown(height, total int) {
	if !t.scrolled {
		return
	}
	if next := t.top + page(height); next >= max(total-height, 0) {
		t.end()
	} else {
		t.top = next
	}
}

func (t *transcript) end() {
	t.scrolled = false
	t.newActivity = false
}

// firstRow is the first row shown of total rows in a view height rows tall.
func (t *transcript) firstRow(height, total int) int {
	bottom := max(total-height, 0)
	if t.scrolled {
		return min(t.top, bottom)
	}
	return bottom
}

func page(height int) int { return max(height-1, 1) }

// revealTools brings the affected turn into view when an output mode change
// would otherwise change only rows above a long final answer.
func (t *transcript) revealTools(width int, showThinking bool, output toolOutput, now time.Time, height int) {
	if t.scrolled || width <= 0 || height <= 0 {
		return
	}
	total, _ := t.visibleRows(width, showThinking, output, now, height)
	row, userRow, toolRow, turnRow := 0, 0, -1, 0
	for i := range t.items {
		it := &t.items[i]
		if len(it.rows) == 0 {
			continue
		}
		if row > 0 {
			row++
		}
		if it.kind == userItem {
			userRow = row
		}
		if it.kind == toolProgressItem || it.kind == toolItem && it.call.name != "" {
			toolRow, turnRow = row, userRow
		}
		row += len(it.rows)
	}
	if toolRow >= 0 && toolRow < t.firstRow(height, total) {
		t.top, t.scrolled = turnRow, true
		t.newActivity = false
	}
}

// visibleRows returns the total row count and the rows shown in a view height
// rows tall, with one blank row between items.
func (t *transcript) visibleRows(width int, showThinking bool, output toolOutput, now time.Time, height int) (int, []string) {
	key := rowsKey{width, showThinking, output}
	if t.cacheKey != key {
		for i := range t.items {
			t.items[i].rendered = false
		}
		t.cacheKey = key
	}
	total := 0
	for i := range t.items {
		it := &t.items[i]
		// A running placeholder shows the time, so it renders every time.
		placeholder := it.thinking() && (!showThinking || strings.TrimSpace(it.text) == "")
		if !it.rendered || placeholder {
			it.rows = itemLines(it, width, showThinking, output, now)
			it.rendered = true
		}
		if len(it.rows) == 0 {
			continue
		}
		if total > 0 {
			total++
		}
		total += len(it.rows)
	}
	first := t.firstRow(height, total)
	end := first + height
	row := 0
	var visible []string
	for i := range t.items {
		rows := t.items[i].rows
		if len(rows) == 0 {
			continue
		}
		if row > 0 {
			if row >= first && row < end {
				visible = append(visible, "")
			}
			row++
		}
		from := min(max(first-row, 0), len(rows))
		to := min(max(end-row, 0), len(rows))
		visible = append(visible, rows[from:to]...)
		row += len(rows)
	}
	return total, visible
}

// itemLines returns an item's rows. A named call shows as much of its content
// as output allows; a nameless call, such as a replayed turn error, always
// shows all of it.
func itemLines(i *item, width int, showThinking bool, output toolOutput, now time.Time) []string {
	if output == summary && i.hiddenSummary && (i.kind != thinkingItem || !showThinking) {
		return nil
	}
	switch i.kind {
	case userItem:
		return userLines(i.text, width)
	case thinkingItem:
		if showThinking && strings.TrimSpace(i.text) != "" {
			return prefixed(markdown(i.text, dimStyle, width-2), dimStyle.Render("● "), "  ")
		}
		return styled(dimStyle, "● "+placeholder(i.started, i.ended, now))
	case responseItem:
		return prefixed(markdown(i.text, lipgloss.NewStyle(), width-2), "● ", "  ")
	case toolProgressItem:
		if output != summary {
			return nil
		}
		if i.call != nil {
			if row, ok := shellLine(i.call, width); ok {
				return []string{row}
			}
			return []string{toolLine(i.call, i.call.title, width)}
		}
		return prefixed(markdown(i.text, lipgloss.NewStyle(), width-2), "● ", "  ")
	case toolItem:
		call := i.call
		head, ok := shellLine(call, width)
		if !ok {
			head = toolLine(call, call.title, width)
		}
		lines := []string{head}
		switch {
		case call.name == "" || output == full:
			lines = append(lines, contentLines(call, width, "  └ ", "    ", true)...)
		case output == truncated:
			rows := contentRows(call, width-4, true)
			// Hiding a single row would not save a row.
			if len(rows) > truncatedRows+1 {
				hidden := len(rows) - truncatedRows
				count := fg(faint).Render(fmt.Sprintf("...%d more lines", hidden))
				rows = append([]string{count}, rows[hidden:]...)
			}
			lines = append(lines, prefixed(rows, "  └ ", "    ")...)
		}
		return lines
	default:
		return styled(fg(i.color), plain(i.text, width)...)
	}
}

// userLines returns a user message with one cell of padding and a background
// filling the transcript width.
func userLines(text string, width int) []string {
	width = max(width, 1)
	background := lipgloss.NewStyle().Background(userMessage)
	content := markdown(text, background, max(width-2, 1))
	if len(content) == 0 {
		content = []string{""}
	}
	padding := background.Render(strings.Repeat(" ", width))
	lines := []string{padding}
	for _, row := range content {
		right := max(width-ansi.StringWidth(row)-1, 0)
		lines = append(lines, background.Render(" ")+row+background.Render(strings.Repeat(" ", right)))
	}
	return append(lines, padding)
}

func placeholder(started, ended, now time.Time) string {
	if !ended.IsZero() {
		return fmt.Sprintf("Thought for %ds", int(ended.Sub(started).Seconds()))
	}
	elapsed := max(now.Sub(started), 0)
	if elapsed < countAfter {
		return "Thinking..."
	}
	return fmt.Sprintf("Thinking for %ds...", int(elapsed.Seconds()))
}

// describedLines returns a call's `●` row and its content, indented by two
// columns.
func describedLines(call *toolCall, width int) []string {
	return append([]string{toolLine(call, call.title, width)}, contentLines(call, width, "  ", "  ", false)...)
}

// contentLines returns a call's content rows with first before the first row
// and rest, of the same width, before the others.
func contentLines(call *toolCall, rowWidth int, first, rest string, dimmed bool) []string {
	return prefixed(contentRows(call, rowWidth-width(first), dimmed), first, rest)
}

// contentRows returns a call's content rows width columns wide, dim when
// dimmed is set. Text blocks wrap, as Markdown when the call is nameless, such
// as a replayed turn error; diff blocks are unified hunks, clipped so their
// indentation survives.
func contentRows(call *toolCall, width int, dimmed bool) []string {
	text := lipgloss.NewStyle()
	if dimmed {
		text = dimStyle
	}
	var rows []string
	for _, block := range call.content {
		switch {
		case block.Content != nil:
			content := content(block.Content.Content)
			if call.name == "" {
				rows = append(rows, markdown(content, text, width)...)
			} else {
				rows = append(rows, styled(text, plain(content, width)...)...)
			}
		case block.Diff != nil:
			rows = append(rows, diffRows(block.Diff, width, text)...)
		default:
			rows = append(rows, text.Render("[non-text content]"))
		}
	}
	return rows
}

// diffRows returns the unified hunks of a diff with three rows of context.
// Hunk headers and notes are dim, removed rows red, and added rows green;
// context rows keep the context style.
func diffRows(diff *protocol.ToolCallContentDiff, width int, context lipgloss.Style) []string {
	old := ""
	if diff.OldText != nil {
		old = *diff.OldText
	}
	unified, err := udiff.ToUnified("", "", old, udiff.Lines(old, diff.NewText), 3)
	if err != nil {
		return styled(fg(red), clip(err.Error(), width))
	}
	var rows []string
	// The first two rows name the files.
	for i, row := range strings.Split(strings.TrimSuffix(unified, "\n"), "\n") {
		if i < 2 {
			continue
		}
		rowStyle := context
		switch {
		case strings.HasPrefix(row, "@@"), strings.HasPrefix(row, "\\"):
			rowStyle = dimStyle
		case strings.HasPrefix(row, "-"):
			rowStyle = fg(red)
		case strings.HasPrefix(row, "+"):
			rowStyle = fg(green)
		}
		// Tabs expand from the column after the sign.
		row = strings.TrimSuffix(row, "\r")
		rows = append(rows, rowStyle.Render(clip(row[:1]+expand(row[1:]), width)))
	}
	return rows
}

func icon(call *toolCall) string {
	switch call.status {
	case protocol.ToolCallStatusPending, "":
		return fg(yellow).Render("●")
	case protocol.ToolCallStatusCompleted:
		return fg(green).Render("●")
	case protocol.ToolCallStatusFailed:
		return fg(red).Render("●")
	}
	return "●"
}

func toolLine(call *toolCall, title string, width int) string {
	return icon(call) + " " + clip(title, width-2)
}

// shellLine returns the one-row `● Shell` line of a shell call.
func shellLine(call *toolCall, width int) (string, bool) {
	input, ok := call.rawInput.(map[string]any)
	if call.name != "shell" || !ok {
		return "", false
	}
	command, ok := input["command"].(string)
	if !ok {
		return "", false
	}
	title := "Shell " + strings.Join(strings.Fields(command), " ")
	if background, _ := input["background"].(bool); background {
		title += " &"
	}
	return toolLine(call, title, width), true
}

func content(block protocol.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	return "[non-text content]"
}
