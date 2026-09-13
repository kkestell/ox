package openrouter

import (
	"errors"
	"strings"
	"testing"
)

func TestSSESkipsCommentsJoinsDataAndStopsAtDone(t *testing.T) {
	input := ": OPENROUTER PROCESSING\n\n" +
		"event: ignored\n" +
		"data: {\"value\":\n" +
		"data: 1}\n\n" +
		"data: [DONE]\n\n" +
		"data: never\n\n"
	var events []string
	err := readSSE(strings.NewReader(input), func(data []byte) error {
		events = append(events, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "{\"value\":\n1}" {
		t.Fatalf("events = %#v", events)
	}
}

func TestSSEReportsBodyEndingWithoutDone(t *testing.T) {
	var events int
	err := readSSE(strings.NewReader("data: {}\n\n"), func([]byte) error {
		events++
		return nil
	})
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("error = %v", err)
	}
	if events != 1 {
		t.Fatalf("events = %d", events)
	}
}

func TestSSEAcceptsAnEventLargerThanScannerDefault(t *testing.T) {
	large := strings.Repeat("x", 128*1024)
	input := "data: " + large + "\n\ndata: [DONE]\n\n"
	var got string
	err := readSSE(strings.NewReader(input), func(data []byte) error {
		got = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != large {
		t.Fatalf("event length = %d", len(got))
	}
}

// TestSSEReportsAReadFailureAsAValue covers the condition the retry
// classification branches on, so rewording the wrap cannot change it.
func TestSSEReportsAReadFailureAsAValue(t *testing.T) {
	broken := errors.New("connection reset")
	err := readSSE(failingReader{err: broken}, func([]byte) error { return nil })
	if !errors.Is(err, errStreamRead) {
		t.Fatalf("error = %v, want it to wrap errStreamRead", err)
	}
	if !errors.Is(err, broken) {
		t.Fatalf("error = %v, want it to wrap the transport failure", err)
	}
}

type failingReader struct {
	err error
}

func (r failingReader) Read([]byte) (int, error) {
	return 0, r.err
}
