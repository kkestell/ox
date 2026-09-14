package agent

import (
	"context"
	"slices"

	"github.com/kkestell/ox/internal/lsp"
	"github.com/kkestell/ox/internal/settings"
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

func languageDefinitions(configured []settings.ResolvedLanguageServer) []lsp.Definition {
	definitions := make([]lsp.Definition, 0, len(configured))
	for _, server := range configured {
		definitions = append(definitions, lsp.Definition{
			Name:       server.Name,
			Command:    server.Command,
			Args:       slices.Clone(server.Args),
			Extensions: slices.Clone(server.Extensions),
		})
	}
	return definitions
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

// sessionLanguages serializes one session's language queries. A query mutates
// the server's view of open documents and their versions, so two concurrent
// queries in one session would interleave those updates; sessions hold separate
// managers and stay independent.
type sessionLanguages struct {
	manager *lsp.Manager
	turn    chan struct{}
}

func newSessionLanguages(manager *lsp.Manager) *sessionLanguages {
	if manager == nil {
		return nil
	}
	return &sessionLanguages{manager: manager, turn: make(chan struct{}, 1)}
}

func (l *sessionLanguages) acquire(ctx context.Context) (func(), error) {
	select {
	case l.turn <- struct{}{}:
		return func() { <-l.turn }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *sessionLanguages) Definition(
	ctx context.Context, path string, position lsp.Position, reader lsp.TextReader,
) (lsp.Locations, error) {
	release, err := l.acquire(ctx)
	if err != nil {
		return lsp.Locations{}, err
	}
	defer release()
	return l.manager.Definition(ctx, path, position, reader)
}

func (l *sessionLanguages) References(
	ctx context.Context, path string, position lsp.Position, includeDeclaration bool, reader lsp.TextReader,
) (lsp.Locations, error) {
	release, err := l.acquire(ctx)
	if err != nil {
		return lsp.Locations{}, err
	}
	defer release()
	return l.manager.References(ctx, path, position, includeDeclaration, reader)
}

func (l *sessionLanguages) DocumentSymbols(
	ctx context.Context, path string, reader lsp.TextReader,
) (lsp.Symbols, error) {
	release, err := l.acquire(ctx)
	if err != nil {
		return lsp.Symbols{}, err
	}
	defer release()
	return l.manager.DocumentSymbols(ctx, path, reader)
}

func (l *sessionLanguages) WorkspaceSymbols(
	ctx context.Context, query string, reader lsp.TextReader,
) (lsp.Symbols, error) {
	release, err := l.acquire(ctx)
	if err != nil {
		return lsp.Symbols{}, err
	}
	defer release()
	return l.manager.WorkspaceSymbols(ctx, query, reader)
}

func (l *sessionLanguages) Diagnostics(
	ctx context.Context, path string, reader lsp.TextReader,
) (lsp.DiagnosticReport, error) {
	release, err := l.acquire(ctx)
	if err != nil {
		return lsp.DiagnosticReport{}, err
	}
	defer release()
	return l.manager.Diagnostics(ctx, path, reader)
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
