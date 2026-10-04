package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/control"
)

// pickerTop is the rows before a picker's list: the search input and the
// error line, blank when there is no error.
const pickerTop = 2

// The blank columns beside a picker and the blank rows above and below it.
const pickerPaddingX, pickerPaddingY = 4, 2

// pickerRows is how many rows a picker shows on a screen height rows tall.
func pickerRows(height int) int {
	return max(height-2*pickerPaddingY-pickerTop, 0)
}

// picker is a searchable list that fills the screen: saved sessions, or the
// models when models is set.
type picker struct {
	sessions []protocol.SessionInfo
	models   []modelChoice
	query    string
	// matches are the indexes of the shown rows that match the query, in the
	// shown order.
	matches []int
	// selected is an index into matches.
	selected int
	first    int
	err      string
}

func newSessionPicker(sessions []protocol.SessionInfo) *picker {
	p := &picker{sessions: sortedSessions(sessions)}
	p.filter()
	return p
}

func newModelPicker(models []modelChoice, favorites []string) *picker {
	for i := range models {
		models[i].favorite = slices.Contains(favorites, string(models[i].value))
	}
	p := &picker{models: models}
	p.filter()
	return p
}

// filter keeps the rows whose name contains every word of the query,
// ignoring case, and selects the first. Favorite models come first; both
// groups keep the session's order.
func (p *picker) filter() {
	words := strings.Fields(strings.ToLower(p.query))
	var names []string
	if p.models != nil {
		for _, model := range p.models {
			names = append(names, model.name)
		}
	} else {
		for _, session := range p.sessions {
			names = append(names, sessionTitle(session))
		}
	}
	var favorites, others []int
	for index, name := range names {
		name = strings.ToLower(name)
		if slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(name, word) }) {
			continue
		}
		if p.models != nil && p.models[index].favorite {
			favorites = append(favorites, index)
		} else {
			others = append(others, index)
		}
	}
	p.matches = append(favorites, others...)
	p.selected, p.first = 0, 0
}

// moveTo selects a match and scrolls so it is among the rows shown.
func (p *picker) moveTo(selected, rows int) {
	p.selected = max(min(selected, len(p.matches)-1), 0)
	if p.selected < p.first {
		p.first = p.selected
	}
	if p.selected >= p.first+rows {
		p.first = p.selected + 1 - rows
	}
}

type modelChoice struct {
	value    protocol.SessionConfigValueId
	name     string
	provider string
	// inputPrice and outputPrice are USD per million tokens.
	inputPrice, outputPrice *float64
	contextLimit            *uint64
	favorite                bool
}

// newModelChoice reads the provider, prices, and context limit Ox sends in
// the choice's `_meta`. Values of the wrong type are left out.
func newModelChoice(choice protocol.SessionConfigSelectOption) modelChoice {
	model := modelChoice{value: choice.Value, name: control.Escape(choice.Name)}
	if provider, ok := choice.Meta["provider"].(string); ok {
		model.provider = control.Escape(provider)
	}
	price := func(key string) *float64 {
		if value, ok := choice.Meta[key].(float64); ok {
			return &value
		}
		return nil
	}
	model.inputPrice, model.outputPrice = price("inputPrice"), price("outputPrice")
	if limit, ok := choice.Meta["contextLimit"].(float64); ok && limit >= 0 && limit == float64(uint64(limit)) {
		model.contextLimit = new(uint64(limit))
	}
	return model
}

func activity(session protocol.SessionInfo) (time.Time, bool) {
	if session.UpdatedAt == nil {
		return time.Time{}, false
	}
	date, err := time.Parse(time.RFC3339, *session.UpdatedAt)
	return date, err == nil
}

func sessionTitle(session protocol.SessionInfo) string {
	if session.Title == nil {
		return "Untitled session"
	}
	return control.Escape(*session.Title)
}

// sortedSessions orders sessions by their latest activity, newest first and
// undated last.
func sortedSessions(sessions []protocol.SessionInfo) []protocol.SessionInfo {
	sessions = slices.Clone(sessions)
	slices.SortStableFunc(sessions, func(a, b protocol.SessionInfo) int {
		aDate, aDated := activity(a)
		bDate, bDated := activity(b)
		switch {
		case aDated && bDated:
			return bDate.Compare(aDate)
		case aDated:
			return -1
		case bDated:
			return 1
		}
		return 0
	})
	return sessions
}

func sessionNames(sessions []protocol.SessionInfo, rowWidth int) []string {
	var names []string
	for _, session := range sessions {
		date := "Unknown date"
		if updated, ok := activity(session); ok {
			date = updated.Format(time.DateOnly)
		}
		names = append(names, justified(sessionTitle(session), date, rowWidth))
	}
	return names
}

// modelNames returns the model rows. The provider column reserves its widest
// value plus two spaces. Both price columns share the widest price's width
// plus two spaces.
func modelNames(models []modelChoice, rowWidth int) []string {
	price := func(price *float64) string {
		if price == nil {
			return ""
		}
		return fmt.Sprintf("$%.2f", *price)
	}
	limit := func(model modelChoice) string {
		if model.contextLimit == nil {
			return ""
		}
		return thousands(*model.contextLimit)
	}
	providerWidth, priceWidth, limitWidth := 0, 0, 0
	for _, model := range models {
		if model.provider != "" {
			providerWidth = max(providerWidth, width(model.provider)+2)
		}
		priceWidth = max(priceWidth, width(price(model.inputPrice)), width(price(model.outputPrice)))
		limitWidth = max(limitWidth, width(limit(model)))
	}
	priceWidth += 2
	var names []string
	for _, model := range models {
		details := pad(model.provider, providerWidth) + pad(price(model.inputPrice), priceWidth) +
			pad(price(model.outputPrice), priceWidth) + strings.Repeat(" ", limitWidth-width(limit(model))) + limit(model)
		names = append(names, justified(model.name, details, rowWidth))
	}
	return names
}

// pad fills text with spaces to columns.
func pad(text string, columns int) string {
	return text + strings.Repeat(" ", max(columns-width(text), 0))
}

// thousands formats a number with commas between thousands.
func thousands(value uint64) string {
	digits := strconv.FormatUint(value, 10)
	var text strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			text.WriteByte(',')
		}
		text.WriteRune(digit)
	}
	return text.String()
}

// justified returns left, at least two spaces, and right ending at rowWidth,
// with left clipped to fit.
func justified(left, right string, rowWidth int) string {
	left = clip(left, max(rowWidth-width(right)-2, 0))
	return left + strings.Repeat(" ", max(rowWidth-width(left)-width(right), 0)) + right
}
