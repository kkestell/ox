# Review: Merge the Changes tab into the file tree

## Scope and coverage

Uncommitted changes in the `changes-in-file-tree` worktree on base `56086e8` (`origin/main`), checked against `agents/plans/2026-11-27-changes-in-file-tree.md` for #197. The change is web client only, so this session reviewed it alone, with no concern reviewers.

The requirements diff in `docs/functional-requirements.md` matches the plan's Requirement changes word for word: File tree, File viewer, Changes view, Changes updates, Panel toggles, Tabs, Splitting tab groups, Resizing panels, Layout through navigation, Narrow screens. No other requirement changed, and no requirement still names the Files | Changes panel, the Files tab, or the Changes list.

The implementer's flagged choices hold:

- `fileCounts(path)` on `createListing` derives from the same `changes()` as the statuses and the deleted entries, so each fact has one source.
- The `<h2>Files</h2>` header follows VS Code's side bar title and names the panel the aria snapshot finds.
- A changed file's `li.changed` holds the row button and a sibling counts button, so buttons are not nested. The flex row stretches the counts button to the row's height, including the touch height.
- The non-Git test waits for a second `workspace/list_changes` request. The poll keeps one request in flight, so the first answer has been applied when the second is sent, and the deep-equal snapshot then shows the absent top row. The existing `answered` helper filters by `path`, which `workspace/list_changes` does not carry, so the inline poll is the right fit.

Gaps:

- How the header, top row, counts column, and deleted rows look is for the user to judge.

## Summary

| ID | Severity | Category      | Action   | Status   | Title                                                      |
| -- | -------- | ------------- | -------- | -------- | ---------------------------------------------------------- |
| F1 | Low      | Simplicity    | Fix      | Resolved | Row key's disk/deleted prefix is redundant and unprotected |
| F2 | Low      | Documentation | Fix      | Resolved | Architecture's `diff.ts` row omits the tree entries        |
| F3 | Low      | Requirements  | Rejected | —        | A deleted tracked `.DS_Store` shows as a tree row          |

## F1. Row key's disk/deleted prefix is redundant and unprotected

Low · Simplicity · Fix · Resolved · `cmd/ox/web/src/FileTree.tsx:104`

`Entries` keys each row by `${onDisk ? "disk" : "deleted"}/${name}` so that a row whose entry turns between disk and deleted remounts as the other kind. The remount does not need the key. `treeEntries` gives each name once per folder, and the row's `Switch` on `entry().onDisk` and `FolderRow`'s `Show when={props.onDisk}` are reactive, so a row keyed by name alone swaps between `FileRow` and `DeletedRow`, and between `Folder` and the poll-free `Entries`, when its entry changes. Expansion is keyed by path either way.

### Evidence

With the key changed to `entry.name`, "a deleted folder lists its deleted files without reading the disk, and returns as a folder on disk" still passes: `gone` and `deep` turn into on-disk folders, stay expanded, and `c.txt` opens in the viewer.

### Action

Key rows by `entry.name`. The existing deleted-folder e2e test already covers the switch between kinds and needs no change.

## F2. Architecture's `diff.ts` row omits the tree entries

Low · Documentation · Fix · Resolved · `docs/architecture.md:526`

The Client modules table says `diff.ts` owns "Diff line counts, and changed files' and folders' statuses." It now also owns `deletedEntries` and `treeEntries`: each folder's deleted entries, and the merge of a folder's disk and deleted entries in the tree's order.

### Evidence

`cmd/ox/web/src/diff.ts:486-525`.

### Action

Extend the row to name each folder's deleted files and folders and the file tree's entries, and run `make docs`.

## F3. A deleted tracked `.DS_Store` shows as a tree row

Low · Requirements · Rejected · `cmd/ox/web/src/diff.ts:489`

File tree says the tree hides operating-system metadata files at any depth. `deletedEntries` lists every deleted path, so a `.DS_Store` that was committed and then removed shows as a deleted row, while `oxd`'s `hidden` rule in `internal/server/files.go` keeps the disk listing free of it.

### Action

Rejected: the row shows a deletion the user made and that the Changes view lists anyway, which no user would report as broken. Hiding it would copy `oxd`'s hidden-name rule into the page, a second place for one rule, which costs more than the row.

## Checks run

- `make test`: passed (Biome, Go with the race detector, the Node suites including the new `diff.test.mjs` cases and `style.test.mjs`).
- `make e2e`: 243 passed, 4 skipped.
- Removal: `Entries` merging no deleted entries for an on-disk folder fails "a deleted file is a row in its folder…" and "a deleted folder lists its deleted files…".
- Removal: the row key without its disk/deleted prefix passes every test (F1).
- Removal: rendering the top row on a listing error does not type-check, since the row reads the branch from the listing's value; the non-Git test's deep-equal snapshot covers any other row there.

## Verification 1

Uncommitted changes on base `56086e8` after `/ox-work fix`. The only changes since the review are the row key in `FileTree.tsx` and the `diff.ts` row in `docs/architecture.md`; no comment still describes a remount by key. `make docs` leaves the documents unchanged. `make test` passed, and `make e2e` passed 243 with 4 skipped.

| ID | Status   | Evidence                                                                                                                                                              |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | Rows are keyed by `entry.name` (`FileTree.tsx:104`); "a deleted folder lists its deleted files without reading the disk, and returns as a folder on disk" still passes. |
| F2 | Resolved | The `diff.ts` row names each folder's deleted files and folders and the file tree's entries (`architecture.md:526`).                                                   |

No new findings.

## Verdict

Ready to commit. F1 and F2 are resolved; F3 is rejected.
