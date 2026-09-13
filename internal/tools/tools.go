package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
)

func All() []agent.Tool {
	coordination := subagentTools()
	return append(coordination, []agent.Tool{
		{
			Name:         "question",
			Description:  questionDescription,
			InputSchema:  json.RawMessage(questionSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalNone,
			ParallelSafe: false,
			PlanMode:     true,
			RequiresForm: true,
			Title:        questionTitle,
			Execute:      executeQuestion,
		},
		{
			Name:         "skill",
			Description:  skillDescription,
			InputSchema:  json.RawMessage(skillSchema),
			Kind:         acp.ToolKindRead,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        skillTitle,
			Execute:      executeSkill,
		},
		{
			Name:         "todo",
			Description:  todoDescription,
			InputSchema:  json.RawMessage(todoSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalNone,
			ParallelSafe: false,
			PlanMode:     true,
			Scope:        agent.ToolScopePrimary,
			Title:        todoTitle,
			Execute:      executeTodo,
		},
		{
			Name:         "read_file",
			Description:  readDescription,
			InputSchema:  json.RawMessage(readSchema),
			Kind:         acp.ToolKindRead,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        readTitle,
			Execute:      executeRead,
		},
		{
			Name:         "glob",
			Description:  globDescription,
			InputSchema:  json.RawMessage(globSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        globTitle,
			Execute:      executeGlob,
		},
		{
			Name:         "grep",
			Description:  grepDescription,
			InputSchema:  json.RawMessage(grepSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        grepTitle,
			Execute:      executeGrep,
		},
		{
			Name:         "lsp_definition",
			Description:  lspDefinitionDescription,
			InputSchema:  json.RawMessage(lspDefinitionSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        lspDefinitionTitle,
			Execute:      executeLSPDefinition,
		},
		{
			Name:         "lsp_references",
			Description:  lspReferencesDescription,
			InputSchema:  json.RawMessage(lspReferencesSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        lspReferencesTitle,
			Execute:      executeLSPReferences,
		},
		{
			Name:         "lsp_document_symbols",
			Description:  lspDocumentSymbolsDescription,
			InputSchema:  json.RawMessage(lspDocumentSymbolsSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        lspDocumentSymbolsTitle,
			Execute:      executeLSPDocumentSymbols,
		},
		{
			Name:         "lsp_workspace_symbols",
			Description:  lspWorkspaceSymbolsDescription,
			InputSchema:  json.RawMessage(lspWorkspaceSymbolsSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        lspWorkspaceSymbolsTitle,
			Execute:      executeLSPWorkspaceSymbols,
		},
		{
			Name:         "lsp_diagnostics",
			Description:  lspDiagnosticsDescription,
			InputSchema:  json.RawMessage(lspDiagnosticsSchema),
			Kind:         acp.ToolKindRead,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        lspDiagnosticsTitle,
			Execute:      executeLSPDiagnostics,
		},
		{
			Name:         "web_fetch",
			Description:  webFetchDescription,
			InputSchema:  json.RawMessage(webFetchSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        webFetchTitle,
			Execute:      executeWebFetch,
		},
		{
			Name:         "memory_search",
			Description:  memorySearchDescription,
			InputSchema:  json.RawMessage(memorySearchSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        memorySearchTitle,
			Execute:      executeMemorySearch,
		},
		{
			Name:         "memory_write",
			Description:  memoryWriteDescription,
			InputSchema:  json.RawMessage(memoryWriteSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Title:        memoryWriteTitle,
			Execute:      executeMemoryWrite,
		},
		{
			Name:         "memory_delete",
			Description:  memoryDeleteDescription,
			InputSchema:  json.RawMessage(memoryDeleteSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Title:        memoryDeleteTitle,
			Execute:      executeMemoryDelete,
		},
		{
			Name:         "write_file",
			Description:  writeDescription,
			InputSchema:  json.RawMessage(writeSchema),
			Kind:         acp.ToolKindEdit,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Title:        writeTitle,
			Execute:      executeWrite,
		},
		{
			Name:         "edit_file",
			Description:  editDescription,
			InputSchema:  json.RawMessage(editSchema),
			Kind:         acp.ToolKindEdit,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Title:        editTitle,
			Execute:      executeEdit,
		},
		{
			Name:         "shell",
			Description:  shellDescription,
			InputSchema:  json.RawMessage(shellSchema),
			Kind:         acp.ToolKindExecute,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Suggest:      shellSuggestion,
			Covered:      shellCovered,
			Title:        shellTitle,
			Execute:      executeShell,
		},
	}...)
}

func decodeArgs(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New(trimJSONError(err.Error()))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("tool arguments must be a single JSON object")
	}
	return nil
}

func trimJSONError(message string) string {
	const prefix = "json: "
	if len(message) >= len(prefix) && message[:len(prefix)] == prefix {
		return message[len(prefix):]
	}
	return message
}

func requireString(name string, value *string) (string, error) {
	if value == nil {
		return "", errors.New("`" + name + "` must be a string")
	}
	return *value, nil
}
