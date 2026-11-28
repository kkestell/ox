package server

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/agent"
)

const (
	pageRunes      = 40000 // the runes of rendered entries and their separators in a page
	toolInputRunes = 200
	timeLayout     = "2006-01-02 15:04:05 -0700"
)

// transcriptPage renders history[:end] newest last, walking back from the end
// while the entries fit the page. A page that leaves earlier entries behind
// begins with the cursor that reads them. The first entry the walk takes is
// always included, cut to the page size if it alone exceeds it.
func transcriptPage(history []agent.Update, end int) string {
	failed := map[string]bool{}
	for _, u := range history {
		if u.Type == agent.UpdateToolResult && u.Result != nil && u.Result.IsError {
			failed[u.ToolUseID] = true
		}
	}
	var entries []string // newest first
	used := 0
	for i := end - 1; i >= 0; i-- {
		entry, ok := renderEntry(history[i], failed)
		if !ok {
			continue
		}
		cost := utf8.RuneCountInString(entry)
		if len(entries) > 0 {
			cost += len("\n\n")
		}
		switch {
		case used+cost <= pageRunes:
		case len(entries) == 0:
			entry, cost = cut(entry, pageRunes), pageRunes+1
		default:
			slices.Reverse(entries)
			return fmt.Sprintf("Earlier entries: call read_session with cursor %d.\n\n%s", i+1, strings.Join(entries, "\n\n"))
		}
		entries = append(entries, entry)
		used += cost
	}
	slices.Reverse(entries)
	return strings.Join(entries, "\n\n")
}

// firstRunes is the first n runes of text, and whether it cut any.
func firstRunes(text string, n int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= n {
		return text, false
	}
	return string(runes[:n]), true
}

// cut keeps the first n runes of text, marking the cut with an ellipsis.
func cut(text string, n int) string {
	if first, long := firstRunes(text, n); long {
		return first + "…"
	}
	return text
}

// renderEntry renders a history update as one transcript entry; tool results
// and parking are not shown.
func renderEntry(u agent.Update, failed map[string]bool) (string, bool) {
	when := u.Time.Local().Format(timeLayout)
	switch u.Type {
	case agent.UpdatePrompt:
		if len(u.Reports) > 0 {
			lines := []string{fmt.Sprintf("[%s] Reports:", when)}
			for _, r := range u.Reports {
				lines = append(lines, fmt.Sprintf("%s (%s): %s", r.FromLabel, r.From, r.Text))
			}
			return strings.Join(lines, "\n"), true
		}
		if u.From != "" {
			return fmt.Sprintf("[%s] Message from %s (%s): %s", when, u.FromLabel, u.From, u.Text), true
		}
		text := u.Text
		if len(u.Images) > 0 {
			text = strings.TrimSpace(fmt.Sprintf("%s [images: %d]", text, len(u.Images)))
		}
		return fmt.Sprintf("[%s] User: %s", when, text), true
	case agent.UpdateTextDelta:
		return "Assistant: " + u.Text, true
	case agent.UpdateToolUse:
		entry := "Tool: " + u.ToolName
		if len(u.Input) > 0 {
			entry += " " + cut(string(u.Input), toolInputRunes)
		}
		if failed[u.ToolUseID] {
			entry += " (failed)"
		}
		return entry, true
	case agent.UpdateResult:
		if u.Result != nil && u.Result.IsError {
			return "Error: " + u.Result.Text, true
		}
		return "(turn finished)", true
	case agent.UpdateExited:
		return "Exited: " + u.Error, true
	}
	return "", false
}

// stateLine describes what the history lacks: the session's state, the tools
// it waits on approval for, and how many inputs it has not taken.
func stateLine(snapshot agent.Snapshot) string {
	state := snapshot.State
	if state == agent.StateRequiresAction {
		state = "waiting for approval"
		names := make([]string, len(snapshot.Approvals))
		for i, a := range snapshot.Approvals {
			var request struct {
				ToolName string `json:"tool_name"`
			}
			json.Unmarshal(a.Request, &request)
			names[i] = request.ToolName
		}
		if len(names) > 0 {
			state += ": " + strings.Join(names, ", ")
		}
	}
	if queued := len(snapshot.Queued); queued > 0 {
		state += fmt.Sprintf(", %d queued", queued)
	}
	return "(" + state + ")"
}
