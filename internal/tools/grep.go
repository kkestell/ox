package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

const (
	maxScanLineBytes = 4 * 1024 * 1024
	scanCheckLines   = 1024
)

const grepDescription = "Search UTF-8 file contents with a regular expression. " +
	"`output_mode` is `content` (default), `files_with_matches`, or `count`. " +
	"Honors .gitignore, skips hidden files, and is confined to the workspace."

const grepSchema = `{
	"type": "object",
	"properties": {
		"pattern": {"type": "string", "description": "Regular expression to search for."},
		"path": {"type": "string", "description": "Directory or file to search. Defaults to the workspace root."},
		"glob": {"type": "string", "description": "Optional glob filter relative to the search path."},
		"output_mode": {"type": "string", "enum": ["content", "files_with_matches", "count"]}
	},
	"required": ["pattern"],
	"additionalProperties": false
}`

type grepArguments struct {
	Pattern    *string `json:"pattern"`
	Path       *string `json:"path"`
	Glob       *string `json:"glob"`
	OutputMode *string `json:"output_mode"`
}

func executeGrep(ctx context.Context, invocation agent.Invocation) (string, error) {
	var arguments grepArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	pattern, err := requireString("pattern", arguments.Pattern)
	if err != nil {
		return "", err
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %s", pattern)
	}
	rootArg := "."
	if arguments.Path != nil {
		rootArg = *arguments.Path
	}
	if arguments.Glob != nil && !doublestar.ValidatePattern(normalizeGlob(*arguments.Glob)) {
		return "", fmt.Errorf("invalid glob: %s", *arguments.Glob)
	}
	mode := "content"
	if arguments.OutputMode != nil {
		mode = *arguments.OutputMode
	}
	switch mode {
	case "content", "files_with_matches", "count":
	default:
		return "", fmt.Errorf(
			"invalid output_mode `%s` — expected content, files_with_matches, or count",
			mode,
		)
	}

	files := workspace.NewWorkspace(invocation.Root).WithReadable(invocation.SpillDir)
	var collected workspace.Capped
	err = files.WalkFiles(ctx, rootArg, func(file workspace.WalkedFile) bool {
		if arguments.Glob != nil &&
			!globMatches(*arguments.Glob, workspace.RelativeTo(rootArg, file.Display)) {
			return true
		}
		entries, over := scanFile(ctx, file, expression, mode, collected.Remaining())
		for _, entry := range entries {
			if !collected.Push(entry) {
				return false
			}
		}
		if over {
			collected.Truncated = true
			return false
		}
		return ctx.Err() == nil
	})
	if err != nil {
		return "", err
	}
	rendered, err := workspace.RenderLines(
		invocation.SpillDir,
		"grep",
		invocation.CallID,
		collected.Lines,
		collected.Truncated,
	)
	if err != nil {
		return "", err
	}
	if rendered.Spilled != "" && invocation.ReportSpill != nil {
		invocation.ReportSpill(rendered.Spilled)
	}
	return rendered.Content, nil
}

func scanFile(
	ctx context.Context,
	source workspace.WalkedFile,
	expression *regexp.Regexp,
	mode string,
	budget int,
) (entries []string, capped bool) {
	file, err := source.Open()
	if err != nil {
		return nil, false
	}
	defer func() {
		_ = file.Close()
	}()

	reader := bufio.NewReader(file)
	var lineBuffer []byte
	entryBytes := 0
	lineNumber := 0
	count := 0
	matched := false
	for {
		if lineNumber%scanCheckLines == 0 && ctx.Err() != nil {
			return nil, false
		}
		line, readErr := readBoundedLine(reader, &lineBuffer)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, false
		}
		lineNumber++
		switch mode {
		case "content":
			if expression.Match(line) {
				entry := fmt.Sprintf("%s:%d: %s", source.Display, lineNumber, line)
				if entryBytes+len(entry)+1 > budget {
					return entries, true
				}
				entryBytes += len(entry) + 1
				entries = append(entries, entry)
			}
		case "files_with_matches", "count":
			if !matched || mode == "count" {
				if expression.Match(line) {
					matched = true
					count++
				}
			}
		}
	}
	switch mode {
	case "files_with_matches":
		if matched {
			entries = append(entries, source.Display)
		}
	case "count":
		if count > 0 {
			entries = append(entries, fmt.Sprintf("%s: %d", source.Display, count))
		}
	}
	return entries, false
}

var (
	errLineTooLong = errors.New("line exceeds the scan bound")
	errInvalidText = errors.New("line is not valid UTF-8")
)

func readBoundedLine(reader *bufio.Reader, buffer *[]byte) ([]byte, error) {
	*buffer = (*buffer)[:0]
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(*buffer)+len(chunk) > maxScanLineBytes {
			return nil, errLineTooLong
		}
		*buffer = append(*buffer, chunk...)
		switch {
		case err == nil:
			line := trimLineEnding(*buffer)
			if !utf8.Valid(line) {
				return nil, errInvalidText
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(*buffer) == 0 {
				return nil, io.EOF
			}
			line := trimLineEnding(*buffer)
			if !utf8.Valid(line) {
				return nil, errInvalidText
			}
			return line, nil
		default:
			return nil, err
		}
	}
}

func trimLineEnding(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}
