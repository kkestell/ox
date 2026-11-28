package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// changes is workspace/list_changes's result: the directory's branch, empty
// when HEAD is detached, and its changed files sorted by path.
type changes struct {
	Branch string        `json:"branch,omitempty"`
	Files  []changedFile `json:"files"`
}

// changedFile is one changed file: the lines it adds and removes, when Git
// counts them, and its hunks, or, when they cannot be shown, the reason
// instead.
type changedFile struct {
	Path   string      `json:"path"`
	Status string      `json:"status"`
	Counts *lineCounts `json:"counts,omitempty"`
	Hunks  []hunk      `json:"hunks"`
	Reason string      `json:"reason,omitempty"`
}

// lineCounts is how many lines a file's change adds and removes.
type lineCounts struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// hunk is one section of a file's diff. Each line keeps git's one-character
// prefix: a space for context, - for a removed line, + for an added one.
type hunk struct {
	OldStart int      `json:"old_start"`
	NewStart int      `json:"new_start"`
	Lines    []string `json:"lines"`
}

var (
	errNotRepository = errors.New("not a git repository")
	errNoCommonBase  = errors.New("no commit in common with the workspace's branch")
	errDiffTooLarge  = errors.New("diff is larger than 1 MiB")
)

// changed builds a file's entry from its counts and its hunks, or from the
// error that kept them from being read, so a file has hunks exactly when it
// has no reason.
func changed(path, status string, counts *lineCounts, hunks []hunk, err error) changedFile {
	if err != nil {
		return changedFile{Path: path, Status: status, Counts: counts, Reason: err.Error()}
	}
	if hunks == nil {
		hunks = []hunk{}
	}
	return changedFile{Path: path, Status: status, Counts: counts, Hunks: hunks}
}

// listChanges lists the files in dir that differ from where its branch left
// workspace's branch: commits since then, uncommitted edits, and untracked
// files Git does not ignore, with paths relative to dir, each listed once.
// Listing writes nothing in the repository.
func listChanges(workspace, dir string) (changes, error) {
	run := func(dir string, args ...string) (string, error) { return listGit(dir, nil, "", args...) }
	branch, err := run(dir, "branch", "--show-current")
	if failed, ok := errors.AsType[*gitError](err); ok && strings.HasPrefix(failed.stderr, "fatal: not a git repository") {
		return changes{}, errNotRepository
	}
	if err != nil {
		return changes{}, err
	}
	base, err := forkPoint(run, workspace, dir)
	if err != nil {
		return changes{}, err
	}
	untracked, err := run(dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return changes{}, err
	}
	restored, env, cleanup, err := restoreRemoved(dir, base, untracked)
	if err != nil {
		return changes{}, err
	}
	defer cleanup()
	// git diff refreshes the index whenever it finds a file whose stat
	// changed but whose content did not, optional locks or not; with
	// diff.autoRefreshIndex off it still compares and skips such files but
	// writes nothing. Blank context lines keep their space, and a submodule
	// shows as its commit whenever the commit changed, so every section reads
	// the same and every change is listed whatever the repository's
	// configuration.
	diff, err := listGit(dir, env, "", "-c", "diff.autoRefreshIndex=false", "-c", "diff.suppressBlankEmpty=false",
		"diff", "--numstat", "-z", "-p", "--no-renames", "--no-color", "--no-ext-diff", "--no-textconv", "--submodule=short",
		"--ignore-submodules=none", "--unified=3", "--relative", "--src-prefix=a/", "--dst-prefix=b/", base, "--")
	if err != nil {
		return changes{}, err
	}
	files, err := parseDiff(diff)
	if err != nil {
		return changes{}, err
	}
	// A file staged after the untracked scan is in both lists. The diff ran
	// later, so its entry is the current one.
	listed := map[string]bool{}
	for _, file := range files {
		listed[file.Path] = true
	}
	for path := range nulSeparated(untracked) {
		if !restored[path] && !listed[localName(path)] {
			files = append(files, untrackedFile(dir, path))
		}
	}
	slices.SortFunc(files, func(a, b changedFile) int { return strings.Compare(a.Path, b.Path) })
	return changes{strings.TrimSpace(branch), files}, nil
}

// listGit runs git in dir for a listing, without git's optional locks, with
// env added to its environment and stdin as its input.
func listGit(dir string, env []string, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return gitCommand("list changes", cmd)
}

// nulSeparated yields each path in git's NUL-terminated output.
func nulSeparated(out string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for path := range strings.SplitSeq(out, "\x00") {
			if path != "" && !yield(path) {
				return
			}
		}
	}
}

// restoreRemoved finds the files in untracked that the index has deleted
// since base, as git rm --cached leaves a file: removed from the index but
// kept on disk. When there are any, env points the diff at a copy of the
// index holding base's version of each, so the diff shows each file's change
// from base once, and cleanup removes the copy.
func restoreRemoved(dir, base, untracked string) (restored map[string]bool, env []string, cleanup func(), err error) {
	removed, err := listGit(dir, nil, "", "diff", "--cached", "--name-only", "-z", "--no-renames", "--relative", "--diff-filter=D", base, "--")
	if err != nil {
		return nil, nil, nil, err
	}
	deleted := map[string]bool{}
	for path := range nulSeparated(removed) {
		deleted[path] = true
	}
	restored = map[string]bool{}
	var paths []string
	for path := range nulSeparated(untracked) {
		if deleted[path] {
			restored[path] = true
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return restored, nil, func() {}, nil
	}
	index, err := restoredIndex(dir, base, paths)
	if err != nil {
		return nil, nil, nil, err
	}
	return restored, []string{"GIT_INDEX_FILE=" + index}, func() { os.RemoveAll(filepath.Dir(index)) }, nil
}

// restoredIndex copies dir's index into a new temporary directory and sets
// each of paths in the copy to its version in base, and returns the copy's
// path. The repository is left as it was.
func restoredIndex(dir, base string, paths []string) (string, error) {
	real, err := listGit(dir, nil, "", "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(strings.TrimSpace(real))
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "ox-index-")
	if err != nil {
		return "", err
	}
	index := filepath.Join(tmp, "index")
	if err := os.WriteFile(index, data, 0o600); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	_, err = listGit(dir, []string{"GIT_INDEX_FILE=" + index}, strings.Join(paths, "\x00"),
		"--literal-pathspecs", "reset", "-q", "--pathspec-from-file=-", "--pathspec-file-nul", base)
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return index, nil
}

// forkPoint returns where dir's branch left workspace's branch: the merge
// base of their HEADs. A workspace whose HEAD is unborn has no commits, so
// every file in it is new, and the base is the empty tree. A directory in
// another repository, which lacks the workspace's HEAD, has no common base.
func forkPoint(run func(dir string, args ...string) (string, error), workspace, dir string) (string, error) {
	head, err := run(workspace, "rev-parse", "--verify", "-q", "HEAD")
	if quietFailure(err) {
		tree, err := run(dir, "hash-object", "-t", "tree", os.DevNull)
		return strings.TrimSpace(tree), err
	}
	if err != nil {
		return "", err
	}
	head = strings.TrimSpace(head)
	_, err = run(dir, "rev-parse", "--verify", "-q", head+"^{commit}")
	if quietFailure(err) {
		return "", errNoCommonBase
	}
	if err != nil {
		return "", err
	}
	base, err := run(dir, "merge-base", "HEAD", head)
	if quietFailure(err) {
		return "", errNoCommonBase
	}
	return strings.TrimSpace(base), err
}

// quietFailure reports whether err is a git command's exit 1 with nothing on
// stderr, which rev-parse -q and merge-base use to answer no.
func quietFailure(err error) bool {
	failed, ok := errors.AsType[*gitError](err)
	return ok && failed.code == 1 && failed.stderr == ""
}

// untrackedFile builds an untracked file's entry from what Git would record
// for it: a symlink's target, or a regular file's text, as one hunk of added
// lines from line 1, with every line counted as added.
func untrackedFile(dir, path string) changedFile {
	text, counts, err := untrackedText(dir, path)
	if err != nil {
		return changed(localName(path), "added", counts, nil, err)
	}
	hunks, err := added(text)
	return changed(localName(path), "added", counts, hunks, err)
}

// untrackedText returns a symlink's target or a regular file's text, and its
// line count as Git's numstat gives it, or none for a binary file. A regular
// file too large or not text enough to show still has its lines counted to
// its end.
func untrackedText(dir, path string) (string, *lineCounts, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	info, err := root.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(path)
		if err != nil {
			return "", nil, err
		}
		return target, &lineCounts{Added: 1}, nil
	}
	f, err := openRegular(root, path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return "", nil, err
	}
	if isBinary(head) {
		return "", nil, errBinary
	}
	lines := lineCounter{}
	lines.Write(head)
	if _, err := io.Copy(&lines, f); err != nil {
		return "", nil, err
	}
	shown, err := text(head)
	return shown, &lineCounts{Added: lines.count()}, err
}

// lineCounter counts the lines written to it, including a last line without
// a newline.
type lineCounter struct {
	newlines int
	open     bool
}

func (c *lineCounter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		c.newlines += bytes.Count(p, []byte{'\n'})
		c.open = p[len(p)-1] != '\n'
	}
	return len(p), nil
}

func (c *lineCounter) count() int {
	if c.open {
		return c.newlines + 1
	}
	return c.newlines
}

// added is the one hunk of a new file's text, every line added from line 1,
// or none for an empty file. Its diff, a + before each line, is held to the
// limit of a tracked file's.
func added(text string) ([]hunk, error) {
	if text == "" {
		return nil, nil
	}
	var lines []string
	size := 0
	for line := range strings.Lines(text) {
		line = "+" + strings.TrimSuffix(line, "\n")
		size += len(line) + 1
		lines = append(lines, line)
	}
	if size > maxFileSize {
		return nil, errDiffTooLarge
	}
	return []hunk{{OldStart: 0, NewStart: 1, Lines: lines}}, nil
}

// parseDiff reads the output of git diff --numstat -z -p: NUL-terminated
// numstat records, which name each file without quoting, ended by an empty
// record, then the patch, one section per record in the same order, each
// starting with a line beginning "diff --git ".
func parseDiff(out string) ([]changedFile, error) {
	var paths []string
	var counts []*lineCounts
	for out != "" {
		record, rest, found := strings.Cut(out, "\x00")
		if !found {
			return nil, fmt.Errorf("list changes: unterminated numstat record %q", record)
		}
		out = rest
		if record == "" {
			break
		}
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("list changes: unexpected numstat record %q", record)
		}
		paths = append(paths, localName(fields[2]))
		counts = append(counts, numstatCounts(fields[0], fields[1]))
	}
	sections := splitSections(out)
	if len(sections) != len(paths) {
		return nil, fmt.Errorf("list changes: %d numstat records but %d patch sections", len(paths), len(sections))
	}
	files := []changedFile{}
	for i, section := range sections {
		files = append(files, parseSection(paths[i], counts[i], section))
	}
	return files, nil
}

// numstatCounts reads a numstat record's added and removed lines, which Git
// gives as - for a binary file.
func numstatCounts(added, removed string) *lineCounts {
	a, errA := strconv.Atoi(added)
	r, errR := strconv.Atoi(removed)
	if errA != nil || errR != nil {
		return nil
	}
	return &lineCounts{Added: a, Removed: r}
}

// splitSections splits a patch into its files' sections. No content line can
// begin "diff --git ", since each begins with a space, +, -, or \. A file
// whose type changed, such as a regular file that became a symlink, has two
// parts under the same "diff --git " line, its old type's deletion and its
// new type's addition, which together are its section.
func splitSections(patch string) []string {
	var sections []string
	start := -1
	header := ""
	for i := 0; i < len(patch); {
		next := strings.IndexByte(patch[i:], '\n')
		line := patch[i:]
		if next >= 0 {
			line = patch[i : i+next]
		}
		if strings.HasPrefix(line, "diff --git ") && line != header {
			if start >= 0 {
				sections = append(sections, patch[start:i])
			}
			start = i
			header = line
		}
		if next < 0 {
			break
		}
		i += next + 1
	}
	if start >= 0 {
		sections = append(sections, patch[start:])
	}
	return sections
}

// parseSection reads one file's patch section into its entry. A file whose
// type changed was both deleted and added, so it is modified.
func parseSection(path string, counts *lineCounts, section string) changedFile {
	var deleted, created, header bool
	var hunks []hunk
	var reason error
	for line := range strings.Lines(section) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			header = true
		case strings.HasPrefix(line, "@@ "):
			header = false
			hunks = append(hunks, parseHunkHeader(line))
		case header:
			switch {
			case strings.HasPrefix(line, "new file mode "):
				created = true
			case strings.HasPrefix(line, "deleted file mode "):
				deleted = true
			case strings.HasPrefix(line, "Binary files "):
				reason = errBinary
			}
		case line == "", line[0] == '\\':
		default:
			last := &hunks[len(hunks)-1]
			last.Lines = append(last.Lines, line)
		}
	}
	status := "modified"
	switch {
	case created && !deleted:
		status = "added"
	case deleted && !created:
		status = "deleted"
	}
	switch {
	case reason != nil:
	case len(section) > maxFileSize:
		reason = errDiffTooLarge
	case !utf8.ValidString(section):
		reason = errNotText
	}
	return changed(path, status, counts, hunks, reason)
}

// parseHunkHeader reads a hunk header, "@@ -a[,b] +c[,d] @@", into an empty
// hunk starting at old line a and new line c.
func parseHunkHeader(line string) hunk {
	fields := strings.Fields(line)
	start := func(field string) int {
		number, _, _ := strings.Cut(field[1:], ",")
		n, _ := strconv.Atoi(number)
		return n
	}
	return hunk{OldStart: start(fields[1]), NewStart: start(fields[2]), Lines: []string{}}
}
