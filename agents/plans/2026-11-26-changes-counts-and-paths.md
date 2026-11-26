# Changes counts, paths, and file selection

## Goal

The Changes view and the file tree can misreport what changed: rows carry no
line counts, a diff too large to show counts as zero, a file staged during a
poll is listed twice, a file that replaced a folder takes the folder's color, a
revealed file is lost after switching sessions, and on macOS an accented
filename is spelled differently by Files and Changes, losing its color and
opening twice. After this change every changed file has Git's counts, listed
once, colored by its own change, spelled the same in Files and Changes, and the
Changes tab scrolls to its revealed file whenever that file's card appears.

Resolves [#149](https://github.com/kkestell/ox/issues/149): audit findings F6,
F14, F15, F16, F21, and F28 in `agents/audits/2026-11-26-main.md`.

## Related code

- `internal/server/changes.go` — `listChanges` scans untracked paths, runs one
  `git diff --numstat -z -p`, and appends untracked files. `parseDiff` reads
  numstat records but keeps only their paths. `untrackedFile` and `added` build
  an untracked file's hunk.
- `internal/server/files.go` — `listFolder` returns directory entry names as
  the filesystem spells them; `readFile` and `maxFileSize`.
- `internal/server/changes_test.go` — `TestListChanges` covers tracked,
  untracked, binary, oversized, and type-changed files through
  `workspace/list_changes`.
- `internal/server/methods_test.go` — `TestWorkspaceFiles` covers
  `workspace/list_folder`.
- `cmd/ox/web/src/protocol.ts` — `ChangedFile`.
- `cmd/ox/web/src/diff.ts` — `counts(lines)`, `fileCounts` (counts from
  displayable hunks), `statuses` (file and folder statuses in one map).
- `cmd/ox/web/src/Changes.tsx` — `createListing` (totals, unused `change`
  lookup, `status`), exported `BranchSummary`, `ChangesList` rows,
  `ChangesView`'s reveal effect, `FileCard`.
- `cmd/ox/web/src/FileTree.tsx` — `FolderRow` and `FileRow` both color with
  `listing.status`.
- `cmd/ox/web/src/TabArea.tsx`, `TabPicker.tsx` — read `listing.total()` for
  the Changes tab; unchanged.
- `cmd/ox/web/diff.test.mjs`, `cmd/ox/web/e2e/workspace.spec.ts` — the
  `statuses` unit test and the Changes specs.

## Decisions

- **Counts come from Git, independent of hunks (F6).** Each `changedFile` gets
  `counts`, `{added, removed}`, read from its numstat record. Git writes `-` for
  a binary file, which leaves `counts` absent. An untracked file's counts are
  its line count as added lines, counting a last line without a newline, as
  Git's numstat would: 1 for a symlink, the whole file's lines for a regular
  file, including one over 1 MiB (read through to its end) and one that is not
  UTF-8. A binary untracked file (a NUL in its first 8000 bytes, as `readFile`
  decides) and one that cannot be read have no counts. The page's totals, the
  Changes tab label, each card, and each Changes list row use `counts`;
  `fileCounts` goes. `counts(lines)` stays for each hunk's header.
- **One entry per path (F14).** `listChanges` adds an untracked path only when
  the diff did not already list it, besides the existing `restore` exclusion.
  The diff ran later, so its entry is the current one. This is the one place the
  tracked and untracked lists merge, keyed by the path as reported (see F21).
- **File and folder statuses are separate (F15).** `statuses` returns
  `{ files, folders }`: `files` maps each changed file's path to its own status,
  `folders` each folder above one to the aggregate. The listing exposes
  `fileStatus(path)` and `folderStatus(path)`; `FileRow` reads the first,
  `FolderRow` the second. A file `sub` that replaced the folder `sub/` is
  `added` as a file row.
- **Reveal applies when its card exists, once (F16).** Stays inside
  `Changes.tsx`; `TabArea`, `App`, and `tabs.ts` are unchanged. `ChangesView`
  keeps `applied`, the last reveal object it scrolled to, in a plain local
  variable. Its effect's compute returns the reveal when it differs from
  `applied` and the listing holds a file at its path; the apply scrolls to the
  card and sets `applied`. A poll that relists the file finds the reveal applied
  and leaves the user's scroll alone. `tabs.reveal` builds a new reveal object
  per click, so a second click on the same file scrolls again. A remount, from
  switching sessions or tabs, starts with nothing applied, so the saved reveal
  scrolls once its card renders.
- **macOS paths are reported in composed form (F21).** macOS filesystems treat
  the composed (NFC) and decomposed (NFD) spellings of a name as one name, and
  Git there reports composed paths (`core.precomposeUnicode`), while `ReadDir`
  returns the spelling on disk. On darwin the daemon converts every name and
  path it reports, in `workspace/list_folder` and `workspace/list_changes`, to
  NFC with one function, `localName` in `files.go`, using
  `golang.org/x/text/unicode/norm` (new module dependency). Elsewhere it returns
  names unchanged, so a filesystem that holds both spellings lists both. Paths
  the page sends back in NFC resolve to the same file on darwin. In
  `listChanges` the conversion applies to the numstat paths and the untracked
  paths before the F14 merge, so a tracked composed entry and an untracked
  decomposed spelling of the same file, as a repository with
  `core.precomposeUnicode=false` reports, merge into one. The `restore` map and
  `restoredIndex` keep Git's raw paths, since both sides come from Git. Tab
  keys and the status map need no change once both feeds agree.
- **Unused listing parts go (F28).** Remove `files` and `change` from
  `createListing` and the `export` from `BranchSummary`.
- **Requirements.** **Changes view** (rows carry folder and counts; one row per
  file), **Change diffs** (a card over 1 MiB still shows its counts), **Changes
  updates** (a poll during staging lists the file once), **File tree** (exact
  color for a file row; accented names colored), **Tabs** (the Changes tab's
  totals include oversized diffs; one tab per file). Each listing is a poll with
  no request the page sends and waits on besides `workspace/list_changes`, which
  stays read-only; a lost reply is replaced by the next tick.
- **Construction.** One entry per path: the merge in `listChanges`. Counts exist
  exactly when Git counts lines: `counts` is a pointer filled from numstat or
  the untracked count, independent of `hunks`/`reason`. A file row cannot read a
  folder's status: the two maps are separate and each row kind reads its own.
  One spelling per path on darwin: `localName` at the daemon's output.
- **Ownership.** One new state, `applied` in `ChangesView`, owned by the view
  instance and dropped with it. It exists so a poll does not re-scroll after the
  user scrolls away. The only interleaving is the listing arriving before or
  after the reveal, and the compute covers both.

## Check your assumptions

None.

## Test plan

- Counts for tracked, untracked, binary, oversized, and non-UTF-8 files: server,
  `TestListChanges`. Every expected `changedFile` gets its `counts`; `big.txt`
  (tracked, over 1 MiB) has `{80000, 0}`, `many.txt` (untracked, diff over
  1 MiB) has `{400000, 0}`, `latin1.txt` `{1, 0}`, `bin.dat` and
  `untracked.bin` none. Add an untracked file over 1 MiB to its second listing
  and expect its full line count with the size reason.
- A file staged while the listing runs appears once: server, new
  `TestListChangesStagedDuringListing` in `changes_test.go`. It puts a stand-in
  `git` first on `PATH` that, when called with `--numstat`, stages a new file
  with the real `git` and then runs the real command. No existing test can hold
  Git between the untracked scan and the diff.
- Folder names and changed paths agree: server, new
  `TestChangedPathsMatchFolderNames` in `changes_test.go`, the only test that
  calls both methods. A committed then edited file and an untracked file, both
  named in NFD; `list_folder`'s names equal `list_changes`'s paths, and on
  darwin they are NFC. Off darwin, an NFC twin of the untracked file is created
  and both spellings are listed by both methods.
- File and folder statuses for one path: unit, `diff.test.mjs`. Update the
  existing case to the `{ files, folders }` shape, and add `sub` added as a file
  with `sub/file.txt` deleted: the file status is `added`, the folder status
  `deleted`.
- Row counts: e2e, "the panel's Changes list shows the branch, the totals, and
  each changed file" expects `button "committed.txt +2 −1"` and
  `button "untracked.txt +0 −0"`.
- An oversized diff counts in the tab total: e2e, "the Changes tab's label
  counts the lines added and removed" also writes an untracked 70000-line file
  over 1 MiB and expects `+70002 −1`.
- Reveal after switching sessions: e2e, "a file in the Changes list opens the
  Changes tab at its card" continues, as the carry test does, by sending a
  prompt so the draft becomes session "first", starting session "second",
  returning to "first", and expecting `file-19.txt`'s card in view.
- A poll keeps the user's scroll after a reveal: e2e, new clocked spec "a poll
  keeps the Changes tab's scroll after a reveal": reveal `file-19.txt`, scroll
  the Changes region to its top, run the clock one second, and expect
  `committed.txt`'s card in view. No existing clocked spec reveals a file.

## Implementation plan

1. `internal/server/files.go`: add `localName(name string) string`, NFC on
   darwin and the name unchanged elsewhere, with a comment giving the macOS and
   Git reason. `listFolder` reports `localName(e.Name())`. Add
   `golang.org/x/text` to `go.mod` and `go.sum`.
2. `internal/server/changes.go`: add `lineCounts{Added, Removed}` and
   a `Counts *lineCounts` field, JSON `counts` with `omitempty`, to
   `changedFile`; `changed` takes the counts. `parseDiff` parses each numstat record's counts (`-` gives
   none) and its path through `localName`. `untrackedFile` reads a regular file
   once, keeping its first `maxFileSize`+1 bytes for the hunk and counting every
   line to the end; a symlink counts 1. `listChanges` builds the set of the
   diff's paths and appends an untracked path, through `localName`, only when
   it is not in `restore` and not in that set.
3. `internal/server/changes_test.go`: the counts expectations and the two new
   tests above.
4. `cmd/ox/web/src/protocol.ts`: `ChangedFile` gains
   `counts?: { added: number; removed: number }`.
5. `cmd/ox/web/src/diff.ts`: remove `fileCounts`; `statuses` returns
   `{ files, folders }`. Update `diff.test.mjs`.
6. `cmd/ox/web/src/Changes.tsx`: totals sum `file.counts`; remove `files` and
   `change`; replace `status` with `fileStatus` and `folderStatus`; make
   `BranchSummary` local; each `ChangesList` row shows `<Counts>` after its
   folder when `counts` is present; `FileCard` shows `<Counts>` when `counts` is
   present; `ChangesView`'s reveal effect as decided, its comment updated.
7. `cmd/ox/web/src/FileTree.tsx`: `FolderRow` reads `folderStatus`, `FileRow`
   `fileStatus`.
8. `cmd/ox/web/src/style.css`: the row's counts sit at its right end, as in
   `.branch-summary .counts`.
9. `cmd/ox/web/e2e/workspace.spec.ts`: the e2e cases above.
10. `docs/protocol.md`: as below.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`, `workspace/list_changes`: add a `counts` row to the file
  table: the lines the change adds and removes, as `added` and `removed`,
  counted over the whole file even when `reason` is present, an untracked file's
  lines all counted as added; omitted for a binary file and an untracked file
  that cannot be read. Add `counts` to the example. State that each path appears
  once.
- `docs/protocol.md`, `workspace/list_folder` and `workspace/list_changes`: on
  macOS, names and paths are reported in composed Unicode form, as Git reports
  them.
