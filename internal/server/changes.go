package server

import (
	"errors"
	"fmt"
	"os"
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

// changedFile is one changed file: its hunks, or, when they cannot be shown,
// the reason instead.
type changedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Hunks  []hunk `json:"hunks"`
	Reason string `json:"reason,omitempty"`
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

// changed builds a file's entry from its hunks, or from the error that kept
// them from being read, so a file has hunks exactly when it has no reason.
func changed(path, status string, hunks []hunk, err error) changedFile {
	if err != nil {
		return changedFile{Path: path, Status: status, Reason: err.Error()}
	}
	if hunks == nil {
		hunks = []hunk{}
	}
	return changedFile{Path: path, Status: status, Hunks: hunks}
}

// listChanges lists the files in dir that differ from where its branch left
// workspace's branch: commits since then, uncommitted edits, and untracked
// files Git does not ignore, with paths relative to dir. Listing writes
// nothing in the repository.
func listChanges(workspace, dir string) (changes, error) {
	const step = "list changes"
	run := func(dir string, args ...string) (string, error) {
		return git(step, append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	}
	branch, err := run(dir, "branch", "--show-current")
	if err != nil {
		if _, ok := errors.AsType[*gitError](err); ok {
			return changes{}, errNotRepository
		}
		return changes{}, err
	}
	base, err := forkPoint(run, workspace, dir)
	if err != nil {
		return changes{}, err
	}
	// git diff refreshes the index whenever it finds a file whose stat
	// changed but whose content did not, optional locks or not; with
	// diff.autoRefreshIndex off it still compares and skips such files but
	// writes nothing.
	diff, err := run(dir, "-c", "diff.autoRefreshIndex=false", "diff", "--numstat", "-z", "-p", "--no-renames", "--no-color", "--no-ext-diff",
		"--no-textconv", "--unified=3", "--relative", "--src-prefix=a/", "--dst-prefix=b/", base, "--")
	if err != nil {
		return changes{}, err
	}
	files, err := parseDiff(diff)
	if err != nil {
		return changes{}, err
	}
	untracked, err := run(dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return changes{}, err
	}
	for path := range strings.SplitSeq(strings.TrimSuffix(untracked, "\x00"), "\x00") {
		if path == "" {
			continue
		}
		text, err := readFile(dir, path)
		files = append(files, changed(path, "added", added(text), err))
	}
	slices.SortFunc(files, func(a, b changedFile) int { return strings.Compare(a.Path, b.Path) })
	return changes{strings.TrimSpace(branch), files}, nil
}

// forkPoint returns where dir's branch left workspace's branch: the merge
// base of their HEADs. A workspace whose HEAD is unborn has no commits, so
// every file in it is new, and the base is the empty tree.
func forkPoint(run func(dir string, args ...string) (string, error), workspace, dir string) (string, error) {
	head, err := run(workspace, "rev-parse", "--verify", "-q", "HEAD")
	if quietFailure(err) {
		tree, err := run(dir, "hash-object", "-t", "tree", os.DevNull)
		return strings.TrimSpace(tree), err
	}
	if err != nil {
		return "", err
	}
	base, err := run(dir, "merge-base", "HEAD", strings.TrimSpace(head))
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

// added is the one hunk of a new file's text, every line added from line 1,
// or none for an empty file.
func added(text string) []hunk {
	if text == "" {
		return nil
	}
	var lines []string
	for line := range strings.Lines(text) {
		lines = append(lines, "+"+strings.TrimSuffix(line, "\n"))
	}
	return []hunk{{OldStart: 0, NewStart: 1, Lines: lines}}
}

// parseDiff reads the output of git diff --numstat -z -p: NUL-terminated
// numstat records, which name each file without quoting, ended by an empty
// record, then the patch, one section per record in the same order, each
// starting with a line beginning "diff --git ".
func parseDiff(out string) ([]changedFile, error) {
	var paths []string
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
		paths = append(paths, fields[2])
	}
	sections := splitSections(out)
	if len(sections) != len(paths) {
		return nil, fmt.Errorf("list changes: %d numstat records but %d patch sections", len(paths), len(sections))
	}
	files := []changedFile{}
	for i, section := range sections {
		files = append(files, parseSection(paths[i], section))
	}
	return files, nil
}

// splitSections splits a patch into its files' sections. No content line can
// begin "diff --git ", since each begins with a space, +, -, or \.
func splitSections(patch string) []string {
	var sections []string
	start := -1
	for i := 0; i < len(patch); {
		if strings.HasPrefix(patch[i:], "diff --git ") {
			if start >= 0 {
				sections = append(sections, patch[start:i])
			}
			start = i
		}
		next := strings.IndexByte(patch[i:], '\n')
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

// parseSection reads one file's patch section into its entry.
func parseSection(path, section string) changedFile {
	status := "modified"
	var hunks []hunk
	var reason error
	for line := range strings.Lines(section) {
		line = strings.TrimSuffix(line, "\n")
		if hunks == nil {
			switch {
			case strings.HasPrefix(line, "new file mode "):
				status = "added"
			case strings.HasPrefix(line, "deleted file mode "):
				status = "deleted"
			case strings.HasPrefix(line, "Binary files "):
				reason = errBinary
			}
		}
		switch {
		case strings.HasPrefix(line, "@@ "):
			hunks = append(hunks, parseHunkHeader(line))
		case hunks == nil, line == "", line[0] == '\\':
		default:
			last := &hunks[len(hunks)-1]
			last.Lines = append(last.Lines, line)
		}
	}
	switch {
	case reason != nil:
	case len(section) > maxFileSize:
		reason = errDiffTooLarge
	case !utf8.ValidString(section):
		reason = errNotText
	}
	return changed(path, status, hunks, reason)
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
