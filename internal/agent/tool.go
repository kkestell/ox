package agent

import (
	"context"
	"encoding/json"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
)

type Tool struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	Kind         acp.ToolKind
	Approval     Approval
	ParallelSafe bool
	Delegates    bool
	Label        func(json.RawMessage) string
	// Suggest and Covered narrow allow-always grants to tool-defined rules.
	// A nil pair keeps the default name-scoped grant behavior.
	Suggest func(json.RawMessage) string
	Covered func([]string, json.RawMessage) bool
	Execute func(context.Context, Invocation) (string, error)
}

type toolSet struct {
	tools      []Tool
	byName     map[string]int
	modelTools []openrouter.Tool
}

// Approval is a tool's static gate classification. The zero value asks so a
// registration that omits the classification fails closed.
type Approval uint8

const (
	ApprovalAsk Approval = iota
	ApprovalNone
)

type Invocation struct {
	Arguments   json.RawMessage
	Root        string
	SpillDir    string
	CallID      string
	FileReads   FileReads
	Delegate    func(context.Context, string) (string, error)
	Emit        func(string)
	ReportSpill func(string)
}

type FileReads interface {
	Record(string, string)
	Hash(string) (string, bool)
	Clear()
}
