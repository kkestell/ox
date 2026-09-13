package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

const globDescription = "Find files by glob pattern under a search path, one per line. " +
	"Honors .gitignore, skips hidden files, and is confined to the workspace."

const globSchema = `{
	"type": "object",
	"properties": {
		"pattern": {"type": "string", "description": "Glob pattern, for example **/*.go."},
		"path": {"type": "string", "description": "Directory to search. Defaults to the workspace root."}
	},
	"required": ["pattern"],
	"additionalProperties": false
}`

type globArguments struct {
	Pattern *string `json:"pattern"`
	Path    *string `json:"path"`
}

func executeGlob(ctx context.Context, invocation agent.Invocation) (string, error) {
	var arguments globArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	pattern, err := requireString("pattern", arguments.Pattern)
	if err != nil {
		return "", err
	}
	rootArg := "."
	if arguments.Path != nil {
		rootArg = *arguments.Path
	}
	normalized := normalizeGlob(pattern)
	if !doublestar.ValidatePattern(normalized) {
		return "", fmt.Errorf("invalid glob: %s", pattern)
	}

	files := workspace.NewWorkspace(invocation.Root).WithReadable(invocation.SpillDir)
	var collected workspace.Capped
	if err := files.WalkFiles(ctx, rootArg, func(file workspace.WalkedFile) bool {
		if !globMatches(normalized, workspace.RelativeTo(rootArg, file.Display)) {
			return true
		}
		return collected.Push(file.Display)
	}); err != nil {
		return "", err
	}
	sort.Strings(collected.Lines)
	rendered, err := workspace.RenderLines(
		invocation.SpillDir,
		"glob",
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

func normalizeGlob(pattern string) string {
	if !strings.Contains(pattern, "/") {
		return "**/" + pattern
	}
	return pattern
}

// globMatches takes an already normalized pattern, so a walk does not reshape
// the same pattern for every file it visits.
func globMatches(normalized, relative string) bool {
	matched, err := doublestar.Match(normalized, relative)
	return err == nil && matched
}
