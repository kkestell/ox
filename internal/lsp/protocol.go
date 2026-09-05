package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type wirePosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type wireRange struct {
	Start wirePosition `json:"start"`
	End   wirePosition `json:"end"`
}

func encodePosition(text string, position Position, encoding encodingKind) (wirePosition, error) {
	if position.Line < 1 || position.Column < 1 {
		return wirePosition{}, errors.New("line and column must be at least 1")
	}
	line, ok := textLine(text, position.Line-1)
	if !ok {
		return wirePosition{}, fmt.Errorf("line %d is outside the document", position.Line)
	}
	runes := []rune(line)
	if position.Column > len(runes)+1 {
		return wirePosition{}, fmt.Errorf("column %d is outside line %d", position.Column, position.Line)
	}
	return wirePosition{Line: position.Line - 1, Character: encodedLength(string(runes[:position.Column-1]), encoding)}, nil
}

func decodePosition(text string, position wirePosition, encoding encodingKind) (Position, error) {
	if position.Line < 0 || position.Character < 0 {
		return Position{}, errors.New("LSP position contains a negative value")
	}
	line, ok := textLine(text, position.Line)
	if !ok {
		return Position{}, fmt.Errorf("LSP line %d is outside the document", position.Line)
	}
	units := 0
	column := 1
	for _, value := range line {
		if units == position.Character {
			return Position{Line: position.Line + 1, Column: column}, nil
		}
		units += encodedRuneLength(value, encoding)
		if units > position.Character {
			return Position{}, fmt.Errorf("LSP character %d splits an encoded character", position.Character)
		}
		column++
	}
	if units == position.Character {
		return Position{Line: position.Line + 1, Column: column}, nil
	}
	return Position{}, fmt.Errorf("LSP character %d is outside line %d", position.Character, position.Line)
}

func decodeRange(text string, value wireRange, encoding encodingKind) (Range, error) {
	start, err := decodePosition(text, value.Start, encoding)
	if err != nil {
		return Range{}, err
	}
	end, err := decodePosition(text, value.End, encoding)
	if err != nil {
		return Range{}, err
	}
	if end.Line < start.Line || (end.Line == start.Line && end.Column < start.Column) {
		return Range{}, errors.New("LSP range ends before it starts")
	}
	return Range{Start: start, End: end}, nil
}

func textLine(text string, wanted int) (string, bool) {
	if wanted < 0 {
		return "", false
	}
	lines := strings.Split(text, "\n")
	if wanted >= len(lines) {
		return "", false
	}
	return strings.TrimSuffix(lines[wanted], "\r"), true
}

func encodedLength(text string, encoding encodingKind) int {
	length := 0
	for _, value := range text {
		length += encodedRuneLength(value, encoding)
	}
	return length
}

func encodedRuneLength(value rune, encoding encodingKind) int {
	switch encoding {
	case encodingUTF8:
		return utf8.RuneLen(value)
	case encodingUTF16:
		return len(utf16.Encode([]rune{value}))
	case encodingUTF32:
		return 1
	default:
		panic("unsupported LSP position encoding")
	}
}

func pathToURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func uriToPath(value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "file" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", false
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || path == "" {
		return "", false
	}
	return filepath.FromSlash(path), true
}

func (m *Manager) resolveURI(uri string) (absolute, relative string, ok bool) {
	path, ok := uriToPath(uri)
	if !ok {
		return "", "", false
	}
	absolute, err := m.resolve(path)
	if err != nil {
		return "", "", false
	}
	relative, ok = m.workspace.Key(absolute)
	if !ok {
		return "", "", false
	}
	return absolute, relative, true
}

type textCache struct {
	ctx    context.Context
	reader TextReader
	values map[string]string
}

func newTextCache(ctx context.Context, reader TextReader) *textCache {
	return &textCache{ctx: ctx, reader: reader, values: make(map[string]string)}
}

func (c *textCache) load(path string) (string, error) {
	if value, ok := c.values[path]; ok {
		return value, nil
	}
	value, err := readText(c.ctx, c.reader, path)
	if err != nil {
		return "", err
	}
	c.values[path] = value
	return value, nil
}

func rawItems(raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return []json.RawMessage{raw}, nil
	}
	return nil, errors.New("LSP result must be an object, array, or null")
}

func (m *Manager) decodeLocations(ctx context.Context, raw json.RawMessage, reader TextReader, encoding encodingKind) (Locations, error) {
	items, err := rawItems(raw)
	if err != nil {
		return Locations{}, fmt.Errorf("decode locations: %w", err)
	}
	cache := newTextCache(ctx, reader)
	var result Locations
	for _, item := range items {
		var value struct {
			URI                  string     `json:"uri"`
			Range                *wireRange `json:"range"`
			TargetURI            string     `json:"targetUri"`
			TargetSelectionRange *wireRange `json:"targetSelectionRange"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			return Locations{}, fmt.Errorf("decode location: %w", err)
		}
		uri, targetRange := value.URI, value.Range
		if value.TargetURI != "" {
			uri, targetRange = value.TargetURI, value.TargetSelectionRange
		}
		if uri == "" || targetRange == nil {
			return Locations{}, errors.New("location omitted its URI or range")
		}
		absolute, relative, ok := m.resolveURI(uri)
		if !ok {
			result.Omitted++
			continue
		}
		text, err := cache.load(absolute)
		if err != nil {
			result.Omitted++
			continue
		}
		decoded, err := decodeRange(text, *targetRange, encoding)
		if err != nil {
			return Locations{}, fmt.Errorf("decode location for %s: %w", relative, err)
		}
		result.Items = append(result.Items, Location{Path: relative, Range: decoded})
	}
	return result, nil
}

type wireSymbol struct {
	Name           string          `json:"name"`
	Kind           int             `json:"kind"`
	Container      string          `json:"containerName"`
	Range          *wireRange      `json:"range"`
	SelectionRange json.RawMessage `json:"selectionRange"`
	Children       []wireSymbol    `json:"children"`
	Location       *struct {
		URI   string     `json:"uri"`
		Range *wireRange `json:"range"`
	} `json:"location"`
}

func (m *Manager) decodeDocumentSymbols(ctx context.Context, raw json.RawMessage, target string, reader TextReader, encoding encodingKind) (Symbols, error) {
	if string(raw) == "null" {
		return Symbols{}, nil
	}
	var values []wireSymbol
	if err := json.Unmarshal(raw, &values); err != nil {
		return Symbols{}, fmt.Errorf("decode document symbols: %w", err)
	}
	cache := newTextCache(ctx, reader)
	text, err := cache.load(target)
	if err != nil {
		return Symbols{}, err
	}
	relative, ok := m.workspace.Key(target)
	if !ok {
		return Symbols{}, errors.New("document symbol target escaped the workspace")
	}
	var result Symbols
	var walk func([]wireSymbol, string) error
	walk = func(symbols []wireSymbol, parent string) error {
		for _, symbol := range symbols {
			if symbol.Name == "" {
				return errors.New("document symbol omitted its name")
			}
			path, body, valueRange := relative, text, symbol.Range
			if symbol.Location != nil {
				absolute, resolved, ok := m.resolveURI(symbol.Location.URI)
				if !ok {
					result.Omitted++
					continue
				}
				var err error
				body, err = cache.load(absolute)
				if err != nil {
					result.Omitted++
					continue
				}
				path, valueRange = resolved, symbol.Location.Range
			}
			if valueRange == nil {
				return fmt.Errorf("document symbol %q omitted its range", symbol.Name)
			}
			decoded, err := decodeRange(body, *valueRange, encoding)
			if err != nil {
				return fmt.Errorf("decode symbol %q: %w", symbol.Name, err)
			}
			container := symbol.Container
			if container == "" {
				container = parent
			}
			result.Items = append(result.Items, Symbol{Name: symbol.Name, Kind: symbol.Kind, Container: container, Path: path, Range: decoded})
			childParent := container
			if childParent == "" {
				childParent = symbol.Name
			} else {
				childParent += "." + symbol.Name
			}
			if err := walk(symbol.Children, childParent); err != nil {
				return err
			}
		}
		return nil
	}
	return result, walk(values, "")
}

func (m *Manager) decodeWorkspaceSymbols(ctx context.Context, raw json.RawMessage, reader TextReader, encoding encodingKind) (Symbols, error) {
	if string(raw) == "null" {
		return Symbols{}, nil
	}
	var values []wireSymbol
	if err := json.Unmarshal(raw, &values); err != nil {
		return Symbols{}, fmt.Errorf("decode workspace symbols: %w", err)
	}
	cache := newTextCache(ctx, reader)
	var result Symbols
	for _, symbol := range values {
		if symbol.Name == "" || symbol.Location == nil {
			return Symbols{}, errors.New("workspace symbol omitted its name or location")
		}
		absolute, relative, ok := m.resolveURI(symbol.Location.URI)
		if !ok {
			result.Omitted++
			continue
		}
		text, err := cache.load(absolute)
		if err != nil {
			result.Omitted++
			continue
		}
		if symbol.Location.Range == nil {
			return Symbols{}, fmt.Errorf("workspace symbol %q omitted its range", symbol.Name)
		}
		decoded, err := decodeRange(text, *symbol.Location.Range, encoding)
		if err != nil {
			return Symbols{}, fmt.Errorf("decode symbol %q: %w", symbol.Name, err)
		}
		result.Items = append(result.Items, Symbol{Name: symbol.Name, Kind: symbol.Kind, Container: symbol.Container, Path: relative, Range: decoded})
	}
	return result, nil
}

type wireDiagnostic struct {
	Range    *wireRange      `json:"range"`
	Severity int             `json:"severity"`
	Message  string          `json:"message"`
	Source   string          `json:"source"`
	Code     json.RawMessage `json:"code"`
}

func diagnosticCode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func (m *Manager) decodeDiagnostics(ctx context.Context, raw json.RawMessage, target string, reader TextReader, encoding encodingKind) ([]Diagnostic, int, error) {
	type report struct {
		Kind             string                     `json:"kind"`
		Items            json.RawMessage            `json:"items"`
		RelatedDocuments map[string]json.RawMessage `json:"relatedDocuments"`
	}
	reports := map[string]json.RawMessage{pathToURI(target): raw}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var value report
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, 0, err
		}
		if value.Kind == "unchanged" {
			return nil, 0, errors.New("diagnostic server returned unchanged without a previous result")
		}
		if len(value.Items) == 0 {
			return nil, 0, errors.New("diagnostic report omitted items")
		}
		reports[pathToURI(target)] = value.Items
		for uri, related := range value.RelatedDocuments {
			var relatedReport report
			if err := json.Unmarshal(related, &relatedReport); err != nil || len(relatedReport.Items) == 0 {
				return nil, 0, errors.New("malformed related diagnostic report")
			}
			reports[uri] = relatedReport.Items
		}
	}
	cache := newTextCache(ctx, reader)
	var diagnostics []Diagnostic
	omitted := 0
	for uri, encodedItems := range reports {
		absolute, relative, ok := m.resolveURI(uri)
		if !ok {
			omitted++
			continue
		}
		text, err := cache.load(absolute)
		if err != nil {
			omitted++
			continue
		}
		var items []wireDiagnostic
		if err := json.Unmarshal(encodedItems, &items); err != nil {
			return nil, 0, errors.New("malformed diagnostic items")
		}
		for _, item := range items {
			if item.Message == "" {
				return nil, 0, errors.New("diagnostic omitted its message")
			}
			if item.Range == nil {
				return nil, 0, errors.New("diagnostic omitted its range")
			}
			valueRange, err := decodeRange(text, *item.Range, encoding)
			if err != nil {
				return nil, 0, fmt.Errorf("decode diagnostic for %s: %w", relative, err)
			}
			diagnostics = append(diagnostics, Diagnostic{Path: relative, Range: valueRange, Severity: item.Severity, Message: item.Message, Source: item.Source, Code: diagnosticCode(item.Code)})
		}
	}
	return diagnostics, omitted, nil
}

// String helpers keep tests and later formatting free from raw protocol names.
func (p Position) String() string { return strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column) }
