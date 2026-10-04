package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ox/internal/transcript"
)

func wrapped(body string) string {
	return "*** Begin Patch\n" + body + "*** End Patch\n"
}

func edit(t *testing.T, source, body string) (string, error) {
	t.Helper()
	operations, err := parsePatch(wrapped("*** Update File: file\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	return update(source, operations[0].chunks)
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func entries(t *testing.T, dir string) int {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestPatchCreatesUpdatesMovesAndDeletesInOrder(t *testing.T) {
	root := t.TempDir()
	greeting := "fn greeting() -> &'static str {\n    \"Hello\"\n}\n"
	for path, text := range map[string]string{
		"src/greeting.rs": greeting, "old-name.txt": "Old text\n", "obsolete.txt": "obsolete", "same.txt": "Same\n", "kept.txt": "Kept\n",
	} {
		writeFile(t, filepath.Join(root, path), text)
	}
	patch := `*** Begin Patch
*** Add File: notes.txt
+New notes.
*** Update File: src/greeting.rs
@@ fn greeting() -> &'static str {
-    "Hello"
+    "Hello, world"
 }
*** Update File: old-name.txt
*** Move to: new-name.txt
@@
-Old text
+New text
*** Delete File: obsolete.txt
*** Update File: same.txt
*** Move to: moved.txt
*** Update File: kept.txt
@@
 Kept
*** End Patch
`
	text, content, err := applyPatch(root, patch)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Applied patch.\nAdded notes.txt\nModified src/greeting.rs\nMoved old-name.txt -> new-name.txt\nDeleted obsolete.txt\nMoved same.txt -> moved.txt\nUnchanged kept.txt" {
		t.Errorf("text = %q", text)
	}
	diff := func(path string, old *string, new string) transcript.ToolContent {
		return transcript.ToolContent{Diff: &transcript.Diff{Path: filepath.Join(root, path), OldText: old, NewText: new}}
	}
	ptr := func(s string) *string { return &s }
	want := []transcript.ToolContent{
		{Text: "Added notes.txt"}, diff("notes.txt", nil, "New notes.\n"),
		{Text: "Modified src/greeting.rs"}, diff("src/greeting.rs", ptr(greeting), strings.Replace(greeting, `"Hello"`, `"Hello, world"`, 1)),
		{Text: "Moved old-name.txt -> new-name.txt"}, diff("new-name.txt", ptr("Old text\n"), "New text\n"),
		{Text: "Deleted obsolete.txt"}, diff("obsolete.txt", ptr("obsolete"), ""),
		{Text: "Moved same.txt -> moved.txt"},
		{Text: "Unchanged kept.txt"},
	}
	if !reflect.DeepEqual(content, want) {
		t.Errorf("content = %+v", content)
	}
	for path, text := range map[string]string{"notes.txt": "New notes.\n", "new-name.txt": "New text\n", "moved.txt": "Same\n"} {
		if got := readText(t, filepath.Join(root, path)); got != text {
			t.Errorf("%s = %q", path, got)
		}
	}
	for _, path := range []string{"old-name.txt", "obsolete.txt", "same.txt"} {
		if _, err := os.Lstat(filepath.Join(root, path)); err == nil {
			t.Errorf("%s remains", path)
		}
	}
}

func TestPatchHandlesEmptyFilesMarkerLikeLinesAndBinaryMoves(t *testing.T) {
	root := t.TempDir()
	if text, content, err := applyPatch(root, wrapped("")); text != "Applied patch." || content != nil || err != nil {
		t.Errorf("empty patch: %q %v %v", text, content, err)
	}
	if _, _, err := applyPatch(root, wrapped("*** Add File: empty\n*** Add File: nested/blank\n+\n+*** End Patch\n")); err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(root, "empty")) != "" || readText(t, filepath.Join(root, "nested/blank")) != "\n*** End Patch\n" {
		t.Error("added files differ")
	}
	if text, _, _ := applyPatch(root, wrapped("*** Update File: nested/blank\n@@\n \n *** End Patch\n")); text != "Applied patch.\nUnchanged nested/blank" {
		t.Errorf("unchanged: %q", text)
	}
	writeFile(t, filepath.Join(root, "bytes"), "\xff\r\n\x00")
	if text, _, err := applyPatch(root, wrapped("*** Update File: bytes\n*** Move to: moved/bytes\n")); err != nil || text != "Applied patch.\nMoved bytes -> moved/bytes" {
		t.Errorf("binary move: %q %v", text, err)
	}
	if readText(t, filepath.Join(root, "moved/bytes")) != "\xff\r\n\x00" {
		t.Error("a move-only update changed the bytes")
	}
}

func TestLargePatchSummaryIsBoundedAndKeepsEveryDiff(t *testing.T) {
	tools := toolbox(t)
	var body strings.Builder
	const count = 180
	for i := range count {
		fmt.Fprintf(&body, "*** Add File: %03d-%s\n+content\n", i, strings.Repeat("雪", 32))
	}
	outcome := tools.call(ApplyPatch, object{"patch": wrapped(body.String())})
	if outcome.Status != transcript.ToolCompleted || len(outcome.Text) > outputLimit || !strings.HasSuffix(outcome.Text, "Summary truncated.") {
		t.Fatalf("summary: %s, %d bytes", outcome.Status, len(outcome.Text))
	}
	if len(outcome.Content) != count*2 || entries(t, tools.Workspace) != count {
		t.Fatalf("content blocks = %d, files = %d", len(outcome.Content), entries(t, tools.Workspace))
	}
}

func TestPatchMatchingIsExactLiteralAndForward(t *testing.T) {
	for _, test := range []struct{ source, body, want string }{
		{"x\nx\nx\n", "@@\n-x\n+y\n@@\n-x\n+z\n", "y\nz\nx\n"},
		{"x\n.*\nx\nx\n", "@@ .*\n-x\n+y\n", "x\n.*\ny\nx\n"},
		{"a\nb\n", "@@\n+end\n", "a\nb\nend\n"},
		{"a\nb\n", "@@ a\n+middle\n@@\n+next\n", "a\nmiddle\nnext\nb\n"},
		{"a\nb\n", "@@\n-a\n+A\n@@\n+middle\n", "A\nmiddle\nb\n"},
		{"a\nb", "@@\n-a\n+A\n", "A\nb"},
		{"a\r\nb\r\n", "@@\n-a\n+A\n", "A\r\nb\r\n"},
		{"a\nb\r\nc\n", "@@\n-a\n+A\n", "A\nb\nc\n"},
		{"a\r\nb\nc\r\n", "@@\n-a\n+A\n", "A\r\nb\r\nc\r\n"},
		{"a\r\n", "@@\n-a\n", ""},
		{"", "@@\n+new\n", "new"},
		{"a\n", "@@\n-a\n+\n", "\n"},
	} {
		if got, err := edit(t, test.source, test.body); err != nil || got != test.want {
			t.Errorf("%q with %q: %q, %v", test.source, test.body, got, err)
		}
	}
	for _, test := range [][2]string{
		{" x\n", "@@\n-x\n+y\n"}, {"X\n", "@@\n-x\n+y\n"}, {"a\nb\n", "@@\n-b\n+B\n@@\n-a\n+A\n"},
		{"a\nb\n", "@@ missing\n+b\n"}, {"a\nb\n", "@@ a\n-a\n+A\n"},
	} {
		if _, err := edit(t, test[0], test[1]); err == nil || !strings.Contains(err.Error(), "chunk") {
			t.Errorf("%q with %q: %v", test[0], test[1], err)
		}
	}
}

func TestMalformedPatchesReportLinesAndNeverWrite(t *testing.T) {
	root := t.TempDir()
	for _, test := range [][2]string{
		{"", "line 1: expected *** Begin Patch"},
		{" *** Begin Patch\n*** End Patch", "line 1: expected *** Begin Patch"},
		{"*** Begin Patch", "line 2: expected *** End Patch"},
		{"*** Begin Patch\n*** End Patch\nextra", "line 3: text after *** End Patch"},
		{"*** Begin Patch\n*** Add File: first\n+ok\ninvalid\n*** End Patch", "line 4: expected a file operation or *** End Patch; invalid header or body prefix"},
		{"*** Begin Patch\n*** Add File: first\n+ok\n*** Update File: x\n@@\n?bad\n*** End Patch", "line 6: chunk must contain context, removed, or added lines"},
		{"*** Begin Patch\n*** Add File: first\n+ok\n*** Delete File: x\n-body\n*** End Patch", "line 5: expected a file operation or *** End Patch; invalid header or body prefix"},
		{"*** Begin Patch\n*** Add File: \n*** End Patch", "line 2: file path is empty"},
		{"*** Begin Patch\n*** Update File: x\n*** End Patch", "line 3: Update File requires chunks or Move to"},
		{"*** Begin Patch\n*** Update File: x\n@@ -1 +1 @@\n*** End Patch", "line 4: chunk must contain context, removed, or added lines"},
		{"*** Begin Patch\n*** Update File: x\n*** Move to: \n*** End Patch", "line 3: move destination is empty"},
	} {
		if _, _, err := applyPatch(root, test[0]); err == nil || err.Error() != "parse: "+test[1] {
			t.Errorf("%q: %v", test[0], err)
		}
		if entries(t, root) != 0 {
			t.Fatalf("%q wrote files", test[0])
		}
	}
}

func TestPreparationRejectsInvalidOperationsWithoutChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "existing"), "original\n")
	writeFile(t, filepath.Join(root, "binary"), "\xff")
	os.Mkdir(filepath.Join(root, "directory"), 0o777)
	for _, body := range []string{
		"*** Add File: .\n+x\n",
		"*** Delete File: missing\n",
		"*** Update File: missing\n@@\n-x\n+y\n",
		"*** Update File: existing\n@@\n-wrong\n+y\n",
		"*** Update File: binary\n@@\n+x\n",
		"*** Add File: existing\n+x\n",
		"*** Delete File: directory\n",
		"*** Update File: directory\n*** Move to: new\n",
		"*** Update File: existing\n*** Move to: directory\n",
		"*** Update File: existing\n*** Move to: existing\n",
		"*** Add File: duplicate\n*** Add File: ./duplicate\n",
		"*** Update File: existing\n*** Move to: dest\n*** Add File: dest\n",
	} {
		_, _, err := applyPatch(root, wrapped("*** Add File: first\n+ok\n"+body))
		if err == nil || !strings.HasPrefix(err.Error(), "prepare:") {
			t.Errorf("%q: %v", body, err)
		}
		if entries(t, root) != 3 || readText(t, filepath.Join(root, "existing")) != "original\n" {
			t.Fatalf("%q changed the workspace", body)
		}
	}
}

func TestPatchAcceptsAbsoluteParentAndSymlinkPaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	writeFile(t, outside, "before\n")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyPatch(root, wrapped("*** Update File: link\n@@\n-before\n+after\n*** Add File: ../added\n+parent\n")); err != nil {
		t.Fatal(err)
	}
	if readText(t, outside) != "after\n" || readText(t, filepath.Join(parent, "added")) != "parent\n" {
		t.Fatal("outside edits failed")
	}
	patch := fmt.Sprintf("*** Update File: %s\n*** Move to: %s\n", outside, filepath.Join(parent, "moved"))
	if _, _, err := applyPatch(root, wrapped(patch)); err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(parent, "moved")) != "after\n" {
		t.Fatal("outside move failed")
	}
}

func TestApplicationFailureReportsCompletedFailedAndUnattemptedOperations(t *testing.T) {
	root := t.TempDir()
	// Each target is absent during preparation, but the first operation
	// creates a file where the second needs a parent directory.
	_, _, err := applyPatch(root, wrapped("*** Add File: parent\n+file\n*** Add File: parent/child\n+child\n*** Add File: later\n+later\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "apply: failed Added parent/child:") ||
		!strings.Contains(err.Error(), "Completed:\nAdded parent\nNot attempted:\nAdded later") {
		t.Errorf("error = %v", err)
	}
	if readText(t, filepath.Join(root, "parent")) != "file\n" {
		t.Error("the completed operation was undone")
	}
}

func TestConcurrentMovesNeverReplaceTheDestination(t *testing.T) {
	root := t.TempDir()
	const count = 32
	for i := range count {
		name := fmt.Sprintf("source-%d", i)
		writeFile(t, filepath.Join(root, name), name)
	}
	start := make(chan struct{})
	results := make([]error, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() {
			<-start
			results[i] = moveFile(filepath.Join(root, fmt.Sprintf("source-%d", i)), filepath.Join(root, "nested/destination"))
		})
	}
	close(start)
	workers.Wait()
	winner := -1
	for i, err := range results {
		source := fmt.Sprintf("source-%d", i)
		if err == nil {
			if winner != -1 {
				t.Fatal("more than one move succeeded")
			}
			winner = i
			if _, err := os.Lstat(filepath.Join(root, source)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("winning source still exists: %v", err)
			}
		} else {
			if !errors.Is(err, os.ErrExist) {
				t.Errorf("move %d: %v", i, err)
			}
			if readText(t, filepath.Join(root, source)) != source {
				t.Errorf("failed move changed source %d", i)
			}
		}
	}
	if winner == -1 {
		t.Fatal("no move succeeded")
	}
	if got := readText(t, filepath.Join(root, "nested/destination")); got != fmt.Sprintf("source-%d", winner) {
		t.Fatalf("destination was replaced: %q", got)
	}
}
