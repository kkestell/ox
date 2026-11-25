package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeFiles writes each file's text beneath dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// numbered is lines l1 through ln, with line i replaced by edits[i].
func numbered(n int, edits map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if line, ok := edits[i]; ok {
			b.WriteString(line + "\n")
		} else {
			fmt.Fprintf(&b, "l%02d\n", i)
		}
	}
	return b.String()
}

// listedChanges calls workspace/list_changes with params.
func listedChanges(t *testing.T, c *client, params map[string]string) changes {
	t.Helper()
	var result changes
	raw := c.call("workspace/list_changes", params)
	if err := json.Unmarshal(raw, &result); err != nil || result.Files == nil {
		t.Fatalf("workspace/list_changes %v returned %s", params, raw)
	}
	return result
}

// paths lists the files' paths in order.
func paths(files []changedFile) []string {
	var listed []string
	for _, f := range files {
		listed = append(listed, f.Path)
	}
	return listed
}

// find returns the file at path.
func find(t *testing.T, files []changedFile, path string) changedFile {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("%s is not among %v", path, paths(files))
	return changedFile{}
}

// A session's changes are the files in its directory that differ from where
// its branch left the workspace's branch, as hunks or the reason they cannot
// be shown; a session in the workspace directory shows only uncommitted
// changes.
func TestListChanges(t *testing.T) {
	repo := gitRepo(t)
	sub := filepath.Join(repo, "sub")
	writeFiles(t, sub, map[string]string{"file.txt": numbered(20, nil), "still.txt": "still\n", "tail.txt": "first\nold"})
	runGit(t, "-C", repo, "add", ".")
	runGit(t, "-C", repo, "commit", "-q", "-m", "numbered")

	s := worktreeServer(t, openStore(t), t.TempDir())
	c := connect(t, s)
	workspaceID := addWorkspace(t, c, sub)
	sess := worktreeSession(t, c, workspaceID)
	dir := sess.Directory

	writeFiles(t, dir, map[string]string{
		"file.txt": numbered(20, map[int]string{2: "two"}),
		"gone.txt": "gone\n",
		"bin.dat":  "\x00\x01",
	})
	runGit(t, "-C", dir, "add", ".")
	runGit(t, "-C", dir, "commit", "-q", "-m", "edit")
	runGit(t, "-C", dir, "rm", "-q", "gone.txt")
	runGit(t, "-C", dir, "commit", "-q", "-m", "remove")
	writeFiles(t, dir, map[string]string{
		"file.txt":       numbered(20, map[int]string{2: "two", 18: "eighteen"}),
		"tail.txt":       "first\nnew",
		"new.txt":        "hello\nworld\n",
		"untracked.bin":  "\x00\x02",
		"ignored.log":    "ignored\n",
		"../outside.txt": "outside\n",
	})
	// A stat-dirty file is one a listing that refreshed the index would
	// rewrite it for.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "still.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, sub, map[string]string{"main-only.txt": "main\n"})
	runGit(t, "-C", repo, "add", ".")
	runGit(t, "-C", repo, "commit", "-q", "-m", "main moves on")

	index := runGit(t, "-C", dir, "rev-parse", "--path-format=absolute", "--git-path", "index")
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	result := listedChanges(t, c, map[string]string{"session_id": sess.SessionID})
	if after, err := os.Stat(index); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("listing changes rewrote the index: %v", err)
	}

	if want := "ox/" + filepath.Base(sess.Worktree); result.Branch != want {
		t.Errorf("branch %q, want %q", result.Branch, want)
	}
	if got, want := paths(result.Files), []string{"bin.dat", "file.txt", "new.txt", "tail.txt", "untracked.bin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	wantFile := changedFile{Path: "file.txt", Status: "modified", Hunks: []hunk{
		{OldStart: 1, NewStart: 1, Lines: []string{" l01", "-l02", "+two", " l03", " l04", " l05"}},
		{OldStart: 15, NewStart: 15, Lines: []string{" l15", " l16", " l17", "-l18", "+eighteen", " l19", " l20"}},
	}}
	if got := find(t, result.Files, "file.txt"); !reflect.DeepEqual(got, wantFile) {
		t.Errorf("file.txt is %+v, want %+v", got, wantFile)
	}
	wantNew := changedFile{Path: "new.txt", Status: "added", Hunks: []hunk{{OldStart: 0, NewStart: 1, Lines: []string{"+hello", "+world"}}}}
	if got := find(t, result.Files, "new.txt"); !reflect.DeepEqual(got, wantNew) {
		t.Errorf("new.txt is %+v, want %+v", got, wantNew)
	}
	// Git's "\ No newline at end of file" lines are not the file's.
	wantTail := changedFile{Path: "tail.txt", Status: "modified", Hunks: []hunk{{OldStart: 1, NewStart: 1, Lines: []string{" first", "-old", "+new"}}}}
	if got := find(t, result.Files, "tail.txt"); !reflect.DeepEqual(got, wantTail) {
		t.Errorf("tail.txt is %+v, want %+v", got, wantTail)
	}
	for _, path := range []string{"bin.dat", "untracked.bin"} {
		want := changedFile{Path: path, Status: "added", Reason: "file is binary"}
		if got := find(t, result.Files, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s is %+v, want %+v", path, got, want)
		}
	}

	// The workspace directory forks from its own HEAD, so it shows only
	// uncommitted and untracked files, including a deletion, a diff too large
	// to show, and one that is not UTF-8.
	runGit(t, "-C", sub, "rm", "-q", "still.txt")
	writeFiles(t, sub, map[string]string{
		"big.txt":    strings.Repeat("a line of text\n", 80000),
		"latin1.txt": "caf\xe9\n",
		"fresh.txt":  "",
	})
	runGit(t, "-C", sub, "add", "big.txt", "latin1.txt")
	result = listedChanges(t, c, map[string]string{"workspace_id": workspaceID})
	if result.Branch != "main" {
		t.Errorf("the workspace's branch is %q", result.Branch)
	}
	want := []changedFile{
		{Path: "big.txt", Status: "added", Reason: "diff is larger than 1 MiB"},
		{Path: "fresh.txt", Status: "added", Hunks: []hunk{}},
		{Path: "latin1.txt", Status: "added", Reason: "file is not UTF-8 text"},
		{Path: "still.txt", Status: "deleted", Hunks: []hunk{{OldStart: 1, NewStart: 0, Lines: []string{"-still"}}}},
	}
	if !reflect.DeepEqual(result.Files, want) {
		t.Errorf("the workspace listed %+v, want %+v", result.Files, want)
	}

	// A branch with no history in common with the workspace's has no base.
	orphan := worktreeSession(t, c, workspaceID)
	runGit(t, "-C", orphan.Directory, "checkout", "-q", "--orphan", "other")
	runGit(t, "-C", orphan.Directory, "commit", "-q", "-m", "orphan")
	response := c.request("workspace/list_changes", map[string]string{"session_id": orphan.SessionID})
	if response.Error == nil || response.Error.Code != -32000 || response.Error.Message != "no commit in common with the workspace's branch" {
		t.Errorf("an orphan branch returned %.200s %+v", response.Result, response.Error)
	}

	// A repository with no commits yet lists every file as added.
	fresh := t.TempDir()
	runGit(t, "-C", fresh, "init", "-q", "-b", "main")
	writeFiles(t, fresh, map[string]string{"staged.txt": "staged\n", "untracked.txt": "new\n"})
	runGit(t, "-C", fresh, "add", "staged.txt")
	result = listedChanges(t, c, map[string]string{"workspace_id": addWorkspace(t, c, fresh)})
	want = []changedFile{
		{Path: "staged.txt", Status: "added", Hunks: []hunk{{OldStart: 0, NewStart: 1, Lines: []string{"+staged"}}}},
		{Path: "untracked.txt", Status: "added", Hunks: []hunk{{OldStart: 0, NewStart: 1, Lines: []string{"+new"}}}},
	}
	if result.Branch != "main" || !reflect.DeepEqual(result.Files, want) {
		t.Errorf("a repository with no commits listed %q %+v, want main %+v", result.Branch, result.Files, want)
	}

	plain := addWorkspace(t, c, t.TempDir())
	response = c.request("workspace/list_changes", map[string]string{"workspace_id": plain})
	if response.Error == nil || response.Error.Code != -32000 || response.Error.Message != "not a git repository" {
		t.Errorf("a directory outside a repository returned %.200s %+v", response.Result, response.Error)
	}
}
