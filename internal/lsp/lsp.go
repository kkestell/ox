// Package lsp owns the lifecycle and protocol boundary for configured language
// servers. It deliberately exposes model-facing, root-relative values rather
// than raw LSP wire types.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/workspace"
)

const (
	initializeTimeout = 30 * time.Second
	queryTimeout      = 10 * time.Second
	shutdownTimeout   = 2 * time.Second
)

// Definition is one trusted process-level language-server configuration.
type Definition struct {
	Name       string
	Command    string
	Args       []string
	Extensions []string
}

// TextReader returns the authoritative UTF-8 contents of an absolute path.
type TextReader func(context.Context, string) (string, error)

type Position struct {
	Line   int
	Column int
}

type Range struct {
	Start Position
	End   Position
}

type Location struct {
	Path  string
	Range Range
}

type Locations struct {
	Items   []Location
	Omitted int
}

type Symbol struct {
	Name      string
	Kind      int
	Container string
	Path      string
	Range     Range
}

type Symbols struct {
	Items   []Symbol
	Omitted int
}

type Diagnostic struct {
	Path     string
	Range    Range
	Severity int
	Message  string
	Source   string
	Code     string
}

// DiagnosticReport identifies the exact local document version observed. A
// push report is an observation, not proof that analysis has finished.
type DiagnosticReport struct {
	Version  int
	Complete bool
	Items    []Diagnostic
	Omitted  int
}

type Manager struct {
	root      string
	workspace *workspace.Workspace
	servers   []*serverState
	byExt     map[string]*serverState

	ctx               context.Context
	cancel            context.CancelFunc
	initializeTimeout time.Duration
	queryTimeout      time.Duration
	shutdownTimeout   time.Duration
	closeOnce         sync.Once
	closeErr          error
}

type serverState struct {
	definition Definition
	manager    *Manager

	mu       sync.Mutex
	starting bool
	ready    chan struct{}
	client   *client
	err      error
}

// New constructs a lazy activation manager. It starts no child process.
func New(root string, definitions []Definition) (*Manager, error) {
	canonical, err := workspace.Canonical(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		root: canonical, workspace: workspace.NewWorkspace(canonical),
		byExt: make(map[string]*serverState), ctx: ctx, cancel: cancel,
		initializeTimeout: initializeTimeout, queryTimeout: queryTimeout,
		shutdownTimeout: shutdownTimeout,
	}
	definitions = slices.Clone(definitions)
	slices.SortFunc(definitions, func(a, b Definition) int { return strings.Compare(a.Name, b.Name) })
	for _, definition := range definitions {
		definition.Name = strings.TrimSpace(definition.Name)
		definition.Command = strings.TrimSpace(definition.Command)
		definition.Args = slices.Clone(definition.Args)
		definition.Extensions = slices.Clone(definition.Extensions)
		if definition.Name == "" {
			cancel()
			return nil, errors.New("language server name must not be blank")
		}
		if definition.Command == "" {
			cancel()
			return nil, fmt.Errorf("language server %q command must not be blank", definition.Name)
		}
		state := &serverState{definition: definition, manager: m}
		seen := make(map[string]bool)
		for index, extension := range definition.Extensions {
			extension = normalizeExtension(extension)
			if extension == "" || strings.ContainsAny(extension, `/\\`) {
				cancel()
				return nil, fmt.Errorf("language server %q has invalid extension %q", definition.Name, definition.Extensions[index])
			}
			if seen[extension] {
				cancel()
				return nil, fmt.Errorf("language server %q repeats extension %q", definition.Name, extension)
			}
			seen[extension] = true
			definition.Extensions[index] = extension
			if previous := m.byExt[extension]; previous != nil {
				cancel()
				return nil, fmt.Errorf("language servers %q and %q both own extension %q", previous.definition.Name, definition.Name, extension)
			}
			m.byExt[extension] = state
		}
		if len(definition.Extensions) == 0 {
			cancel()
			return nil, fmt.Errorf("language server %q must configure at least one extension", definition.Name)
		}
		state.definition = definition
		m.servers = append(m.servers, state)
	}
	return m, nil
}

func normalizeExtension(extension string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))
}

func (m *Manager) stateFor(path string) (*serverState, error) {
	extension := normalizeExtension(filepath.Ext(path))
	if extension == "" {
		return nil, fmt.Errorf("no language server is configured for %q", path)
	}
	state := m.byExt[extension]
	if state == nil {
		return nil, fmt.Errorf("no language server is configured for extension %q", extension)
	}
	return state, nil
}

func (s *serverState) get(ctx context.Context) (*client, error) {
	s.mu.Lock()
	if s.client != nil || s.err != nil {
		client, err := s.client, s.err
		s.mu.Unlock()
		return client, err
	}
	if s.starting {
		ready := s.ready
		s.mu.Unlock()
		select {
		case <-ready:
			s.mu.Lock()
			client, err := s.client, s.err
			s.mu.Unlock()
			return client, err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.manager.ctx.Done():
			return nil, errors.New("language-server activation is closed")
		}
	}
	s.starting = true
	s.ready = make(chan struct{})
	ready := s.ready
	s.mu.Unlock()

	client, err := startClient(ctx, s.manager, s.definition)
	if err != nil {
		err = fmt.Errorf("start language server %q: %w", s.definition.Name, err)
	}
	s.mu.Lock()
	s.client, s.err, s.starting = client, err, false
	close(ready)
	s.mu.Unlock()
	return client, err
}

func (m *Manager) resolve(path string) (string, error) {
	if filepath.IsAbs(path) {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		path = canonical
	}
	return m.workspace.Resolve(path)
}

func readText(ctx context.Context, reader TextReader, path string) (string, error) {
	if reader == nil {
		return "", errors.New("language-server query has no file reader")
	}
	text, err := reader(ctx, path)
	if err != nil {
		return "", err
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("language-server input %q is not valid UTF-8", path)
	}
	return text, nil
}

func (m *Manager) Definition(ctx context.Context, path string, position Position, reader TextReader) (Locations, error) {
	return m.locations(ctx, "textDocument/definition", path, position, false, reader)
}

func (m *Manager) References(ctx context.Context, path string, position Position, includeDeclaration bool, reader TextReader) (Locations, error) {
	return m.locations(ctx, "textDocument/references", path, position, includeDeclaration, reader)
}

func (m *Manager) locations(ctx context.Context, method, path string, position Position, includeDeclaration bool, reader TextReader) (Locations, error) {
	absolute, err := m.resolve(path)
	if err != nil {
		return Locations{}, err
	}
	text, err := readText(ctx, reader, absolute)
	if err != nil {
		return Locations{}, err
	}
	state, err := m.stateFor(absolute)
	if err != nil {
		return Locations{}, err
	}
	client, err := state.get(ctx)
	if err != nil {
		return Locations{}, err
	}
	raw, err := client.locations(ctx, method, absolute, text, position, includeDeclaration)
	if err != nil {
		return Locations{}, err
	}
	return m.decodeLocations(ctx, raw, reader, client.encoding)
}

func (m *Manager) DocumentSymbols(ctx context.Context, path string, reader TextReader) (Symbols, error) {
	absolute, err := m.resolve(path)
	if err != nil {
		return Symbols{}, err
	}
	text, err := readText(ctx, reader, absolute)
	if err != nil {
		return Symbols{}, err
	}
	state, err := m.stateFor(absolute)
	if err != nil {
		return Symbols{}, err
	}
	client, err := state.get(ctx)
	if err != nil {
		return Symbols{}, err
	}
	raw, err := client.documentSymbols(ctx, absolute, text)
	if err != nil {
		return Symbols{}, err
	}
	return m.decodeDocumentSymbols(ctx, raw, absolute, reader, client.encoding)
}

func (m *Manager) WorkspaceSymbols(ctx context.Context, query string, reader TextReader) (Symbols, error) {
	if len(m.servers) == 0 {
		return Symbols{}, errors.New("no language servers are configured")
	}
	var result Symbols
	for _, state := range m.servers {
		client, err := state.get(ctx)
		if err != nil {
			return Symbols{}, err
		}
		raw, err := client.workspaceSymbols(ctx, query)
		if err != nil {
			return Symbols{}, err
		}
		decoded, err := m.decodeWorkspaceSymbols(ctx, raw, reader, client.encoding)
		if err != nil {
			return Symbols{}, err
		}
		result.Items = append(result.Items, decoded.Items...)
		result.Omitted += decoded.Omitted
	}
	return result, nil
}

func (m *Manager) Diagnostics(ctx context.Context, path string, reader TextReader) (DiagnosticReport, error) {
	absolute, err := m.resolve(path)
	if err != nil {
		return DiagnosticReport{}, err
	}
	text, err := readText(ctx, reader, absolute)
	if err != nil {
		return DiagnosticReport{}, err
	}
	state, err := m.stateFor(absolute)
	if err != nil {
		return DiagnosticReport{}, err
	}
	client, err := state.get(ctx)
	if err != nil {
		return DiagnosticReport{}, err
	}
	raw, version, complete, err := client.diagnostics(ctx, absolute, text)
	if err != nil {
		return DiagnosticReport{}, err
	}
	items, omitted, err := m.decodeDiagnostics(ctx, raw, absolute, reader, client.encoding)
	if err != nil {
		return DiagnosticReport{}, err
	}
	return DiagnosticReport{Version: version, Complete: complete, Items: items, Omitted: omitted}, nil
}

// Close shuts down every server that was actually started.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		for _, state := range m.servers {
			state.mu.Lock()
			client := state.client
			state.mu.Unlock()
			if client != nil {
				m.closeErr = errors.Join(m.closeErr, client.close(m.shutdownTimeout))
			}
		}
		m.cancel()
	})
	return m.closeErr
}
