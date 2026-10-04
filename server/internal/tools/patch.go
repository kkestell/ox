package tools

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"ox/internal/transcript"
)

//go:embed apply_patch.md
var patchDescription string

func patchSchema() json.RawMessage {
	schema, err := json.Marshal(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        ApplyPatch,
			"description": patchDescription,
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patch": map[string]any{
						"type":        "string",
						"description": "A patch beginning with *** Begin Patch and ending with *** End Patch.",
					},
				},
				"required":             []string{"patch"},
				"additionalProperties": false,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return schema
}

type fileOperation struct {
	path string
	kind operationKind
	// contents is an Add's file contents.
	contents string
	// destination is an Update's Move to path, or empty.
	destination string
	chunks      []chunk
}

type operationKind int

const (
	addFile operationKind = iota
	deleteFile
	updateFile
)

// chunk replaces the lines before with the lines after, searching from its
// anchor line when it has one.
type chunk struct {
	anchor        *string
	before, after []string
}

// patchParser is a cursor over a patch's lines. Each method reads the part of
// the patch format it names and leaves index on the first line after it.
type patchParser struct {
	lines []string
	index int
}

func parsePatch(input string) ([]fileOperation, error) {
	p := patchParser{lines: textLines(input)}
	if p.line() != "*** Begin Patch" {
		return nil, p.error(0, "expected *** Begin Patch")
	}
	p.index++
	var operations []fileOperation
	for p.index < len(p.lines) {
		if p.line() == "*** End Patch" {
			if p.index+1 != len(p.lines) {
				return nil, p.error(p.index+1, "text after *** End Patch")
			}
			return operations, nil
		}
		operation, err := p.fileOperation()
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return nil, p.error(p.index, "expected *** End Patch")
}

func (p *patchParser) line() string {
	if p.index < len(p.lines) {
		return p.lines[p.index]
	}
	return ""
}

func (p *patchParser) error(index int, reason string) error {
	return fmt.Errorf("parse: line %d: %s", index+1, reason)
}

func (p *patchParser) fileOperation() (fileOperation, error) {
	headerIndex := p.index
	header := p.line()
	p.index++
	var operation fileOperation
	if path, ok := strings.CutPrefix(header, "*** Add File: "); ok {
		operation = fileOperation{path: path, kind: addFile}
		for p.index < len(p.lines) && strings.HasPrefix(p.line(), "+") {
			operation.contents += p.line()[1:] + "\n"
			p.index++
		}
	} else if path, ok := strings.CutPrefix(header, "*** Delete File: "); ok {
		operation = fileOperation{path: path, kind: deleteFile}
	} else if path, ok := strings.CutPrefix(header, "*** Update File: "); ok {
		operation = fileOperation{path: path, kind: updateFile}
		if err := p.updateFile(&operation); err != nil {
			return fileOperation{}, err
		}
	} else {
		return fileOperation{}, p.error(headerIndex, "expected a file operation or *** End Patch; invalid header or body prefix")
	}
	if operation.path == "" {
		return fileOperation{}, p.error(headerIndex, "file path is empty")
	}
	return operation, nil
}

func (p *patchParser) updateFile(operation *fileOperation) error {
	if destination, ok := strings.CutPrefix(p.line(), "*** Move to: "); ok {
		if destination == "" {
			return p.error(p.index, "move destination is empty")
		}
		operation.destination = destination
		p.index++
	}
	for {
		chunk, ok, err := p.chunk()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		operation.chunks = append(operation.chunks, chunk)
	}
	if operation.destination == "" && len(operation.chunks) == 0 {
		return p.error(p.index, "Update File requires chunks or Move to")
	}
	return nil
}

// chunk reads one chunk; ok is false when the next line is not a chunk
// header.
func (p *patchParser) chunk() (chunk, bool, error) {
	if p.index >= len(p.lines) {
		return chunk{}, false, nil
	}
	var c chunk
	switch line := p.line(); {
	case line == "@@":
	case strings.HasPrefix(line, "@@ "):
		anchor := line[len("@@ "):]
		c.anchor = &anchor
	default:
		return chunk{}, false, nil
	}
	p.index++
body:
	for ; p.index < len(p.lines); p.index++ {
		line := p.line()
		if line == "" {
			break
		}
		switch line[0] {
		case ' ':
			c.before = append(c.before, line[1:])
			c.after = append(c.after, line[1:])
		case '-':
			c.before = append(c.before, line[1:])
		case '+':
			c.after = append(c.after, line[1:])
		default:
			break body
		}
	}
	if len(c.before) == 0 && len(c.after) == 0 {
		return chunk{}, false, p.error(p.index, "chunk must contain context, removed, or added lines")
	}
	return c, true, nil
}

// textLines splits text at "\n" and "\r\n" line endings. A final line ending
// does not start another line.
func textLines(text string) []string {
	var lines []string
	for line := range strings.Lines(text) {
		if trimmed, ok := strings.CutSuffix(line, "\n"); ok {
			line = strings.TrimSuffix(trimmed, "\r")
		}
		lines = append(lines, line)
	}
	return lines
}

func update(source string, chunks []chunk) (string, error) {
	ending := "\n"
	if first, _, found := strings.Cut(source, "\n"); found && strings.HasSuffix(first, "\r") {
		ending = "\r\n"
	}
	lines := textLines(source)
	cursor := 0
	for i, c := range chunks {
		if c.anchor != nil {
			position := slices.Index(lines[cursor:], *c.anchor)
			if position < 0 {
				return "", fmt.Errorf("chunk %d: anchor not found", i+1)
			}
			cursor += position + 1
		}
		start := cursor
		if len(c.before) == 0 {
			if i == 0 && c.anchor == nil {
				start = len(lines)
			}
		} else {
			position := -1
			for at := cursor; at+len(c.before) <= len(lines); at++ {
				if slices.Equal(lines[at:at+len(c.before)], c.before) {
					position = at
					break
				}
			}
			if position < 0 {
				return "", fmt.Errorf("chunk %d: exact context not found", i+1)
			}
			start = position
		}
		lines = slices.Concat(lines[:start], c.after, lines[start+len(c.before):])
		cursor = start + len(c.after)
	}
	contents := strings.Join(lines, ending)
	if len(lines) > 0 && strings.HasSuffix(source, "\n") {
		contents += ending
	}
	return contents, nil
}

// target identifies a patch target and rejects duplicate operations on it.
func target(root, name string, seen map[string]bool) (string, error) {
	if name == "" {
		return "", errors.New("file path is empty")
	}
	path := filePath(root, name)
	if seen[path] {
		return "", errors.New("duplicate target")
	}
	seen[path] = true
	return path, nil
}

func requireAbsent(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("destination already exists")
}

func readFileText(path string) (string, error) {
	file, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	return string(data), err
}

// change is one checked operation, ready to apply.
type change struct {
	summary string
	// diff is what the client shows when the change alters a file's
	// contents.
	diff  *transcript.Diff
	apply func() error
}

// prepare checks every operation and builds its change,
// so nothing touches the disk unless every operation can apply. The first
// failing operation stops preparation with an error naming its path.
func prepare(root string, operations []fileOperation) ([]change, error) {
	seen := map[string]bool{}
	var changes []change
	for _, operation := range operations {
		c, err := prepareOperation(root, operation, seen)
		if err != nil {
			return nil, fmt.Errorf("prepare: %s: %w", operation.path, err)
		}
		changes = append(changes, c)
	}
	return changes, nil
}

func fileDiff(path string, oldText *string, newText string) *transcript.Diff {
	return &transcript.Diff{Path: path, OldText: oldText, NewText: newText}
}

func prepareOperation(root string, operation fileOperation, seen map[string]bool) (change, error) {
	path, err := target(root, operation.path, seen)
	if err != nil {
		return change{}, err
	}
	switch operation.kind {
	case addFile:
		if err := requireAbsent(path); err != nil {
			return change{}, err
		}
		contents := operation.contents
		return change{
			summary: "Added " + operation.path,
			diff:    fileDiff(path, nil, contents),
			apply:   func() error { return writeContents(path, []byte(contents), true) },
		}, nil
	case deleteFile:
		text, err := readFileText(path)
		if err != nil {
			return change{}, err
		}
		text = strings.ToValidUTF8(text, "�")
		return change{
			summary: "Deleted " + operation.path,
			diff:    fileDiff(path, &text, ""),
			apply:   func() error { return os.Remove(path) },
		}, nil
	}
	return prepareUpdate(root, operation, path, seen)
}

func prepareUpdate(root string, operation fileOperation, path string, seen map[string]bool) (change, error) {
	file, err := openRegular(path)
	if err != nil {
		return change{}, err
	}
	defer file.Close()
	// source and contents are set when the chunks change the file.
	var source, contents string
	changed := false
	if len(operation.chunks) > 0 {
		data, err := io.ReadAll(file)
		if err != nil {
			return change{}, err
		}
		source = string(data)
		if !utf8.ValidString(source) {
			return change{}, errors.New("stream did not contain valid UTF-8")
		}
		if contents, err = update(source, operation.chunks); err != nil {
			return change{}, err
		}
		changed = contents != source
	}
	if operation.destination != "" {
		destination, err := target(root, operation.destination, seen)
		if err == nil {
			err = requireAbsent(destination)
		}
		if err != nil {
			return change{}, fmt.Errorf("destination %s: %w", operation.destination, err)
		}
		c := change{
			summary: fmt.Sprintf("Moved %s -> %s", operation.path, operation.destination),
			apply:   func() error { return moveFile(path, destination) },
		}
		if changed {
			c.diff = fileDiff(destination, &source, contents)
			c.apply = func() error {
				if err := writeContents(destination, []byte(contents), true); err != nil {
					return err
				}
				return os.Remove(path)
			}
		}
		return c, nil
	}
	if !changed {
		return change{summary: "Unchanged " + operation.path, apply: func() error { return nil }}, nil
	}
	return change{
		summary: "Modified " + operation.path,
		diff:    fileDiff(path, &source, contents),
		apply:   func() error { return writeContents(path, []byte(contents), false) },
	}, nil
}

// applyChanges applies the changes in order. Success returns the model's
// summary and, for the client, each operation's summary followed by its diff.
func applyChanges(changes []change) (string, []transcript.ToolContent, error) {
	var completed []string
	for i, c := range changes {
		if err := c.apply(); err != nil {
			var remaining []string
			for _, later := range changes[i+1:] {
				remaining = append(remaining, later.summary)
			}
			return "", nil, fmt.Errorf("apply: failed %s: %v\nThe failed operation may be partially applied.\nCompleted:\n%s\nNot attempted:\n%s",
				c.summary, err, listOrNone(completed), listOrNone(remaining))
		}
		completed = append(completed, c.summary)
	}
	summary := strings.Join(append([]string{"Applied patch."}, completed...), "\n")
	if len(summary) > outputLimit {
		summary = truncate(summary, bodyLimit) + "\nSummary truncated."
	}
	var content []transcript.ToolContent
	for _, c := range changes {
		content = append(content, transcript.ToolContent{Text: c.summary})
		if c.diff != nil {
			content = append(content, transcript.ToolContent{Diff: c.diff})
		}
	}
	return summary, content, nil
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, "\n")
}

// changedPaths returns the paths a patch names, for display. A patch that does
// not parse names none; the failure is reported when the patch runs.
func changedPaths(input string) []string {
	operations, err := parsePatch(input)
	if err != nil {
		return nil
	}
	paths := make([]string, len(operations))
	for i, operation := range operations {
		paths[i] = operation.path
	}
	return paths
}

func applyPatch(root, input string) (string, []transcript.ToolContent, error) {
	operations, err := parsePatch(input)
	if err != nil {
		return "", nil, err
	}
	changes, err := prepare(root, operations)
	if err != nil {
		return "", nil, err
	}
	return applyChanges(changes)
}
