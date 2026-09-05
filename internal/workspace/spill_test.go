package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRenderLinesHonorsInlineBoundaries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill")
	exactBytes := []string{strings.Repeat("x", InlineMaxBytes)}
	result, err := RenderLines(dir, "grep", "exact-bytes", exactBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spilled != "" {
		t.Fatal("output exactly at the byte limit spilled")
	}
	overBytes := []string{strings.Repeat("x", InlineMaxBytes+1)}
	result, err = RenderLines(dir, "grep", "over-bytes", overBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spilled == "" || result.TotalBytes != InlineMaxBytes+1 {
		t.Fatalf("byte overflow result = %+v", result)
	}

	exactLines := make([]string, InlineMaxLines)
	for index := range exactLines {
		exactLines[index] = "x"
	}
	result, err = RenderLines(dir, "glob", "exact-lines", exactLines, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spilled != "" {
		t.Fatal("output exactly at the line limit spilled")
	}
	result, err = RenderLines(
		dir,
		"glob",
		"over-lines",
		append(exactLines, "x"),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spilled == "" || result.TotalLines != InlineMaxLines+1 {
		t.Fatalf("line overflow result = %+v", result)
	}
}

func TestSpillIsLazySecureCompleteAndSanitizesCallID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill")
	result, err := RenderLines(dir, "grep", "small", []string{"one"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Spilled != "" {
		t.Fatal("small result spilled")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("spill directory created eagerly: %v", err)
	}

	lines := make([]string, InlineMaxLines+1)
	for index := range lines {
		lines[index] = "line"
	}
	result, err = RenderLines(dir, "grep", "../bad/\x00"+strings.Repeat("x", 300), lines, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(result.Spilled) != dir {
		t.Fatalf("spill escaped directory: %q", result.Spilled)
	}
	if !regexp.MustCompile(`^grep-[A-Za-z0-9_-]+\.out$`).MatchString(filepath.Base(result.Spilled)) {
		t.Fatalf("unsafe spill filename: %q", filepath.Base(result.Spilled))
	}
	content, err := os.ReadFile(result.Spilled)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != strings.Join(lines, "\n") {
		t.Fatal("spill file did not preserve the complete output")
	}
	info, err := os.Stat(result.Spilled)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("spill mode = %o", info.Mode().Perm())
	}
	if result.TotalBytes != len(content) {
		t.Fatalf("reported bytes = %d, file bytes = %d", result.TotalBytes, len(content))
	}
}

func TestSpillNeverOverwritesAnExistingCallResult(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill")
	lines := make([]string, InlineMaxLines+1)
	for index := range lines {
		lines[index] = "first"
	}
	first, err := RenderLines(dir, "grep", "same-call", lines, false)
	if err != nil {
		t.Fatal(err)
	}
	for index := range lines {
		lines[index] = "second"
	}
	if _, err := RenderLines(dir, "grep", "same-call", lines, false); err == nil {
		t.Fatal("duplicate spill filename overwrote an existing result")
	}
	content, err := os.ReadFile(first.Spilled)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "second") {
		t.Fatal("existing spill content was changed")
	}
}

func TestSpilledPreviewStaysWithinTheByteBudget(t *testing.T) {
	line := strings.Repeat("é", InlineMaxBytes)
	result, err := RenderLines(
		filepath.Join(t.TempDir(), "spill"),
		"grep",
		"huge-line",
		[]string{line},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) > InlineMaxBytes {
		t.Fatalf("preview = %d bytes, limit = %d", len(result.Content), InlineMaxBytes)
	}
}

func TestCappedUsesTheTrueJoinedSizeAndLatchesTruncation(t *testing.T) {
	collector := Capped{Bytes: CollectionLimitBytes - 3, Lines: []string{"existing"}}
	if !collector.Push("xx") {
		t.Fatal("line exactly reaching the cap was refused")
	}
	if collector.Bytes != CollectionLimitBytes {
		t.Fatalf("bytes = %d", collector.Bytes)
	}
	if collector.Push("") || !collector.Truncated {
		t.Fatal("overflow did not latch truncation")
	}
}

func TestRenderTextUsesExactInlineBoundaryAndFaithfulSpill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spills")
	inline, err := RenderText(dir, "mcp", "inline", "hello", 5)
	if err != nil || inline.Content != "hello" || inline.Spilled != "" {
		t.Fatalf("inline = %#v, %v", inline, err)
	}
	content := "α\n" + strings.Repeat("x", 256)
	spilled, err := RenderText(dir, "mcp", "spill", content, 256)
	if err != nil {
		t.Fatal(err)
	}
	if spilled.Spilled == "" || len(spilled.Content) > 256 || !strings.Contains(spilled.Content, spilled.Spilled) {
		t.Fatalf("spilled = %#v", spilled)
	}
	raw, err := os.ReadFile(spilled.Spilled)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Fatalf("spill = %q", raw)
	}
}
