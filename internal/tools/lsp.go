package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/lsp"
	"github.com/kkestell/ox/internal/workspace"
)

// lspInlineBytes bounds a language result before it spills. Language answers
// are short lists of positions, so a result this large is already unusable to
// the model and belongs in a file it can page through.
const lspInlineBytes = 64 << 10

const positionProperties = `
		"path": {"type": "string", "description": "Workspace-relative path to the source file."},
		"line": {"type": "integer", "minimum": 1, "description": "1-based line number."},
		"column": {"type": "integer", "minimum": 1, "description": "1-based column, counted in Unicode characters."}`

const lspDefinitionDescription = "Find where the symbol at a position is defined, using the language server " +
	"configured for the file's extension. Prefer this over searching for a name."

var lspDefinitionSchema = `{
	"type": "object",
	"properties": {` + positionProperties + `
	},
	"required": ["path", "line", "column"],
	"additionalProperties": false
}`

const lspReferencesDescription = "Find every reference to the symbol at a position, using the language server " +
	"configured for the file's extension. Prefer this over searching for a name."

var lspReferencesSchema = `{
	"type": "object",
	"properties": {` + positionProperties + `,
		"include_declaration": {"type": "boolean", "description": "Include the declaration itself. Defaults to true."}
	},
	"required": ["path", "line", "column"],
	"additionalProperties": false
}`

const lspDocumentSymbolsDescription = "Outline one file's symbols with their kinds and line ranges. " +
	"Use this to orient yourself in a large file before reading it."

const lspDocumentSymbolsSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string", "description": "Workspace-relative path to the source file."}
	},
	"required": ["path"],
	"additionalProperties": false
}`

const lspWorkspaceSymbolsDescription = "Search every configured language server for workspace symbols matching a query."

const lspWorkspaceSymbolsSchema = `{
	"type": "object",
	"properties": {
		"query": {"type": "string", "description": "Symbol name or fragment to search for."}
	},
	"required": ["query"],
	"additionalProperties": false
}`

const lspDiagnosticsDescription = "Report the language server's diagnostics for one file. The result names the " +
	"document version it observed and whether analysis had finished."

const lspDiagnosticsSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string", "description": "Workspace-relative path to the source file."}
	},
	"required": ["path"],
	"additionalProperties": false
}`

type lspPositionArguments struct {
	Path   *string `json:"path"`
	Line   *int    `json:"line"`
	Column *int    `json:"column"`
}

// lspReferencesArguments is the only position query with an option, so
// include_declaration stays out of the schema every other position tool
// declares.
type lspReferencesArguments struct {
	lspPositionArguments
	IncludeDeclaration *bool `json:"include_declaration"`
}

type lspPathArguments struct {
	Path *string `json:"path"`
}

type lspQueryArguments struct {
	Query *string `json:"query"`
}

// languageReader gives the adapter the same authoritative text the file tools
// use: the client's copy when the activation delegates reads, and the confined
// local file otherwise. The adapter resolves paths inside the workspace before
// calling this, and validates the UTF-8 it gets back.
func languageReader(invocation agent.Invocation) lsp.TextReader {
	files := workspace.NewWorkspace(invocation.Root)
	read := invocation.FileSystem.ReadTextFile
	return func(ctx context.Context, absolute string) (string, error) {
		raw, err := acquireText(ctx, files, absolute, absolute, read)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
}

func languageQueries(invocation agent.Invocation) (agent.LanguageQueries, error) {
	if invocation.Languages == nil {
		return nil, fmt.Errorf(
			"no language servers are configured: set \"process.language_servers\" in the global settings file",
		)
	}
	return invocation.Languages, nil
}

func decodePosition(arguments lspPositionArguments) (string, lsp.Position, error) {
	path, err := requireString("path", arguments.Path)
	if err != nil {
		return "", lsp.Position{}, err
	}
	line, err := requiredPositiveInt("line", arguments.Line)
	if err != nil {
		return "", lsp.Position{}, err
	}
	column, err := requiredPositiveInt("column", arguments.Column)
	if err != nil {
		return "", lsp.Position{}, err
	}
	return path, lsp.Position{Line: line, Column: column}, nil
}

func requiredPositiveInt(name string, value *int) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("`%s` must be an integer >= 1", name)
	}
	return positiveInt(name, value, 0)
}

func executeLSPDefinition(ctx context.Context, invocation agent.Invocation) (string, error) {
	queries, err := languageQueries(invocation)
	if err != nil {
		return "", err
	}
	var arguments lspPositionArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, position, err := decodePosition(arguments)
	if err != nil {
		return "", err
	}
	locations, err := queries.Definition(ctx, path, position, languageReader(invocation))
	if err != nil {
		return "", err
	}
	return renderLanguage(invocation, renderLocations(locations, "definition"))
}

func executeLSPReferences(ctx context.Context, invocation agent.Invocation) (string, error) {
	queries, err := languageQueries(invocation)
	if err != nil {
		return "", err
	}
	var arguments lspReferencesArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, position, err := decodePosition(arguments.lspPositionArguments)
	if err != nil {
		return "", err
	}
	declaration := arguments.IncludeDeclaration == nil || *arguments.IncludeDeclaration
	locations, err := queries.References(ctx, path, position, declaration, languageReader(invocation))
	if err != nil {
		return "", err
	}
	return renderLanguage(invocation, renderLocations(locations, "reference"))
}

func executeLSPDocumentSymbols(ctx context.Context, invocation agent.Invocation) (string, error) {
	queries, err := languageQueries(invocation)
	if err != nil {
		return "", err
	}
	var arguments lspPathArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, err := requireString("path", arguments.Path)
	if err != nil {
		return "", err
	}
	symbols, err := queries.DocumentSymbols(ctx, path, languageReader(invocation))
	if err != nil {
		return "", err
	}
	return renderLanguage(invocation, renderSymbols(symbols))
}

func executeLSPWorkspaceSymbols(ctx context.Context, invocation agent.Invocation) (string, error) {
	queries, err := languageQueries(invocation)
	if err != nil {
		return "", err
	}
	var arguments lspQueryArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	query, err := requireString("query", arguments.Query)
	if err != nil {
		return "", err
	}
	symbols, err := queries.WorkspaceSymbols(ctx, query, languageReader(invocation))
	if err != nil {
		return "", err
	}
	return renderLanguage(invocation, renderSymbols(symbols))
}

func executeLSPDiagnostics(ctx context.Context, invocation agent.Invocation) (string, error) {
	queries, err := languageQueries(invocation)
	if err != nil {
		return "", err
	}
	var arguments lspPathArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, err := requireString("path", arguments.Path)
	if err != nil {
		return "", err
	}
	report, err := queries.Diagnostics(ctx, path, languageReader(invocation))
	if err != nil {
		return "", err
	}
	return renderLanguage(invocation, renderDiagnostics(report))
}

func renderLanguage(invocation agent.Invocation, content string) (string, error) {
	rendered, err := workspace.RenderText(
		invocation.SpillDir, "lsp", invocation.CallID, content, lspInlineBytes,
	)
	if err != nil {
		return "", err
	}
	if rendered.Spilled != "" && invocation.ReportSpill != nil {
		invocation.ReportSpill(rendered.Spilled)
	}
	return rendered.Content, nil
}

func renderLocations(locations lsp.Locations, noun string) string {
	if len(locations.Items) == 0 {
		return "no " + noun + " found" + omittedNote(locations.Omitted)
	}
	var out strings.Builder
	for _, item := range locations.Items {
		fmt.Fprintf(&out, "%s:%s\n", item.Path, item.Range.Start)
	}
	return strings.TrimRight(out.String(), "\n") + omittedNote(locations.Omitted)
}

func renderSymbols(symbols lsp.Symbols) string {
	if len(symbols.Items) == 0 {
		return "no symbols found" + omittedNote(symbols.Omitted)
	}
	var out strings.Builder
	for _, item := range symbols.Items {
		name := item.Name
		if item.Container != "" {
			name = item.Container + "." + item.Name
		}
		fmt.Fprintf(
			&out, "%s %s %s:%s-%s\n",
			symbolKind(item.Kind), name, item.Path, item.Range.Start, item.Range.End,
		)
	}
	return strings.TrimRight(out.String(), "\n") + omittedNote(symbols.Omitted)
}

func renderDiagnostics(report lsp.DiagnosticReport) string {
	header := fmt.Sprintf("version %d, analysis complete", report.Version)
	if !report.Complete {
		header = fmt.Sprintf(
			"version %d, published diagnostics only; analysis may still be running",
			report.Version,
		)
	}
	if len(report.Items) == 0 {
		return "no diagnostics (" + header + ")" + omittedNote(report.Omitted)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", header)
	for _, item := range report.Items {
		fmt.Fprintf(
			&out, "%s %s:%s %s%s\n",
			diagnosticSeverity(item.Severity), item.Path, item.Range.Start,
			item.Message, diagnosticOrigin(item.Source, item.Code),
		)
	}
	return strings.TrimRight(out.String(), "\n") + omittedNote(report.Omitted)
}

func diagnosticOrigin(source, code string) string {
	switch {
	case source != "" && code != "":
		return " [" + source + " " + code + "]"
	case source != "":
		return " [" + source + "]"
	case code != "":
		return " [" + code + "]"
	}
	return ""
}

// omittedNote reports results the adapter dropped because they fell outside the
// workspace or could not be read, so a short answer is never mistaken for a
// complete one.
func omittedNote(omitted int) string {
	if omitted == 0 {
		return ""
	}
	noun := "results"
	if omitted == 1 {
		noun = "result"
	}
	return fmt.Sprintf(
		"\n[%d %s omitted: outside the workspace or unreadable]", omitted, noun,
	)
}

func symbolKind(kind int) string {
	switch kind {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 15:
		return "string"
	case 16:
		return "number"
	case 17:
		return "boolean"
	case 18:
		return "array"
	case 19:
		return "object"
	case 20:
		return "key"
	case 21:
		return "null"
	case 22:
		return "enum-member"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type-parameter"
	}
	return "kind(" + strconv.Itoa(kind) + ")"
}

func diagnosticSeverity(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	}
	return "severity(" + strconv.Itoa(severity) + ")"
}
