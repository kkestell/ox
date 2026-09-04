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
	eventUsage
	eventOutcome
)

type event struct {
	kind      eventKind
	text      string
	messageID string
	thoughtID string
	call      openrouter.ToolCall
	toolKind  acp.ToolKind
	parent    string
	title     string
	delegates bool
	usage     *openrouter.Usage
	context   int
	totalCost float64
	update    any
}
