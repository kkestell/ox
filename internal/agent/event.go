package agent

import (
	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
)

type eventKind int

const (
	eventResponseStart eventKind = iota
	eventText
	eventReasoning
	eventToolPending
	eventToolStarted
	eventToolOutput
	eventToolCompleted
	eventToolFailed
	eventPlan
	eventUsage
	eventOutcome
)

type event struct {
	kind             eventKind
	text             string
	messageID        string
	thoughtID        string
	call             openrouter.ToolCall
	toolKind         acp.ToolKind
	presentation     ToolPresentation
	target           string
	contextOccupancy int
	contextWindow    int
	totals           usageTotals
	update           any
	plan             []acp.PlanEntry
}
