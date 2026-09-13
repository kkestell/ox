//go:build windows

package agent

import "testing"

func makeInstructionFIFO(t *testing.T, _ string) {
	t.Helper()
	t.Skip("Windows does not provide Unix FIFOs")
}
