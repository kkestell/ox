// Package control writes control characters as escape sequences, so text
// printed to a terminal cannot move the cursor or change modes.
package control

import (
	"fmt"
	"strings"
	"unicode"
)

// Escape returns text with each control character except newline and tab
// written as an escape sequence.
func Escape(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			out.WriteString(Rune(r))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Rune returns the escape sequence of a control character, or the character.
func Rune(r rune) string {
	switch {
	case r == '\t':
		return `\t`
	case r == '\r':
		return `\r`
	case r == '\n':
		return `\n`
	case unicode.IsControl(r):
		return fmt.Sprintf(`\u{%x}`, r)
	}
	return string(r)
}
