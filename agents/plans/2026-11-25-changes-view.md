# Changes view

## Goal

The panel that shows the file tree gains a second tab, Changes. It lists each
file in the selected session's directory that differs from the commit where
the session's branch left the workspace's branch, counting commits,
uncommitted edits, and untracked files. Each file shows as a card with its
name, folder, lines added and removed, a button that opens it in the file
viewer, and its diff in collapsible sections with numbered, colored,
syntax-highlighted lines. A new `oxd` method, `workspace/list_changes`, runs
git and returns the files and their hunks; the page polls it on its clock
while the tab shows, as the file tree polls its folders.

## Related code

- `internal/server/methods.go` — `handle`, `fileParams`, `fileRoot`,
  `listFolder`, `readFile`: the file methods, each run in its own goroutine
  (`server.go` `go s.handle`) with disk work outside `Server.mu`.
- `internal/server/files.go` — `readFile` (1 MiB, NUL, and UTF-8 checks, inside
  an `os.Root`), `maxFileSize` and its errors.
- `internal/server/worktree.go` — `git(step, args...)` and `gitError`, the one
  way `oxd` runs git.
- `internal/server/session.go` — `session.directory` and `session.workspace`
  (the workspace's path).
- `internal/server/worktree_test.go` — `gitRepo`, `worktreeServer`,
  `worktreeSession`, `addWorkspace`, `runGit`.
- `internal/server/methods_test.go` — the file-method selector refusals loop.
- `cmd/ox/web/src/App.tsx` — mounts `FileTree` keyed by selection and passes
  `files()` (root, directory, control).
- `cmd/ox/web/src/FileTree.tsx` — the `aside.files` panel, its header (`h2`
  directory name and the toggles), and the tree; `FileRow` opens a file with
  `control.setFile(path)` and `onOpen()`.
- `cmd/ox/web/src/Panels.tsx` — `createPanels`, the page-wide layout state.
- `cmd/ox/web/src/poll.ts` — `createPoll`, one request in flight, kept across a
  lost connection.
- `cmd/ox/web/src/highlight.ts` — `highlight(path, text)`.
- `cmd/ox/web/src/ox.tsx` — `listFolder`, `readFile` RPC wrappers.
- `cmd/ox/web/src/protocol.ts` — `FileRoot`, the method map.
- `cmd/ox/web/src/style.css` — `aside.files`, the viewer's `pre` gutter.
- `cmd/ox/web/e2e/ui.ts` — `gitRepo`, `clocked`; `workspace.spec.ts` holds the
  file tree and viewer specs; `session.spec.ts` "Helper child" spec checks the
  panel's heading.

## Decisions

- **The base.** `oxd` resolves it on each request, in the request's directory
  `dir` (the session's `directory`, or the workspace's path for a draft):
  `git -C <workspace> rev-parse --verify HEAD`, then
  `git -C dir merge-base HEAD <that commit>`. For a worktree session this is
  its fork point, which stays put as the workspace's branch moves on. For a
  session in the workspace directory and for a draft, both are the same
  `HEAD`, so the view shows uncommitted edits and untracked files. A child
  session in its parent's worktree shares the parent's view.
- **The diff.** One run,
  `git --no-optional-locks -C dir diff --numstat -z -p --no-renames --no-color --no-ext-diff --no-textconv --unified=3 --relative --src-prefix=a/ --dst-prefix=b/ <base> --`.
  `--relative` limits it to `dir` and makes paths relative to `dir`, the same
  paths `workspace/read_file` takes. The NUL-terminated numstat records, ended
  by an empty record, give each file's path without quoting; the patch that
  follows has one section per record, in the same order, each starting with a
  line beginning `diff --git ` (no content line can, since content lines start
  with a space, `+`, `-`, or `\`). `--no-optional-locks` keeps git from
  rewriting the index, so *Ox's own files* holds. Renames show as a deleted
  file and an added file.
- **Untracked files.** `git --no-optional-locks -C dir ls-files -z --others
  --exclude-standard` lists them relative to `dir`. Each is read with the
  existing `readFile(dir, path)`, and its text becomes one hunk of added lines
  starting at line 1; an empty file has no hunks. A read error (`file is
  binary`, `file is larger than 1 MiB`, `file is not UTF-8 text`, `not a
  regular file`, or an `os.Root` refusal) becomes the file's `reason`.
- **A section's status and reason.** In a tracked file's section, a header line
  `new file mode` makes it `added`, `deleted file mode` makes it `deleted`,
  otherwise `modified`. A header line starting `Binary files ` gives `file is
  binary`; a section longer than `maxFileSize` gives `diff is larger than 1
  MiB`; a section that is not valid UTF-8 gives `file is not UTF-8 text`. Hunk
  lines starting `\` (no newline at end of file) are dropped.
- **Protocol shape.** `workspace/list_changes` takes exactly one of
  `session_id` or `workspace_id`, as the other file methods, and returns
  `{ "branch": "ox/abc", "files": [...] }`. `branch` is
  `git branch --show-current`, omitted when `HEAD` is detached. `files` is
  sorted by `path`. Each file is
  `{ "path", "status": "added"|"modified"|"deleted", "hunks": [...] }` or
  `{ "path", "status", "hunks": null, "reason" }`. Each hunk is
  `{ "old_start", "new_start", "lines": [" ctx", "-old", "+new"] }`, each line
  keeping git's one-character prefix. Counts, ranges, and line numbers are
  derived from `lines` on the page, so each has one source.
- **Errors.** Every git failure is a `*gitError` and returns `-32000` with it.
  A failure of the first command (`git -C dir branch --show-current`) returns
  `-32000` `not a git repository`. `merge-base` exiting 1 with no output returns
  `-32000` `no commit in common with the workspace's branch`. The selector
  errors are those of `workspace/list_folder`.
- **Panel tabs.** The panel's header replaces the `h2` directory name with a
  segmented control, `role="tablist"`, holding two `role="tab"` buttons, Files
  and Changes, with `aria-selected`; the toggles stay at its end. The session
  and draft headers already show the directory. The body is the selected
  tab's `role="tabpanel"`. The aside keeps `aria-label="Files"`.
  `createPanels` gains `tab` (`"files" | "changes"`, starting `"files"`) and
  `setTab`, page-wide until reload, like the panel widths.
- **The Changes tab.** A summary row: lucide `git-branch` and the branch, then
  the total `+N −M` (added green, removed red), summed over shown files. Then
  one card per file, `<article aria-label={path}>`: header with the base name,
  the folder beneath in muted text (omitted at the top level), the file's
  `+N −M`, and an icon button (lucide `square-arrow-out-up-right`, label
  `Open <name>`) that calls `control.setFile(path)` and `onOpen()`, as
  `FileRow` does. A `deleted` file has no Open button. A file with a `reason`
  shows it in place of its hunks and counts. Each hunk is a
  `<button aria-expanded>` header, `Lines a–b` (or `Line a`) with its own
  `+N −M`, over its lines; it starts expanded. `a–b` is the hunk's new-side
  range, or its old-side range when it adds no lines. An empty listing shows
  `No changes`; an error shows its message, as a folder's error does.
- **Diff lines.** Unified, one row per line in a `<pre>`: added lines in
  `<ins>`, removed lines in `<del>` (ARIA `insertion` and `deletion`), tinted
  green and red with the theme's tokens; context lines plain. One gutter
  number per row: the new line number for context and added lines, the old
  line number for removed ones, written as `data-line` and shown by
  `::before { content: attr(data-line) }`, so copying copies none of them.
  `Wrap` does not apply; rows scroll sideways with the gutter sticky, as in the
  viewer.
- **Highlighting.** `highlight.ts` exports `highlightLines(path, text):
  string[]`, the escaped HTML of each line, and `highlight` is rebuilt on it.
  Each hunk highlights its old side (context and removed lines) and its new
  side (context and added lines) as two texts and takes each row's HTML from
  its side. A hunk memoizes its joined `lines`, so a poll that returns the
  same lines highlights nothing again. Cards are keyed by `path` and hunks by
  `old_start` and `new_start`, so a hunk keeps its collapsed state across
  polls until its position changes.
- **Requirements.**
  - *Changes view*, *Change diffs*, *Changes updates* (added): one poll per
    mounted Changes tab via `createPoll`, on the page's clock, which stops while
    the page is hidden. A lost connection keeps the last answer; a reconnect
    resumes polling. Switching sessions remounts the panel (keyed by
    selection), so each session reads its own root and its hunks start
    expanded. A second page polls on its own. Parking or exit does not matter:
    the method reads only disk. A session deleted while shown unmounts the
    panel with its selection.
  - *Layout through navigation* (changed): the tab lives in `createPanels`,
    page-wide, reset by reload.
  - *File viewer*, *Open file*: Open uses `control.setFile`, so the open file
    follows the existing rules; on a narrow screen `onOpen` closes the drawer
    and the file replaces the transcript.
  - *Narrow screens*, *Panel toggles*, *Resizing panels*: the tabs live inside
    the existing panel and drawer, so these hold unchanged.
  - *File tree*: the tree is the Files tab's content, unchanged.
  - *Ox's own files*: `--no-optional-locks` on every git command, so a listing
    writes nothing in the repository.
- **Settling.** Each git command ends on its own; the page holds one request
  in flight per mounted tab. No new timer or bound.
- **Construction.** A file's `hunks` is `null` exactly when it has a `reason`;
  one Go function builds each file, and the TypeScript type is
  `{ path; status } & ({ hunks: Hunk[] } | { hunks: null; reason: string })`.
  Counts and ranges are derived from `lines`, never sent.
- **Ownership.** One new page state, `tab` in `createPanels`, owned by the
  page's layout, needed so the user's tab choice holds across sessions; no
  races, cleared by reload. Collapsed hunks are component-local signals,
  cleared on unmount. `oxd` stores nothing.
- **Trust boundaries.** The method is reached through the same connection as
  `workspace/read_file`. It runs git in the session's directory with the
  repository's own configuration, as Ox's worktree commands already do, and
  reads untracked files through `readFile`'s `os.Root`, so it reads nothing
  outside `dir`. External diff drivers and textconv are off.

## Check your assumptions

1. **New requirement text: Changes view, Change diffs, Changes updates.**
   - **Plan:** adds
     - **Changes view.** The panel that shows the file tree has a Files tab,
       the tree, and a Changes tab, which lists each file in the selected
       session's directory, or a new-session draft's workspace directory, that
       differs from where the session's branch left the workspace's branch,
       counting commits, uncommitted edits, and untracked files but not files
       Git ignores. The tab shows the branch and the lines added and removed,
       in total and for each file, and says why when the directory is not in a
       Git repository.
     - **Change diffs.** Each file in the Changes tab has a button that opens
       it in the file viewer, unless it was deleted, and shows its changed
       lines with the lines around them in sections that collapse and start
       expanded, numbered, colored by whether they were added or removed, and
       highlighted as the file viewer highlights them. A binary file, or one
       whose diff is larger than 1 MiB or not UTF-8 text, says so instead.
     - **Changes updates.** While the page is visible, the Changes tab shows
       changes on disk and new commits within about a second.
   - **Alternative:** reword any of them; they are what the review checks.
2. **A session in the workspace directory shows only uncommitted changes.**
   - **Plan:** a session that is not in a worktree, and a new-session draft,
     share the workspace's branch, so their fork point is their own latest
     commit, and Changes lists only uncommitted edits and untracked files.
   - **Alternative:** compare them against a named main branch instead, which
     needs a setting or a picker, and a base that differs between sessions on
     the same branch.
3. **Tab names, order, and default: Files, then Changes, Files shown first.**
   - **Plan:** the tabs read Files and Changes, in the panel's header where
     the directory name sits today (the session header already shows the
     directory). Files is selected at load; the choice holds for every session
     until reload. *Layout through navigation* changes from "Which panels show
     and their widths are the same for every session." to "Which panels show,
     which tab the file tree's panel shows, and their widths are the same for
     every session."
   - **Alternative:** ChatGPT's Modified and All Files, Modified first; or
     keep the choice per session, like the open file.
4. **Updates every second while the tab shows.**
   - **Plan:** Changes re-reads the diff on the page's one-second clock while
     the tab shows and the page is visible, as the file tree does, so Claude's
     edits appear as it makes them.
   - **Alternative:** refresh when a turn ends or on a button, which costs
     less on very large repositories but shows stale diffs mid-turn.
5. **Large and binary files show a note, not a diff.**
   - **Plan:** a binary file, one whose diff is over 1 MiB, or one that is not
     UTF-8 text, keeps its card and name with the reason in place of its lines
     and counts, matching the file viewer's limits.
   - **Alternative:** a "Show diff" button that loads it anyway, which can
     stall the page on a generated file.
6. **Renamed files show as a deletion and an addition.**
   - **Plan:** a moved file shows twice: its old path with every line removed,
     its new path with every line added.
   - **Alternative:** detect renames and show one card with both paths and only
     the edited lines, at the cost of a more complex listing.
7. **Narrow screens use the same drawer.**
   - **Plan:** on a phone, Changes shows in the file drawer at its width;
     tapping a file's Open button closes the drawer and shows the file in place
     of the transcript, as tapping a file in the tree does.
   - **Alternative:** show Changes in place of the transcript, like an open
     file, which gives diffs the full width but hides the transcript while
     reading them.

## Test plan

- **Server** — new `TestListChanges` in new `internal/server/changes_test.go`,
  with `gitRepo` and a worktree session on a workspace at `repo/sub`. In the
  worktree it commits an edit to `sub/file.txt` and a new `sub/gone.txt`, then
  removes `sub/gone.txt` in a second commit, edits `sub/file.txt` again
  uncommitted, adds an untracked text file, an untracked binary file, a tracked
  binary change, a `*.log` file (ignored), and an edit outside `sub`. The
  workspace's `main` gains a commit after the fork. Asserts: `branch` is the
  session's; files sorted by path, relative to `sub`; `file.txt` `modified`
  with hunks carrying both the committed and uncommitted edits and correct
  `old_start`/`new_start`; `gone.txt` absent (added then deleted since the
  base); the untracked text file `added` with one hunk from line 1; both binary
  files with `reason` `file is binary` and `hunks` null; no `*.log`, no file
  outside `sub`, nothing from `main`'s later commit; `.git/index`'s
  modification time unchanged after the call with a stat-dirty file present.
  A second case: the workspace root (`workspace_id`) lists only uncommitted
  and untracked files; a third: a workspace in a non-Git directory returns
  `-32000` `not a git repository`; a fourth: a diff section over 1 MiB returns
  `diff is larger than 1 MiB` and a deleted tracked file returns `deleted`.
- **Server** — `methods_test.go` selector loop: add `workspace/list_changes` to
  the two `exactly one of session_id and workspace_id is required` refusals and
  `session not found`.
- **Node** — `highlight.test.mjs`: `highlightLines` returns one entry per line,
  none for the piece after a final newline, and the same HTML `highlight`
  wraps.
- **e2e** — `workspace.spec.ts`, each with `gitRepo(services.workspace)` and an
  edit to `committed.txt`, on a draft's panel:
  - "the Changes tab lists each changed file with its counts and diff":
    selecting the Changes tab shows articles `committed.txt` and
    `untracked.txt` with their counts, the total, `Lines 1–1`, and the
    `deletion` and `insertion` rows.
  - "a section's header collapses and expands its lines" via `aria-expanded`.
  - "a changed file's Open button opens it in the file viewer".
  - "the Changes tab holds through switching sessions and resets on reload".
  - "a directory outside a Git repository says why in the Changes tab".
  - `clocked` "the Changes tab shows a new edit on the next tick".
- **e2e** — narrow `describe` in `workspace.spec.ts`: "a changed file's Open
  button closes the drawer and replaces the transcript".
- **e2e** — `session.spec.ts` "Helper child" spec: replace the panel heading
  assertion with the tree's `child.txt` row it already checks (drop the
  `heading` line).

## Implementation plan

1. `internal/server/changes.go` (created): types `changes`, `changedFile`,
   `hunk`; `listChanges(workspace, dir string) (changes, error)` running the
   git commands above through `git(...)`, parsing numstat records and patch
   sections, adding untracked files via `readFile`, sorting by path. Parse
   hunk headers `@@ -a[,b] +c[,d] @@`.
2. `internal/server/methods.go` (modified): `fileRoot` returns the directory
   and the workspace's path; `listFolder` and `readFile` ignore the latter. Add
   `case "workspace/list_changes"` and `listChanges` handler (selector checks
   as `listFolder`, git outside `s.mu`).
3. `internal/server/changes_test.go` (created), `methods_test.go` (modified):
   the server tests.
4. `cmd/ox/web/src/protocol.ts` (modified): `Hunk`, `ChangedFile`, `Changes`,
   and `"workspace/list_changes": [FileRoot, Changes]`.
5. `cmd/ox/web/src/ox.tsx` (modified): `listChanges(root)`.
6. `cmd/ox/web/src/highlight.ts` (modified): `highlightLines`;
   `highlight.test.mjs` (modified).
7. `cmd/ox/web/src/Panels.tsx` (modified): `tab`, `setTab`.
8. `cmd/ox/web/src/Changes.tsx` (created): `Changes(props: { root, control,
   onOpen })` with the summary row, `FileCard`, `Hunk`.
9. `cmd/ox/web/src/FileTree.tsx` (modified): header holds the tablist and the
   toggles; body shows the tree or `Changes` by `tab`. Props gain `tab` and
   `onTab`; `directory` goes.
10. `cmd/ox/web/src/App.tsx` (modified): pass `panels.tab()` and
    `panels.setTab`; drop `directory` from the panel's props.
11. `cmd/ox/web/src/style.css` (modified): segmented control, cards, hunk
    headers, `ins`/`del` rows and their `data-line` gutter, `+N`/`−M` colors,
    `.git-branch` and `.square-arrow-out-up-right` icons; touch sizes for the
    new buttons under the existing touch rule.
12. `cmd/ox/web/e2e/workspace.spec.ts`, `session.spec.ts` (modified): the
    specs.
13. Docs below.

## Requirement changes

- **Added:** **Changes view.** The panel that shows the file tree has a Files
  tab, the tree, and a Changes tab, which lists each file in the selected
  session's directory, or a new-session draft's workspace directory, that
  differs from where the session's branch left the workspace's branch,
  counting commits, uncommitted edits, and untracked files but not files Git
  ignores. The tab shows the branch and the lines added and removed, in total
  and for each file, and says why when the directory is not in a Git
  repository.
- **Added:** **Change diffs.** Each file in the Changes tab has a button that
  opens it in the file viewer, unless it was deleted, and shows its changed
  lines with the lines around them in sections that collapse and start
  expanded, numbered, colored by whether they were added or removed, and
  highlighted as the file viewer highlights them. A binary file, or one whose
  diff is larger than 1 MiB or not UTF-8 text, says so instead.
- **Added:** **Changes updates.** While the page is visible, the Changes tab
  shows changes on disk and new commits within about a second.
- **Changed:** **Layout through navigation.** Old: "Which panels show and their
  widths are the same for every session." New: "Which panels show, which tab
  the file tree's panel shows, and their widths are the same for every
  session." The rest of the requirement is unchanged.

## Documentation updates

- `docs/protocol.md`: a `workspace/list_changes` section after
  `workspace/read_file`: params, base, result shape, `reason` values, errors,
  example.
- `docs/architecture.md` ("File tree and viewer"): a **Changes** bullet: polls
  `workspace/list_changes` on the same clock while its tab shows, keyed by
  selection.
