package agent

import (
	"strings"
	"unicode"
)

// AutoRuns reports whether Ox runs a shell command in Auto without asking the
// user: every command except one that starts with rm.
func AutoRuns(command string) bool {
	return !strings.HasPrefix(strings.TrimLeftFunc(command, unicode.IsSpace), "rm")
}
