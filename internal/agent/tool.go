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
	ParentOnly   bool
	PlanMode     bool
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
	SessionID   string
	Root        string
	SpillDir    string
	CallID      string
	FileReads   FileReads
	FileSystem  ClientFileSystem
	Terminal    ClientTerminal
	Delegate    func(context.Context, string) (string, error)
	ReplaceTodo func([]acp.PlanEntry) error
	Emit        func(string)
	ReportSpill func(string)
}

type ClientFileSystem struct {
	ReadTextFile  func(context.Context, string, *int, *int) (string, error)
	WriteTextFile func(context.Context, string, string) error
}

type ClientTerminal struct {
	Create      func(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error)
	Output      func(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error)
	WaitForExit func(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error)
	Kill        func(context.Context, acp.KillTerminalRequest) error
	Release     func(context.Context, acp.ReleaseTerminalRequest) error
}

func (t ClientTerminal) Available() bool {
	return t.Create != nil && t.Output != nil && t.WaitForExit != nil &&
		t.Kill != nil && t.Release != nil
}

type FileReads interface {
	Record(string, string)
	Hash(string) (string, bool)
	Clear()
}
