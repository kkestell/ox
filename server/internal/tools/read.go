package tools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"ox/internal/transcript"
)

// maxReadLimit is the most lines one read returns. The byte limit, not the
// line limit, usually ends a page.
const maxReadLimit = 1000

var readSchema = compact(`{
  "type": "function",
  "function": {
    "name": "read_file",
    "description": "Read a UTF-8 text file. Returns numbered lines, at most 16 KiB, with the next offset when more remains. An oversized line returns a marked prefix; its omitted portion cannot be retrieved through line pagination. Example: {\"path\":\"src/main.rs\",\"offset\":1,\"limit\":100}.",
    "parameters": {
      "type": "object",
      "properties": {
        "path": {"type": "string", "description": "File path relative to the workspace or absolute."},
        "offset": {"type": "integer", "minimum": 1, "default": 1, "description": "1-based starting line."},
        "limit": {"type": "integer", "minimum": 1, "maximum": 1000, "default": 1000, "description": "Maximum number of lines to return."}
      },
      "required": ["path"],
      "additionalProperties": false
    }
  }
}`)

// read returns up to limit numbered lines from offset, using at most bodyLimit
// bytes for the lines. A line cut short to fit is followed by a notice that its
// rest cannot be read by line. When the file continues, the result ends with
// the offset to continue from. The content summarizes the lines read.
func read(root, arguments string) (string, []transcript.ToolContent, error) {
	args := struct {
		Path   *string `json:"path"`
		Offset uint64  `json:"offset"`
		Limit  int     `json:"limit"`
	}{Offset: 1, Limit: maxReadLimit}
	if err := decodeArguments(arguments, &args); err != nil {
		return "", nil, err
	}
	if args.Path == nil {
		return "", nil, errors.New("arguments: missing field `path`")
	}
	if args.Offset == 0 || args.Limit < 1 || args.Limit > maxReadLimit {
		return "", nil, fmt.Errorf("offset must be at least 1 and limit must be between 1 and %d", maxReadLimit)
	}
	if *args.Path == "" {
		return "", nil, errors.New("file path is empty")
	}
	file, err := openRegular(filePath(root, *args.Path))
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for range args.Offset - 1 {
		if _, _, ok, err := readLine(reader, 0); err != nil {
			return "", nil, err
		} else if !ok {
			return "Offset is past end of file.", nil, nil
		}
	}
	text, next, end, err := page(reader, args.Offset, args.Limit)
	if err != nil {
		return "", nil, err
	}
	if text == "" {
		if args.Offset == 1 {
			return "File is empty.", nil, nil
		}
		return "Offset is past end of file.", nil, nil
	}
	if end == lineTruncated {
		text += "[Line truncated. The omitted portion cannot be retrieved through line pagination.]\n"
	}
	more := end == nextLineDeferred
	if !more {
		_, err := reader.Peek(1)
		more = err == nil
	}
	last := next - 1
	summary := fmt.Sprintf("Lines %d–%d of %d", args.Offset, last, last)
	if more {
		text += fmt.Sprintf("More content remains. Continue with offset=%d.\n", next)
		summary = fmt.Sprintf("Lines %d–%d", args.Offset, last)
	}
	return text, []transcript.ToolContent{{Text: summary}}, nil
}

// pageEnd is why a page ended.
type pageEnd int

const (
	lineLimitOrEndOfFile pageEnd = iota
	// nextLineDeferred: the next line did not fit in the page's remaining
	// space, so it starts the next page.
	nextLineDeferred
	// lineTruncated: the page's only line was longer than bodyLimit and was
	// cut short.
	lineTruncated
)

// page returns the numbered lines of one read, the number of the first line
// it does not include, and why it ended.
func page(reader *bufio.Reader, first uint64, limit int) (string, uint64, pageEnd, error) {
	var text string
	next := first
	for range limit {
		content, omitted, ok, err := readLine(reader, bodyLimit+2)
		if err != nil {
			return "", 0, 0, err
		}
		if !ok {
			break
		}
		numbered := fmt.Sprintf("%d: %s", next, content)
		if text != "" && (omitted || len(text)+len(numbered)+1 > bodyLimit) {
			return text, next, nextLineDeferred, nil
		}
		truncated := omitted || len(numbered)+1 > bodyLimit
		if truncated {
			numbered = truncate(numbered, bodyLimit-1)
		}
		text += numbered + "\n"
		next++
		if truncated {
			return text, next, lineTruncated, nil
		}
	}
	return text, next, lineLimitOrEndOfFile, nil
}

// readLine reads one line, validating all of it as UTF-8 without NUL bytes but
// keeping at most keep bytes, even for enormous lines. ok is false at the end
// of the file. omitted reports that bytes beyond keep were dropped.
func readLine(reader *bufio.Reader, keep int) (content string, omitted, ok bool, err error) {
	var prefix, pending []byte
	length := 0
	newline := false
	for !newline {
		chunk, readErr := reader.ReadSlice('\n')
		if bytes.IndexByte(chunk, 0) >= 0 {
			return "", false, false, errors.New("unsupported text file: contains NUL bytes")
		}
		pending = append(pending, chunk...)
		valid, invalid := validUTF8Prefix(pending)
		if invalid {
			return "", false, false, errors.New("unsupported text file: invalid UTF-8")
		}
		pending = append([]byte{}, pending[valid:]...)
		prefix = append(prefix, chunk[:min(len(chunk), keep-len(prefix))]...)
		length += len(chunk)
		newline = len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			return "", false, false, readErr
		}
	}
	if len(pending) > 0 {
		return "", false, false, errors.New("unsupported text file: incomplete UTF-8 character")
	}
	if length == 0 {
		return "", false, false, nil
	}
	omitted = length > len(prefix)
	if !omitted && newline {
		prefix = bytes.TrimSuffix(bytes.TrimSuffix(prefix, []byte("\n")), []byte("\r"))
	}
	for !utf8.Valid(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return string(prefix), omitted, true, nil
}

// validUTF8Prefix returns the length of b's valid UTF-8 prefix, leaving an
// incomplete character at its end unread, and whether b holds an invalid
// sequence.
func validUTF8Prefix(b []byte) (int, bool) {
	valid := 0
	for valid < len(b) {
		r, size := utf8.DecodeRune(b[valid:])
		if r == utf8.RuneError && size <= 1 {
			if !utf8.FullRune(b[valid:]) {
				return valid, false
			}
			return valid, true
		}
		valid += size
	}
	return valid, false
}
