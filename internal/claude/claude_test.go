package claude

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// Claude reports a startup failure as an error result or plain text before
// it exits, sometimes before reading initialize.
func TestStartupFailureKeepsClaudesReason(t *testing.T) {
	for name, output := range map[string]string{
		"error result": `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["No conversation found with session ID: 1111"]}` + "\n",
		"plain text":   "No conversation found with session ID: 1111\n",
	} {
		t.Run(name, func(t *testing.T) {
			for _, input := range []io.Writer{io.Discard, closedPipe(t)} {
				err := initialize(input, bufio.NewReader(strings.NewReader(output)))
				if err == nil || !strings.Contains(err.Error(), "No conversation found with session ID: 1111") {
					t.Fatalf("startup failure lost Claude's reason: %v", err)
				}
			}
		})
	}
}

func closedPipe(t *testing.T) io.Writer {
	r, w := io.Pipe()
	r.Close()
	return w
}
