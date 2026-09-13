package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

const editDescription = "Replace exact text in an existing UTF-8 file. old_string must occur " +
	"exactly once unless replace_all is true. Matching is literal; read the file after a miss or " +
	"an ambiguous match. " +
	"old_string and new_string adopt the file's line endings, and every byte outside the replaced " +
	"text is left as it was."

const editSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string", "description": "Path to the file to edit."},
		"old_string": {"type": "string", "description": "Exact text to replace."},
		"new_string": {"type": "string", "description": "Replacement text. May be empty."},
		"replace_all": {"type": "boolean", "description": "Replace every occurrence. Defaults to false."}
	},
	"required": ["path", "old_string", "new_string"],
	"additionalProperties": false
}`

type editArguments struct {
	Path       *string `json:"path"`
	OldString  *string `json:"old_string"`
	NewString  *string `json:"new_string"`
	ReplaceAll *bool   `json:"replace_all"`
}

func executeEdit(ctx context.Context, invocation agent.Invocation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var arguments editArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, err := requireString("path", arguments.Path)
	if err != nil {
		return "", err
	}
	oldString, err := requireString("old_string", arguments.OldString)
	if err != nil {
		return "", err
	}
	newString, err := requireString("new_string", arguments.NewString)
	if err != nil {
		return "", err
	}
	if oldString == "" {
		return "", fmt.Errorf("`old_string` must not be empty")
	}
	if oldString == newString {
		return "", fmt.Errorf("`old_string` and `new_string` must be different")
	}
	replaceAll := arguments.ReplaceAll != nil && *arguments.ReplaceAll
	files := workspace.NewWorkspace(invocation.Root).WithReadable(invocation.SpillDir)
	_, ok := files.Key(path)
	if !ok {
		return "", fmt.Errorf("`%s` is outside the workspace", path)
	}
	if invocation.FileSystem.WriteTextFile != nil {
		return executeDelegatedEdit(
			ctx,
			invocation,
			files,
			path,
			oldString,
			newString,
			replaceAll,
		)
	}
	return executeLocalEdit(files, path, oldString, newString, replaceAll)
}

func executeLocalEdit(
	files *workspace.Workspace,
	path string,
	oldString string,
	newString string,
	replaceAll bool,
) (string, error) {
	var written []byte
	replacements := 0
	err := files.Edit(path, func(current []byte) ([]byte, error) {
		var err error
		written, replacements, err = replaceExact(path, current, oldString, newString, replaceAll)
		return written, err
	})
	if err != nil && !workspace.MutationCommitted(err) {
		return "", err
	}
	return mutationResult(
		fmt.Sprintf("Edited %s (%d replacement(s))", path, replacements),
		err,
	), nil
}

func executeDelegatedEdit(
	ctx context.Context,
	invocation agent.Invocation,
	files *workspace.Workspace,
	path string,
	oldString string,
	newString string,
	replaceAll bool,
) (string, error) {
	absolute, err := workspace.NewWorkspace(invocation.Root).Resolve(path)
	if err != nil {
		return "", err
	}
	current, err := acquireText(ctx, files, path, absolute, invocation.FileSystem.ReadTextFile)
	if err != nil {
		return "", err
	}
	written, replacements, err := replaceExact(path, current, oldString, newString, replaceAll)
	if err != nil {
		return "", err
	}
	if err := invocation.FileSystem.WriteTextFile(ctx, absolute, string(written)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited %s (%d replacement(s))", path, replacements), nil
}

func replaceExact(
	path string,
	current []byte,
	oldString string,
	newString string,
	replaceAll bool,
) ([]byte, int, error) {
	if !utf8.Valid(current) {
		return nil, 0, fmt.Errorf("cannot edit `%s`: file is not valid UTF-8", path)
	}
	// The body is spliced, not rewritten: only old_string and new_string are
	// converted to the file's dominant line ending, so every byte outside a
	// replaced span survives, including mixed endings and trailing blank lines.
	body, state := inspectText(current)
	oldText := convertEnding(oldString, state.ending)
	newText := convertEnding(newString, state.ending)
	if oldText == newText {
		return nil, 0, fmt.Errorf("`old_string` and `new_string` must be different")
	}
	matches := matchSites(body, oldText)
	if len(matches) == 0 {
		return nil, 0, missingMatch(path, body, oldText)
	}
	if !replaceAll && len(matches) != 1 {
		return nil, 0, ambiguousMatch(path, body, matches)
	}
	replacements := 1
	if replaceAll {
		replacements = len(matches)
	}
	edited := strings.Replace(body, oldText, newText, replacements)
	if state.bom {
		edited = string(utf8BOM) + edited
	}
	return []byte(edited), replacements, nil
}

// matchSites reports where strings.Replace will replace oldText. It consumes
// each match, so overlapping candidates are not separate replacements.
func matchSites(content, oldText string) []int {
	var sites []int
	for offset := 0; offset <= len(content)-len(oldText); {
		index := strings.Index(content[offset:], oldText)
		if index < 0 {
			break
		}
		index += offset
		sites = append(sites, index)
		offset = index + len(oldText)
	}
	return sites
}

func missingMatch(path, content, oldText string) error {
	first := strings.Split(convertEnding(oldText, "\n"), "\n")[0]
	lines := splitLines(convertEnding(content, "\n"))
	nearest := -1
	if first != "" {
		for index, line := range lines {
			if line != "" &&
				(strings.Contains(line, first) || strings.Contains(first, line)) {
				nearest = index
				break
			}
		}
	}
	message := fmt.Sprintf(
		"`old_string` was not found in `%s`; read the file and supply exact text",
		path,
	)
	if nearest < 0 {
		return fmt.Errorf("%s", message)
	}
	start := max(0, nearest-2)
	end := min(len(lines), nearest+3)
	var window strings.Builder
	for index := start; index < end; index++ {
		fmt.Fprintf(&window, "\n%d: %s", index+1, lines[index])
	}
	return fmt.Errorf("%s; nearest matching line:%s", message, window.String())
}

func ambiguousMatch(path, content string, sites []int) error {
	line := func(site int) int {
		return strings.Count(content[:site], "\n") + 1
	}
	return fmt.Errorf(
		"`old_string` occurs %d times in `%s` (first matches at lines %d and %d); "+
			"read the file and supply unique exact text or set `replace_all`",
		len(sites),
		path,
		line(sites[0]),
		line(sites[1]),
	)
}
