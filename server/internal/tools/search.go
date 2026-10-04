package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"ox/internal/transcript"
)

var globSchema = compact(`{
  "type": "function",
  "function": {
    "name": "glob",
    "description": "Find files inside the workspace using a ripgrep glob. Returns ` + "`./`" + `-prefixed workspace-relative paths, at most 16 KiB. Narrow the pattern or path if truncated. Skips hidden and ignored files, whatever the pattern; does not follow symlinks during traversal. Example: {\"pattern\":\"*.rs\",\"path\":\"src\"}.",
    "parameters": {
      "type": "object",
      "properties": {
        "pattern": {"type": "string", "description": "Ripgrep glob, e.g. *.rs or src/**/*.rs."},
        "path": {"type": "string", "default": ".", "description": "Directory to search, relative to the workspace or absolute inside it. Globs are relative to the workspace."}
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
    "description": "Search workspace text files with a case-sensitive RE2 regular expression in Go syntax; use (?i) for case-insensitivity. Returns path:line:content, at most 16 KiB. Narrow the pattern or path if truncated. Skips hidden and ignored files unless the path names one; does not follow symlinks during traversal. Example: {\"pattern\":\"fn main\",\"path\":\"src\",\"glob\":\"*.rs\"}.",
    "parameters": {
      "type": "object",
      "properties": {
        "pattern": {"type": "string", "description": "RE2 regular expression."},
        "path": {"type": "string", "default": ".", "description": "File or directory to search, relative to the workspace or absolute inside it."},
        "glob": {"type": "string", "description": "Optional filename glob, relative to the workspace, e.g. *.rs."}
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

// rgProgram is the ripgrep executable; tests replace it.
var rgProgram = "rg"

// search runs glob or grep. Ripgrep supplies candidate names and ignore
// filtering; Ox opens each candidate through the workspace root before
// reading or returning it.
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
	var filter *globMatcher
	var matcher *regexp.Regexp
	if name == Glob {
		if filter, err = compileGlob(*args.Pattern); err != nil {
			return "", nil, fmt.Errorf("pattern: %w", err)
		}
	} else {
		if matcher, err = regexp.Compile(*args.Pattern); err != nil {
			return "", nil, fmt.Errorf("pattern: %w", err)
		}
		if args.Glob != nil {
			if filter, err = compileGlob(*args.Glob); err != nil {
				return "", nil, fmt.Errorf("glob: %w", err)
			}
		}
	}
	w, err := openWorkspace(root)
	if err != nil {
		return "", nil, err
	}
	defer w.Close()
	relative := w.relativeName(args.Path)
	path, err := w.resolve(relative)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", args.Path, err)
	}
	directory := w.isDirectory(path)
	if name == Glob && !directory {
		return "", nil, errors.New("glob path must name a directory")
	}
	if !directory && !w.isRegular(path) {
		return "", nil, errors.New("search path must name a regular file or directory")
	}
	scope := "."
	if relative = filepath.Clean(relative); relative != "." {
		scope = "./" + relative
	}
	cmd := exec.CommandContext(ctx, rgProgram, "--no-config", "--files", "--null", "--", scope)
	cmd.Dir = root
	return run(cmd, w, filter, matcher)
}

func run(cmd *exec.Cmd, w *workspace, filter *globMatcher, matcher *regexp.Regexp) (string, []transcript.ToolContent, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("could not run rg; install ripgrep and ensure rg is on PATH: %w", err)
	}
	reader := bufio.NewReader(stdout)
	out := searchOutput{workspace: w, filter: filter, matcher: matcher}
	for !out.truncated {
		candidate, err := reader.ReadString(0)
		if candidate = strings.TrimSuffix(candidate, "\x00"); candidate != "" {
			out.add(candidate)
		}
		if err != nil {
			break
		}
	}
	if out.truncated {
		cmd.Process.Kill()
	}
	io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	exitCode := cmd.ProcessState.ExitCode()
	if !out.truncated && exitCode != 0 && exitCode != 1 && out.output.Len() == 0 {
		if waitErr == nil {
			waitErr = fmt.Errorf("exit status %d", exitCode)
		}
		return "", nil, fmt.Errorf("rg failed (%v); check the glob or search path", waitErr)
	}
	return out.finish(stderr.Len() > 0)
}

type searchOutput struct {
	workspace *workspace
	filter    *globMatcher
	matcher   *regexp.Regexp
	output    strings.Builder
	truncated bool
	errors    string
	// files counts the files listed, or the files with a match.
	files   int
	matches int
}

func (s *searchOutput) add(candidate string) {
	if s.filter != nil && !s.filter.match(candidate) {
		return
	}
	relative, err := s.workspace.resolve(candidate)
	if err != nil {
		return
	}
	if s.matcher == nil {
		if s.workspace.isRegular(relative) {
			s.files++
			s.truncated = s.append(candidate + "\n")
		}
		return
	}
	file, err := s.workspace.openRegular(relative)
	if err != nil {
		s.recordError(candidate, err)
		return
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	matches := 0
	for number := 1; ; number++ {
		line, err := reader.ReadBytes('\n')
		searchable := bytes.TrimSuffix(line, []byte("\n"))
		if len(line) > 0 && bytes.IndexByte(searchable, 0) < 0 && s.matcher.Match(searchable) {
			matches++
			text := fmt.Sprintf("%s:%d:%s", candidate, number, strings.ToValidUTF8(string(line), "�"))
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			if s.append(text) {
				s.truncated = true
				break
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.recordError(candidate, err)
			}
			break
		}
	}
	s.matches += matches
	if matches > 0 {
		s.files++
	}
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

func (s *searchOutput) recordError(path string, err error) {
	if len(s.errors) < diagnosticsLimit {
		s.errors += fmt.Sprintf("%s: %v\n", path, err)
	}
}

// finish returns the model's text and, when anything was found, a summary of
// the counts for the client.
func (s *searchOutput) finish(enumerationFailed bool) (string, []transcript.ToolContent, error) {
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
		if s.matcher != nil {
			summary = fmt.Sprintf("%d matches in %d files", s.matches, s.files)
		}
		if s.truncated {
			summary += ", truncated"
		}
		content = append(content, transcript.ToolContent{Text: summary})
	}
	if enumerationFailed || s.errors != "" {
		// Ripgrep may enumerate a path that changed during traversal. Its raw
		// diagnostics can name files outside the workspace after such a swap,
		// so only their presence is reported.
		diagnostics := ""
		if enumerationFailed {
			diagnostics = "Ripgrep could not enumerate some paths.\n"
		}
		diagnostics = truncate(diagnostics+s.errors, diagnosticsLimit)
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "Some paths could not be searched:\n" + strings.TrimRight(diagnostics, " \t\r\n") + "\n"
	}
	return text, content, nil
}
