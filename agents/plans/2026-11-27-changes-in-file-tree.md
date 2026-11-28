# Merge the Changes tab into the file tree

Web client only. `oxd`, the protocol, and persistence do not change: `workspace/list_changes` already returns the branch, each changed file's status, counts, and path, deleted files included.

## Goal

The right panel has Files and Changes tabs, and the user switches between them to see what changed and then open a file. The panel shows the file tree alone, and the tree carries the change information. Resolves [#197](https://github.com/kkestell/ox/issues/197).

User decisions: folder rows show no counts (they keep their change color); a deleted file is a row in its folder, colored deleted, with its counts, and clicking it opens the Changes view at that file; each deleted folder above a deleted file is a folder row in its parent, colored deleted, and expanding it lists the deleted files and folders it held; outside a Git repository the top of the tree shows nothing and the Changes view is unreachable.

## Related code

- `cmd/ox/web/src/Workspace.tsx` — renders the panel header's Files/Changes toggle, the `rightMode` switch, `ChangesList`, and `openChanges`.
- `cmd/ox/web/src/Panels.tsx` — `RightMode`, `rightMode`, `setRightMode`.
- `cmd/ox/web/src/Changes.tsx` — `createListing` (polled changes, totals, statuses), `BranchSummary`, `ChangesList`, `Counts`, `ChangesView`.
- `cmd/ox/web/src/FileTree.tsx` — `Folder`, `FolderRow`, `FileRow`; already receives `listing`.
- `cmd/ox/web/src/diff.ts` — `statuses` (file and folder status maps).
- `cmd/ox/web/diff.test.mjs` — unit tests for `diff.ts`.
- `cmd/ox/web/src/poll.ts` — `createPoll`, which `Folder` uses to list a folder on each tick.
- `cmd/ox/web/src/ox.tsx` — `isExpanded`/`setExpanded`, the per-session expansion store keyed by path.
- `cmd/ox/web/src/style.css` — "Changes.tsx" section (`.row.summary`, `.change-row`, `.branch-summary`) and the `aside.files` rows.
- `cmd/ox/web/e2e/workspace.spec.ts` — helpers `changesList`, `panelMode`, `showPanel`, `showChanges`, and the tests that use them.
- `docs/architecture.md` — "UI components", "Workspace", "Changes" entries.

## Decisions

- **Requirements:** File tree, Changes view, Changes updates, Tabs, Panel toggles, Resizing panels, Layout through navigation, Narrow screens, File tree updates (unchanged), File viewer (unchanged). All state is `listing`, which Workspace already owns and polls on the page's clock; the tree reads it, so counts, colors, and deleted rows update on the next tick with no new state. No request is added, so nothing new is in flight, lost, or racing. Opening the Changes view uses the existing `reveal` layout edit, so it survives reconnects and switching as it does today.
- **Construction:** `RightMode`, `rightMode`, and `setRightMode` are deleted; the panel has one view, so the "tab chosen" state cannot exist. The tree's top rows are derived from `listing.changes()` alone: present exactly when the listing holds a value. An error (non-repository or otherwise) renders nothing, so no branch row or counts exist without a listing. Only `Folder` creates a `listFolder` poll, and only an entry the disk listed renders `Folder`, so a deleted folder cannot poll the disk or show a read error.
- **Tree entries:** one rule gives every folder's entries: the folder's disk listing merged with the deleted children the changes listing records for it. Existence comes from the disk alone (the parent's folder listing), and deleted contents from the changes listing alone; neither copies the other.
  - `diff.ts` gains `deletedEntries(changed: ChangedFile[]): Map<string, FolderEntry[]>`. For each file with status `deleted` it adds, for every folder on its path including `""`, the next segment as an entry: a folder entry for each intermediate segment and a file entry for the last, each folder listing a name once per kind. `createListing` memoizes it from `changes()` and exposes `deletedIn(folder: string): FolderEntry[]` (empty when the folder has none or the listing holds no value).
  - `diff.ts` gains `treeEntries(disk: FolderEntry[], deleted: FolderEntry[]): TreeEntry[]`, where `TreeEntry = FolderEntry & { onDisk: boolean }`: every disk entry with `onDisk: true`, then each deleted entry whose name the disk does not list with `onDisk: false`, sorted folders first by name (the `byFolderThenName` comparator moves here from `FileTree.tsx`). The disk wins a name both list, so a restored file or folder shows as its disk entry even while the changes listing still calls it deleted, and a file now standing where a deleted folder was hides that folder (its files stay in the Changes view).
  - `FileTree.tsx`: `Entries(props: { tree; path; depth; disk?: () => Polled<{ entries: FolderEntry[] }> | undefined })` is the one place that calls `treeEntries` and renders the list, the "Empty" note, and a read error. `Folder` (an on-disk folder, and the root) creates the `listFolder` poll and renders `Entries` with it; it shows entries once the poll has a value, as now. A folder entry with `onDisk: false` renders `Entries` with no `disk`, so it has no poll, sends no `workspace/list_folder`, and has no error to show; its entries are `treeEntries([], deletedIn(path))`, which holds only deleted entries, since nothing beneath a missing folder is on disk. `Polled` is exported from `poll.ts`.
  - `FolderRow` takes `onDisk` and, when expanded, renders `Folder` or the poll-free `Entries`. A file entry renders `FileRow` when `onDisk`, `DeletedRow` otherwise. A deleted folder row looks like any folder row: chevron, folder icon, `data-status` from `folderStatus`, which is `deleted` because every changed file beneath a missing folder is deleted.
  - The `<For>` keys each row by `onDisk` and name (`${entry.onDisk ? "disk" : "deleted"}/${entry.name}`), so a row whose entry changes between disk and deleted remounts as the other kind. Expansion lives in `control().isExpanded(path)`, keyed by path, so the remounted folder keeps its expanded state.
  - **Reappearing while expanded:** a deleted folder that returns on disk (restoring any file beneath it recreates it) appears in its parent's next disk listing, remounts as an on-disk folder, stays expanded, starts polling, and lists its disk entries plus the deleted entries still recorded beneath it. A restored deleted file appears in its folder's next disk listing and remounts as a `FileRow` that opens the file in the viewer.
  - **Deleted while expanded:** an on-disk folder that is deleted drops from its parent's disk listing and, once the changes listing records its files as deleted, remounts as a deleted folder, stays expanded, and stops polling.
  - The disk listings and the changes listing are independent polls on the page's clock, answered in any order. For about a second after a change they can disagree: a row can drop out and return, a folder can show "Empty", or a folder deleted while expanded can show its read error until its parent's listing answers, as it does today. Each outcome is valid for some moment and the next answers converge, so the plan accepts the window rather than coordinating the polls.
- **Rows:** A changed file row with counts is an `<li>` holding the row button (opens the file, draggable, as now) and a sibling counts button (`aria-label` "Changes in <name>") at the row's right edge; buttons are not nested. A changed file without counts (binary or too large to count) shows no counts button. A deleted row is one button holding its name and counts that opens the Changes view at the file; it is not draggable and has no file icon change beyond the existing one.
- **Top row:** A button first in the tree, showing the branch icon, branch name (omitted when detached), and total counts; it calls `openChanges()`. It replaces `BranchSummary`'s error branch. `ChangesList` and `.change-row` styles are deleted.
- **Panel header:** keeps a "Files" title and `PanelToggle`; the `fieldset` toggle and `ModeButton` go. The aside's `aria-label` is always "Files".
- **Narrow screens:** opening the Changes view or a file from the tree dismisses the drawer, as `openChanges` and `openFile` do now.
- **Ownership:** no new state, timer, retry, lock, or stored field.
- **Claude Code, Settings, Settling, Process I/O, Trust boundaries:** not touched.

## Check your assumptions

None. The user settled folder counts, deleted files and folders, and the non-repository case. Remaining behaviors follow the issue's proposal.

## Test plan

- Panel has no mode toggle; the tree shows the branch row with totals and each changed file's counts (e2e, replaces "the panel's Changes list shows…" and "the panel lists the files or the changes…").
- Branch row opens the Changes view (e2e: `showChanges` helper clicks the tree's top row).
- Counts button opens the Changes view at that file; clicking the name opens the file (e2e, adapts "a file in the Changes list opens the Changes tab at its card" and the reveal-scroll test that uses `changesList`).
- A deleted file is a row in its folder with counts, colored by existing status styling, and clicking it opens the Changes view at its card (new e2e; no existing test has a deleted tracked file in a tree folder; "the pane shows changes on disk…" at line 1627 can host the setup if it has a git repo, otherwise a new test with `gitRepo`).
- A whole deleted folder `gone/deep/c.txt` (tracked, then removed with its folders) shows `gone` as a folder row colored deleted; expanding `gone` and `deep` lists `c.txt` as a deleted row, which opens the Changes view at its card; the page sends no `workspace/list_folder` for `gone` or `gone/deep` (new e2e, recording the page's `/rpc` posts) and shows no read error. Restoring the folder with `git checkout` while expanded turns `gone` into an on-disk folder that stays expanded and `c.txt` into a file row that opens the viewer (same test).
- `deletedEntries` lists intermediate folders and the file at each level, a name once per folder, and only deleted files; `treeEntries` marks disk and deleted entries, lets the disk win a shared name, and sorts folders first (new cases in `diff.test.mjs`).
- Outside a Git repository the tree has no branch row or counts (e2e, replaces "the Changes list says why outside a Git repository" and "a directory outside a Git repository says why in the Changes tab").
- Folder rows show no counts (asserted in the aria snapshot of the tree).
- `statuses` and its test are unchanged. `make test` (Biome, `style.test.mjs`) and `make e2e` run. Layout of the right-hand column is judged by the user; no CSS assertions.

## Implementation plan

1. `Panels.tsx`: delete `RightMode`, `rightMode`, `setRightMode`; update the `createPanels` comment.
2. `diff.ts`: add `deletedEntries`, `TreeEntry`, `treeEntries`, and move `byFolderThenName` here; add their cases to `diff.test.mjs`. `poll.ts`: export `Polled`.
3. `Changes.tsx`: add `deletedIn` to `createListing`; delete `BranchSummary` and `ChangesList`; keep `Counts`, `ChangesView`, `FileCard`. Export a `BranchSummary`-free top row from `FileTree` instead.
4. `FileTree.tsx`: add the `onOpenChanges(path?: string)` prop and `Tree` field; render the top row when `listing.changes()` holds a value; add `Entries` and make `Folder` the polling wrapper around it; give `FolderRow` `onDisk`; key rows by kind and name; add the counts button to `FileRow`; add `DeletedRow`. Update the component comment.
5. `Workspace.tsx`: drop the mode switch, `ModeButton`, and `ChangesList`; pass `openChanges` to `FileTree`; fix the component comment; aside label "Files".
6. `style.css`: replace the "Changes list" block with rules for the top row, the counts column, and the deleted row, using existing tokens; delete `.change-row` and the branch error rule.
7. `e2e/workspace.spec.ts`: remove `changesList`, `panelMode`, and the `mode` parameter of `showPanel`; make `showChanges` click the tree's top row; rewrite the tests listed above.
8. `docs/architecture.md`: update the three entries named below.

## Requirement changes

- **Changed: File tree.**
  Old: "The panel's Files tab shows the directory the selected session works in, or a new-session draft's workspace directory, as a tree, folders first, whose folders expand and collapse. It hides version-control folders and operating-system metadata files at any depth, such as `.git` and `.DS_Store`, shows files Git ignores, lists nothing outside that directory, and shows why a folder cannot be read. Each changed file, and each folder above one, is colored by its change: modified, added, or deleted, and modified for a folder whose files changed in different ways."
  New: "The Files panel shows the directory the selected session works in, or a new-session draft's workspace directory, as a tree, folders first, whose folders expand and collapse. It hides version-control folders and operating-system metadata files at any depth, such as `.git` and `.DS_Store`, shows files Git ignores, lists nothing outside that directory, and shows why a folder cannot be read. Each changed file, and each folder above one, is colored by its change: modified, added, or deleted, and modified for a folder whose files changed in different ways. A deleted file shows as a row in its folder, and each deleted folder above one shows as a folder whose rows are the deleted files and folders it held. A changed file's row shows its lines added and removed at its right."
- **Changed: File viewer.**
  Old: "Clicking a file in the file tree opens it as a tab, and marks its row. …"
  New: "Clicking a file in the file tree, anywhere on its row but its counts, opens it as a tab, and marks its row. …" (rest unchanged).
- **Changed: Changes view.**
  Old: "The panel's Changes tab lists each file in the selected session's directory, or a draft's workspace directory, that differs from where the session's branch left the workspace's branch (counting commits, uncommitted edits, and untracked files but not files Git ignores), with its folder and lines added and removed, and the branch and totals at its top. Clicking a file, or the top row, opens the Changes tab; the Changes tab shows every file's diff, and a click on a file scrolls to it. It says why when the directory is not in a Git repository."
  New: "The top of the file tree shows the branch and the total lines added and removed across every file in the selected session's directory, or a draft's workspace directory, that differs from where the session's branch left the workspace's branch (counting commits, uncommitted edits, and untracked files but not files Git ignores). Clicking it opens the Changes tab, which shows every file's diff. Clicking a changed file's lines added and removed in the tree, or a deleted file's row, opens the Changes tab scrolled to that file. When the directory is not in a Git repository the top of the tree shows nothing and the Changes tab cannot be opened."
- **Changed: Change diffs.** "Each file in the Changes tab" is unchanged in meaning; no edit.
- **Changed: Changes updates.**
  Old: "While the page is visible, the Changes list and tab, and the file tree's colors show changes on disk and new commits within about a second."
  New: "While the page is visible, the Changes tab, and the file tree's colors, counts, and deleted files and folders show changes on disk and new commits within about a second."
- **Changed: Panel toggles.** "the Files | Changes panel" becomes "the Files panel".
- **Changed: Tabs.** Old: "the Changes view, opened from the Changes list, with the total lines added and removed on its tab; … Clicking a file in the Files tab opens it …" New: "the Changes view, opened from the top of the file tree or a changed file's counts, with the total lines added and removed on its tab; … Clicking a file in the file tree opens it …".
- **Changed: Splitting tab groups.** "or a file from the Files tab" becomes "or a file from the file tree".
- **Changed: Resizing panels.** "the Files | Changes panel" becomes "the Files panel".
- **Changed: Layout through navigation.** Old: "Whether the session list and the Files | Changes panel show, the panel's tab, and the side panels' widths are the same for every session; …" New: "Whether the session list and the Files panel show, and the side panels' widths, are the same for every session; …".
- **Changed: Narrow screens.** "the Files | Changes panel" becomes "the Files panel".

## Documentation updates

- `docs/architecture.md`: "UI components" drops "the panel's mode (files or changes)"; "Workspace" says the right panel lists the file tree; "Changes" says the poll feeds the tree's top row, counts, colors and deleted rows, the Changes tab's counts, and the Changes view; "File tree" notes that each folder's entries are its disk listing merged with the deleted files and folders the changes listing records beneath it, and that a deleted folder is never listed from disk.
