package agent

import (
	"context"

	"github.com/kkestell/ox/internal/lsp"
)

// LanguageQueries is an activation's language-server access. It deliberately
// omits shutdown: the session owns the manager's lifetime, and a tool owns only
// the questions it asks.
type LanguageQueries interface {
	Definition(context.Context, string, lsp.Position, lsp.TextReader) (lsp.Locations, error)
	References(context.Context, string, lsp.Position, bool, lsp.TextReader) (lsp.Locations, error)
	DocumentSymbols(context.Context, string, lsp.TextReader) (lsp.Symbols, error)
	WorkspaceSymbols(context.Context, string, lsp.TextReader) (lsp.Symbols, error)
	Diagnostics(context.Context, string, lsp.TextReader) (lsp.DiagnosticReport, error)
}

// languageExtensions lists every extension a configured language server owns.
// Definition order is stable, and settings validation already normalizes the
// extensions and rejects two servers claiming one, so the result is exactly
// what a session's language tools can answer for.
func languageExtensions(definitions []lsp.Definition) []string {
	var extensions []string
	for _, definition := range definitions {
		extensions = append(extensions, definition.Extensions...)
	}
	return extensions
}

// activateLanguages builds an activation's language-server manager. It starts
// no process: a server comes up on the first query for a file it owns.
func (a *Agent) activateLanguages(root string) (*lsp.Manager, error) {
	if len(a.languageServers) == 0 {
		return nil, nil
	}
	return lsp.New(root, a.languageServers)
}

// languagesFor reports the session's query access, or a nil interface when no
// language server is configured. The tool catalog does not depend on this, so a
// language tool called without configuration fails with a clear message.
func (s *session) languagesFor() LanguageQueries {
	if s.languages == nil {
		return nil
	}
	return s.languages
}
