package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"ox/internal/transcript"
)

var globSchema = compact(`{
  "type": "function",
  "function": {
    "name": "glob",
    "description": "Find files using a ripgrep glob. Returns file paths, at most 16 KiB. Narrow the pattern or path if truncated. Uses ripgrep ignore rules; an explicit glob can include hidden and ignored files; does not follow symlinks during traversal. Example: {\"pattern\":\"*.rs\",\"path\":\"src\"}.",
    "parameters": {
      "type": "object",
      "properties": {
        "pattern": {"type": "string", "description": "Ripgrep glob, e.g. *.rs or src/**/*.rs."},
        "path": {"type": "string", "default": ".", "description": "File or directory to search, relative to the workspace or absolute. Globs use ripgrep path matching."}
      },
      "required": ["pattern"],
      "additionalProperties": false
    }
  }
}`)

var grepSchema = compact(`{
  "type": "function",
  "function": {
    "name": "grep",
    "description": "Search text files with a case-sensitive ripgrep regular expression; use (?i) for case-insensitivity. Returns path:line:content, at most 16 KiB. Narrow the pattern or path if truncated. Uses ripgrep ignore rules; explicit paths or globs can include hidden and ignored files; does not follow symlinks during traversal. Example: {\"pattern\":\"fn main\",\"path\":\"src\",\"glob\":\"*.rs\"}.",
    "parameters": {
      "type": "object",
      "properties": {
        "pattern": {"type": "string", "description": "Ripgrep regular expression."},
        "path": {"type": "string", "default": ".", "description": "File or directory to search, relative to the workspace or absolute."},
        "glob": {"type": "string", "description": "Optional ripgrep filename glob, e.g. *.rs."}
      },
      "required": ["pattern"],
      "additionalProperties": false
    }
  }
}`)

// noticeLimit leaves room in the output budget for the truncation and
// diagnostics notices.
const noticeLimit = 1024

const matchLimit = bodyLimit - noticeLimit

const diagnosticsLimit = 512

// search delegates traversal, ignore rules, globs, and content matching to ripgrep.
func search(ctx context.Context, root, name, arguments string) (string, []transcript.ToolContent, error) {
	args := struct {
		Pattern *string `json:"pattern"`
		Path    string  `json:"path"`
		Glob    *string `json:"glob"`
	}{Path: "."}
	var err error
	if name == Glob {
		var globArgs struct {
			Pattern *string `json:"pattern"`
			Path    *string `json:"path"`
		}
		err = decodeArguments(arguments, &globArgs)
		args.Pattern = globArgs.Pattern
		if globArgs.Path != nil {
			args.Path = *globArgs.Path
		}
	} else {
		err = decodeArguments(arguments, &args)
	}
	if err != nil {
		return "", nil, err
	}
	if args.Pattern == nil {
		return "", nil, errors.New("arguments: missing field `pattern`")
	}
	if args.Path == "" {
		return "", nil, errors.New("search path is empty")
	}
	scope := args.Path
	if !filepath.IsAbs(scope) {
		scope = filepath.Clean(scope)
		if scope != "." {
			scope = "./" + scope
		}
	}
	command := []string{"--no-config"}
	if name == Glob {
		command = append(command, "--files", "--null", "--glob", *args.Pattern)
	} else {
		command = append(command, "--json", "-e", *args.Pattern)
		if args.Glob != nil {
			command = append(command, "--glob", *args.Glob)
		}
	}
	command = append(command, "--", scope)
	cmd := exec.CommandContext(ctx, "rg", command...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, fmt.Errorf("could not run rg; install ripgrep and ensure rg is on PATH: %w", err)
	}
	out := searchOutput{grep: name == Grep}
	if name == Glob {
		reader := bufio.NewReader(stdout)
		for !out.truncated {
			candidate, readErr := reader.ReadString(0)
			if candidate != "" {
				out.files++
				out.truncated = out.append(strings.TrimSuffix(candidate, "\x00") + "\n")
			}
			if readErr != nil {
				err = readErr
				break
			}
		}
	} else {
		decoder := json.NewDecoder(stdout)
		for !out.truncated {
			var event struct {
				Type string `json:"type"`
				Data struct {
					Path       rgText `json:"path"`
					Lines      rgText `json:"lines"`
					LineNumber int    `json:"line_number"`
				} `json:"data"`
			}
			if err = decoder.Decode(&event); err != nil {
				break
			}
			switch event.Type {
			case "begin":
				out.fileMatched = false
			case "match":
				if !out.fileMatched {
					out.files++
					out.fileMatched = true
				}
				out.matches++
				text := fmt.Sprintf("%s:%d:%s", event.Data.Path.value(), event.Data.LineNumber, event.Data.Lines.value())
				if !strings.HasSuffix(text, "\n") {
					text += "\n"
				}
				out.truncated = out.append(strings.ToValidUTF8(text, "�"))
			}
		}
	}
	if out.truncated || err != nil && !errors.Is(err, io.EOF) {
		cmd.Process.Kill()
	}
	io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", nil, fmt.Errorf("reading rg output: %w", err)
	}
	if !out.truncated && waitErr != nil && cmd.ProcessState.ExitCode() != 1 && out.output.Len() == 0 {
		return "", nil, fmt.Errorf("rg: %s", strings.TrimSpace(stderr.String()))
	}
	return out.finish(strings.TrimSpace(stderr.String()))
}

type searchOutput struct {
	grep, fileMatched bool
	output            strings.Builder
	truncated         bool
	files, matches    int
}

type rgText struct {
	Text  string `json:"text"`
	Bytes []byte `json:"bytes"`
}

func (t rgText) value() string {
	if t.Bytes != nil {
		return string(t.Bytes)
	}
	return t.Text
}

// append adds text and reports whether the output reached its limit.
func (s *searchOutput) append(text string) bool {
	s.output.WriteString(text)
	if s.output.Len() <= matchLimit {
		return false
	}
	kept := truncate(s.output.String(), matchLimit)
	s.output.Reset()
	s.output.WriteString(kept)
	return true
}

// finish returns the model's text and, when anything was found, a summary of
// the counts for the client.
func (s *searchOutput) finish(diagnostics string) (string, []transcript.ToolContent, error) {
	text := s.output.String()
	if s.truncated {
		if !strings.HasSuffix(text, "\n") {
			text += " [partial line]\n"
		}
		text += "Output truncated. Narrow the search path or pattern."
	} else if text == "" {
		text = "No matches found."
	}
	var content []transcript.ToolContent
	if s.files > 0 {
		summary := fmt.Sprintf("%d files", s.files)
		if s.grep {
			summary = fmt.Sprintf("%d matches in %d files", s.matches, s.files)
		}
		if s.truncated {
			summary += ", truncated"
		}
		content = append(content, transcript.ToolContent{Text: summary})
	}
	if diagnostics != "" {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "Some paths could not be searched:\n" + truncate(diagnostics, diagnosticsLimit) + "\n"
	}

	return text, content, nil
}
