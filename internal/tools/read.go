package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

const readDescription = "Read a UTF-8 text file. A small file is returned in full; a large one " +
	"is windowed to a bounded number of lines with a footer reporting the total. " +
	"Use `offset` (1-based) and `limit` to page through a file or spilled tool output. " +
	"Paths are confined to the workspace and session spill directory."

const readSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string", "description": "Path to the file to read."},
		"offset": {"type": "integer", "minimum": 1, "description": "1-based line number. Defaults to 1."},
		"limit": {"type": "integer", "minimum": 1, "description": "Maximum lines. Defaults to 200."}
	},
	"required": ["path"],
	"additionalProperties": false
}`

type readArguments struct {
	Path   *string `json:"path"`
	Offset *int    `json:"offset"`
	Limit  *int    `json:"limit"`
}

type windowed struct {
	content  string
	start    int
	end      int
	total    int
	windowed bool
}

func executeRead(ctx context.Context, invocation agent.Invocation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var arguments readArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, err := requireString("path", arguments.Path)
	if err != nil {
		return "", err
	}
	offset, err := positiveInt("offset", arguments.Offset, 1)
	if err != nil {
		return "", err
	}
	limit, err := positiveInt("limit", arguments.Limit, workspace.InlineMaxLines)
	if err != nil {
		return "", err
	}

	files := workspace.NewWorkspace(invocation.Root).WithReadable(invocation.SpillDir)
	raw, err := files.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf(
			"failed to read file: %s: stream did not contain valid UTF-8",
			path,
		)
	}
	if key, ok := files.Key(path); ok && invocation.FileReads != nil {
		sum := sha256.Sum256(raw)
		invocation.FileReads.Record(key, hex.EncodeToString(sum[:]))
	}
	return window(string(raw), offset, limit).content, nil
}

func positiveInt(name string, value *int, fallback int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if *value < 1 {
		return 0, fmt.Errorf("`%s` must be an integer >= 1", name)
	}
	return *value, nil
}

func window(content string, offset, limit int) windowed {
	if content == "" {
		return windowed{}
	}

	lines := splitLines(content)
	total := len(lines)
	start := offset - 1
	if start >= total {
		return windowed{
			content:  fmt.Sprintf("[offset %d is past the end of the file (%d lines)]", offset, total),
			start:    offset,
			end:      offset,
			total:    total,
			windowed: true,
		}
	}

	end := start
	bytes := 0
	for end < total && end-start < limit {
		cost := len(lines[end])
		if end > start {
			cost++
		}
		if end > start && bytes+cost > workspace.InlineMaxBytes {
			break
		}
		bytes += cost
		end++
	}
	if start == 0 && end == total {
		return windowed{
			content: content,
			start:   1,
			end:     total,
			total:   total,
		}
	}

	output := strings.Join(lines[start:end], "\n")
	output += fmt.Sprintf(
		"\n[lines %d-%d of %d; pass offset/limit to read more]",
		start+1,
		end,
		total,
	)
	return windowed{
		content:  output,
		start:    start + 1,
		end:      end,
		total:    total,
		windowed: true,
	}
}

func splitLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for index, line := range lines {
		lines[index] = strings.TrimSuffix(line, "\r")
	}
	return lines
}
