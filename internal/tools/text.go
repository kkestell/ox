package tools

import (
	"bytes"
	"strings"
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
