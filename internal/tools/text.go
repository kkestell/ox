package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

type textState struct {
	bom             bool
	ending          string
	trailingNewline bool
}

func inspectText(data []byte) (string, textState) {
	state := textState{}
	if bytes.HasPrefix(data, utf8BOM) {
		state.bom = true
		data = data[len(utf8BOM):]
	}
	content := string(data)
	state.ending = dominantEnding(content)
	state.trailingNewline = strings.HasSuffix(content, "\n")
	return content, state
}

func dominantEnding(content string) string {
	crlf := strings.Count(content, "\r\n")
	lf := strings.Count(content, "\n") - crlf
	if crlf > 0 && crlf >= lf {
		return "\r\n"
	}
	return "\n"
}

func convertEnding(content, ending string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if ending == "\n" {
		return content
	}
	return strings.ReplaceAll(content, "\n", ending)
}

func restoreText(content string, state textState) []byte {
	content = convertEnding(content, state.ending)
	if state.trailingNewline {
		if !strings.HasSuffix(content, state.ending) {
			content += state.ending
		}
	} else {
		for strings.HasSuffix(content, state.ending) {
			content = strings.TrimSuffix(content, state.ending)
		}
	}
	data := []byte(content)
	if state.bom {
		data = append(append([]byte(nil), utf8BOM...), data...)
	}
	return data
}

func lineCount(content string) int {
	return len(splitLines(convertEnding(content, "\n")))
}

// recordedReadHash returns the evidence a mutation of an existing file needs.
// A delegating path calls this before it asks the client for content, so a
// blind mutation is refused without dispatching anything. verb names the
// refused operation.
func recordedReadHash(reads agent.FileReads, key, path, verb string) (string, error) {
	if reads == nil {
		return "", fmt.Errorf("cannot %s `%s`: read_file has not read its current contents", verb, path)
	}
	hash, read := reads.Hash(key)
	if !read {
		return "", fmt.Errorf(
			"cannot %s `%s`: read_file has not read its current contents; read it first",
			verb, path,
		)
	}
	return hash, nil
}

// matchesReadEvidence reports whether current is still what the recorded read
// saw, which is what makes a stale mutation visible.
func matchesReadEvidence(want string, current []byte, path, verb string) error {
	sum := sha256.Sum256(current)
	if want != hex.EncodeToString(sum[:]) {
		return fmt.Errorf(
			"cannot %s `%s`: the file changed since read_file read it; read it again",
			verb, path,
		)
	}
	return nil
}

// requireReadEvidence refuses to change an existing file the model has not
// read, or has read before something else changed it.
func requireReadEvidence(reads agent.FileReads, key, path, verb string, current []byte) error {
	want, err := recordedReadHash(reads, key, path, verb)
	if err != nil {
		return err
	}
	return matchesReadEvidence(want, current, path, verb)
}

func acquireText(
	ctx context.Context,
	files *workspace.Workspace,
	path string,
	absolute string,
	read func(context.Context, string, *int, *int) (string, error),
) ([]byte, error) {
	if read == nil {
		return files.ReadFile(path)
	}
	content, err := read(ctx, absolute, nil, nil)
	if err != nil {
		return nil, err
	}
	return boundedText(path, content)
}

// boundedText holds client-supplied text to the same size the local read
// boundary enforces, so delegation cannot widen the limit.
func boundedText(path, content string) ([]byte, error) {
	if len(content) > workspace.MaxFileBytes {
		return nil, workspace.OversizeError(path)
	}
	return []byte(content), nil
}
