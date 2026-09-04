package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

const writeDescription = "Create or replace a UTF-8 text file. Missing parent directories are " +
	"created. Before replacing an existing file, use read_file so the current contents can be " +
	"verified. Existing line endings, UTF-8 BOM, trailing-newline state, and file mode are preserved."

const writeSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string", "description": "Path to the file to create or replace."},
		"content": {"type": "string", "description": "Complete new contents of the file."}
	},
	"required": ["path", "content"],
	"additionalProperties": false
}`

type writeArguments struct {
	Path    *string `json:"path"`
	Content *string `json:"content"`
}

func executeWrite(ctx context.Context, invocation agent.Invocation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var arguments writeArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	path, err := requireString("path", arguments.Path)
	if err != nil {
		return "", err
	}
	content, err := requireString("content", arguments.Content)
	if err != nil {
		return "", err
	}
	files := workspace.NewWorkspace(invocation.Root).WithReadable(invocation.SpillDir)
	key, ok := files.Key(path)
	if !ok {
		return "", fmt.Errorf("`%s` is outside the workspace", path)
	}
	if invocation.FileSystem.WriteTextFile != nil {
		return executeDelegatedWrite(ctx, invocation, files, key, path, content)
	}
	return executeLocalWrite(invocation, files, key, path, content)
}

func executeLocalWrite(
	invocation agent.Invocation,
	files *workspace.Workspace,
	key string,
	path string,
	content string,
) (string, error) {
	var written []byte
	var previousLines int
	err := files.Edit(path, func(current []byte) ([]byte, error) {
		if !utf8.Valid(current) {
			return nil, fmt.Errorf("cannot overwrite `%s`: file is not valid UTF-8", path)
		}
		if invocation.FileReads == nil {
			return nil, fmt.Errorf("cannot overwrite `%s`: read_file has not read its current contents", path)
		}
		want, read := invocation.FileReads.Hash(key)
		if !read {
			return nil, fmt.Errorf(
				"cannot overwrite `%s`: read_file has not read its current contents; read it first",
				path,
			)
		}
		sum := sha256.Sum256(current)
		if want != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf(
				"cannot overwrite `%s`: the file changed since read_file read it; read it again",
				path,
			)
		}
		body, state := inspectText(current)
		previousLines = lineCount(body)
		written = restoreText(convertEnding(content, state.ending), state)
		return written, nil
	})
	created := false
	if errors.Is(err, fs.ErrNotExist) {
		written = []byte(content)
		created, err = files.WriteFile(path, written)
	}
	committed := workspace.MutationCommitted(err)
	if err != nil && !committed {
		return "", err
	}
	if invocation.FileReads != nil {
		sum := sha256.Sum256(written)
		invocation.FileReads.Record(key, hex.EncodeToString(sum[:]))
	}
	if created {
		return mutationResult(
			fmt.Sprintf("Created %s (%d lines)", path, lineCount(content)),
			err,
		), nil
	}
	return mutationResult(fmt.Sprintf(
		"Wrote %s (%d lines, was %d)", path, lineCount(string(written)), previousLines,
	), err), nil
}

func executeDelegatedWrite(
	ctx context.Context,
	invocation agent.Invocation,
	files *workspace.Workspace,
	key string,
	path string,
	content string,
) (string, error) {
	absolute, err := workspace.NewWorkspace(invocation.Root).Resolve(path)
	if err != nil {
		return "", err
	}
	info, statErr := os.Stat(absolute)
	created := errors.Is(statErr, fs.ErrNotExist)
	if statErr != nil && !created {
		return "", fmt.Errorf("cannot access `%s`", path)
	}
	if statErr == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("cannot write `%s`: not a regular file", path)
	}

	written := []byte(content)
	previousLines := 0
	if !created {
		if invocation.FileReads == nil {
			return "", fmt.Errorf("cannot overwrite `%s`: read_file has not read its current contents", path)
		}
		want, read := invocation.FileReads.Hash(key)
		if !read {
			return "", fmt.Errorf(
				"cannot overwrite `%s`: read_file has not read its current contents; read it first",
				path,
			)
		}
		current, err := acquireText(ctx, files, path, absolute, invocation.FileSystem.ReadTextFile)
		if err != nil {
			return "", err
		}
		if !utf8.Valid(current) {
			return "", fmt.Errorf("cannot overwrite `%s`: file is not valid UTF-8", path)
		}
		sum := sha256.Sum256(current)
		if want != hex.EncodeToString(sum[:]) {
			return "", fmt.Errorf(
				"cannot overwrite `%s`: the file changed since read_file read it; read it again",
				path,
			)
		}
		body, state := inspectText(current)
		previousLines = lineCount(body)
		written = restoreText(convertEnding(content, state.ending), state)
	}
	if err := invocation.FileSystem.WriteTextFile(ctx, absolute, string(written)); err != nil {
		return "", err
	}
	if invocation.FileReads != nil {
		sum := sha256.Sum256(written)
		invocation.FileReads.Record(key, hex.EncodeToString(sum[:]))
	}
	if created {
		return fmt.Sprintf("Created %s (%d lines)", path, lineCount(content)), nil
	}
	return fmt.Sprintf(
		"Wrote %s (%d lines, was %d)", path, lineCount(string(written)), previousLines,
	), nil
}

func mutationResult(result string, err error) string {
	if workspace.MutationCommitted(err) {
		return result + "; the file was replaced, but syncing its directory failed: " + err.Error()
	}
	return result
}
