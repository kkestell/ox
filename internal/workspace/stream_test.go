package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStreamRecorderKeepsExactBudgetInline(t *testing.T) {
	recorder := NewStreamRecorder(
		filepath.Join(t.TempDir(), "spill"),
		"shell",
		"call-1",
		nil,
	)
	body := strings.Repeat("x", InlineMaxBytes)
	if _, err := recorder.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != body || result.Spilled != "" ||
		result.TotalBytes != InlineMaxBytes || result.TotalLines != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestStreamRecorderSpillsFaithfullyWithHeadAndTail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill")
	recorder := NewStreamRecorder(dir, "shell", "../unsafe", nil)
	var body strings.Builder
	for line := 0; line < 10_000; line++ {
		fmt.Fprintf(&body, "line %05d %s\n", line, strings.Repeat("x", 20))
	}
	text := body.String()
	for start := 0; start < len(text); start += 7919 {
		end := min(start+7919, len(text))
		if _, err := recorder.Write([]byte(text[start:end])); err != nil {
			t.Fatal(err)
		}
	}
	result, err := recorder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalLines != 10_000 || result.TotalBytes != len(text) ||
		result.Spilled == "" {
		t.Fatalf("result = %+v", result)
	}
	spilled, err := os.ReadFile(result.Spilled)
	if err != nil {
		t.Fatal(err)
	}
	if string(spilled) != text {
		t.Fatal("spill file does not contain the complete stream")
	}
	if !strings.HasPrefix(result.Content, "line 00000") ||
		!strings.Contains(result.Content, "line 09999") ||
		!strings.Contains(result.Content, "full output at "+result.Spilled) ||
		!strings.Contains(result.Content, "10000 lines") {
		t.Fatalf("preview = %q", result.Content)
	}
	if len(result.Content) > InlineMaxBytes {
		t.Fatalf("preview uses %d bytes, limit is %d", len(result.Content), InlineMaxBytes)
	}
}

func TestStreamRecorderSpillsAtTheLineLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill")
	recorder := NewStreamRecorder(dir, "shell", "lines", nil)
	body := strings.Repeat("x\n", InlineMaxLines+1)
	if _, err := recorder.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if result.Spilled == "" || result.TotalLines != InlineMaxLines+1 {
		t.Fatalf("result = %+v", result)
	}
	if got, err := os.ReadFile(result.Spilled); err != nil || string(got) != body {
		t.Fatalf("spill = %q, %v", got, err)
	}
}

func TestStreamRecorderSanitizesAcrossWrites(t *testing.T) {
	recorder := NewStreamRecorder(
		filepath.Join(t.TempDir(), "spill"),
		"shell",
		"unicode",
		nil,
	)
	for _, chunk := range [][]byte{
		[]byte("h\xc3"),
		[]byte("\xa9llo "),
		{0xff},
		{0xe2, 0x82},
	} {
		if _, err := recorder.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	result, err := recorder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "héllo ��" || !utf8.ValidString(result.Content) {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestStreamRecorderPreviewDoesNotSplitUTF8(t *testing.T) {
	recorder := NewStreamRecorder(
		filepath.Join(t.TempDir(), "spill"),
		"shell",
		"unicode-preview",
		nil,
	)
	body := strings.Repeat("x", streamHeadBytes-1) +
		"é" +
		strings.Repeat("y", InlineMaxBytes)
	if _, err := recorder.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(result.Content) {
		t.Fatal("model-facing preview is not valid UTF-8")
	}
	spilled, err := os.ReadFile(result.Spilled)
	if err != nil {
		t.Fatal(err)
	}
	if string(spilled) != body {
		t.Fatal("UTF-8 boundary handling changed the spill file")
	}
}

func TestStreamRecorderEmitsCompleteLinesAndThrottles(t *testing.T) {
	var batches []string
	recorder := NewStreamRecorder(
		filepath.Join(t.TempDir(), "spill"),
		"shell",
		"emit",
		func(text string) { batches = append(batches, text) },
	)
	for _, chunk := range []string{"one\ntw", "o\r", "\nthree\rfour\n"} {
		recorder.lastEmit = time.Time{}
		if _, err := recorder.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := recorder.Finish(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(batches, ""); got != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("emitted = %q", got)
	}

	batches = nil
	recorder = NewStreamRecorder(
		filepath.Join(t.TempDir(), "spill"),
		"shell",
		"throttle",
		func(text string) { batches = append(batches, text) },
	)
	if _, err := recorder.Write([]byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Write([]byte("b\n")); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0] != "a\n" {
		t.Fatalf("batches = %#v", batches)
	}
	if _, err := recorder.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || batches[1] != "b\n" {
		t.Fatalf("finished batches = %#v", batches)
	}
}

func TestStreamRecorderDefersSpillFailure(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := NewStreamRecorder(blocked, "shell", "failure", nil)
	if written, err := recorder.Write([]byte(strings.Repeat("x", InlineMaxBytes+1))); err != nil ||
		written != InlineMaxBytes+1 {
		t.Fatalf("write = %d, %v", written, err)
	}
	if _, err := recorder.Finish(); err == nil {
		t.Fatal("Finish succeeded after spill creation failed")
	}
}
