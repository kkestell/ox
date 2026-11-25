# Land the workbench redesign of the file panel

## Goal

The file panel's redesign is implemented, uncommitted, under `cmd/ox/web/src/`:
Files and Changes tabs in the session's header open the workbench beside the
transcript, a file opens between the transcript and the tree, and a file view
shows a changed file's diff in place. This change makes it ready to land. It
fixes the defects confirmed in the implementation, replaces the specs of the old
panel with specs of the new one, adds Node tests for the pure diff logic,
regenerates the README screenshots, and updates the requirements and docs. The
design is the user's and stays as it is, apart from the defect fixes below.

## Related code

- `cmd/ox/web/src/Panels.tsx`: `createPanels` holds the session list flag, the
  workbench's mode (`wideMode` and `narrowMode`), widths, and the narrow
  drawer. Also `PanelToggles`, `SessionsToggle`, `ModeTabs`, and `Sash`, which
  measures its previous and next sibling elements.
- `cmd/ox/web/src/Workbench.tsx` (new): lays out the file view, the tree, or the
  changes. It owns the per-selection changes listing.
- `cmd/ox/web/src/Changes.tsx`: `createListing` polls `workspace/list_changes`
  and derives totals, files by path, and folder statuses. Also `BranchSummary`,
  `ChangesView`, and `FileCard`.
- `cmd/ox/web/src/FileView.tsx`: the Diff, Source, and Preview modes and how the
  default is chosen. Its content lost `role="tabpanel"`.
- `cmd/ox/web/src/diff.ts` (new): `counts`, `fileCounts`, `rowsHtml`,
  `wholeFile`. It imports `./highlight` without an extension, so Node cannot load
  it.
- `cmd/ox/web/src/FileTree.tsx`: tree rows carry `data-status`. The header shows
  `BranchSummary` between `start` and `end`.
- `cmd/ox/web/src/ox.tsx`: `OpenFile = { path; mode? }`, plus `openFile`,
  `setMode`, and `closeFile` on the session control.
- `cmd/ox/web/src/App.tsx`: `Layout`, with `BareHeader` and the `.panes` split
  (`--split`).
- `cmd/ox/web/src/Sidebar.tsx`: the header `toggle` slot and the Add workspace
  footer.
- `cmd/ox/web/src/style.css`: `.workbench`, `.viewer`, `.segmented`,
  `.branch-summary`, `[data-status]`, and the narrow rules.
- `cmd/ox/web/e2e/workspace.spec.ts`, `session.spec.ts`, `hero.spec.ts`: specs
  that drive the old panel through `Toggle files`, the panel's own Files and
  Changes tabs, `Toggle preview`, and `tabpanel "Changes"`.
- `cmd/ox/web/highlight.test.mjs`, `Makefile` `test`: the Node suites, which
  import `src/*.ts` directly.

## Decisions

- **Defect D1: the split counts the tree as part of the file's share**
  (confirmed live). The transcript and the file view should split the width
  beside the tree. Instead, `.workbench` wraps the file view and the tree as one
  flex item, and its minimum covers both. At 1600 px, dragging "Resize file
  viewer" all the way right leaves the file view 1 px wide. At 960 px with the
  default widths, the even split gives the transcript 336 px and the file view
  96 px. The fix:
  - `Workbench` renders its parts as direct children of `.panes`, with no
    wrapping `div`: the split sash, then the file view or changes view, then
    "Resize files" and the tree. The split sash's siblings are then the
    transcript and the view, so `Sash` measures the right columns and
    `setSplit((before + dx) / (before + after))` stays correct.
  - The tree's width has one source. `Layout` sets `--tree` on `.panes` from
    `panels.rightWidth()`, and the wide tree takes
    `width: clamp(160px, var(--tree), 640px)` from CSS. `FileTree` loses its
    `width` prop.
  - While a view shares the width, the transcript and the view each have
    `flex-grow` from `--split` and the minimum
    `min(240px, calc((100cqi - T) / 2))`. T is the clamped tree width when the
    tree is a sibling, and 0 otherwise. With no view beside it, the transcript
    keeps 240 px.
  - The narrow rule that hides the transcript keys on a following `.viewer` or
    a non-drawer `aside.files` in place of `.workbench`. The `alone` class goes.
- **Defect D2: the tabs select with nothing selected** (confirmed live). With
  no session or draft selected, clicking Files marks it selected and shows
  nothing. `PanelToggles` takes `workbench: boolean` and shows `ModeTabs` only
  when it is true. `Layout` passes `files() !== undefined`, the same fact that
  decides whether a workbench can render. The loading header keeps the tabs,
  because a session is selected.
- **Defect D3: the default mode is chosen before the changes are listed**
  (confirmed live). On returning to a session whose open file has changes, the
  file view shows Source, or Preview for Markdown, until `list_changes`
  answers, then switches to Diff. `Listing.file(path)` becomes
  `Listing.change(path): { file?: ChangedFile } | undefined`, which is
  `undefined` until the listing's first answer, value or error. `FileView`'s
  `change` prop takes that value. While it is `undefined`, `mode()` is
  `undefined`: the pane renders empty, as it does while the file is read, and
  there is no mode control or wrap button.
- **Defect D4: Diff is labeled "Changes".** The design names the mode Diff, and
  the label also clashes with the Changes tab. The `modes` entry for `diff`
  becomes `label: "Diff"`.
- **Defect D5: the workbench's toggle is labeled "Toggle files" in Changes
  mode.** `Workbench`'s `Close` becomes "Toggle workbench", after "Toggle
  sessions".
- **Defect D6: the file tab has no panel.** The `tablist "Open files"` tab lost
  the `tabpanel` it selects. Each `FileView` content element, the error `p`,
  the preview `div`, and both `pre`s, gets `role="tabpanel"` back, with no
  visible change. Specs find the pane by it.
- **`diff.ts` holds pure logic only.** `rowsHtml` moves to `highlight.ts`
  beside `highlightLines`, since it renders highlighted rows. `diff.ts` keeps
  `counts`, `fileCounts`, and `wholeFile`, and gains
  `statuses(files: ChangedFile[]): Map<string, ChangedFile["status"]>`, moved
  out of `createListing`. Its only import is then `import type`, so
  `diff.test.mjs` loads it in Node. Specs cannot observe the status colors
  (`cmd/ox/web/AGENTS.md` rules out style assertions), so the Node test covers
  the status rule.
- **Requirements.** The redesign changes Panel toggles, Resizing panels, Layout
  through navigation, Narrow screens, File tree, File viewer, Markdown preview,
  Changes view, Change diffs, and Changes updates (see Requirement changes). It
  leaves Word wrap, Open file, File viewer updates, Syntax highlighting, Line
  numbers, File tree updates, and File tree expansion as they are. The only
  `oxd` traffic that changes is `workspace/list_changes`. It now polls whenever
  the workbench is open, in either mode, once per selection, through
  `createPoll`. A lost connection keeps the last listing, an error replaces it
  until the next answer, and a reconnect resumes polling. Everything else is
  page-local layout or session-control state, so a second page and `oxd`
  restarts do not touch it.
- **Construction.** `panels.mode()` is the single source of whether the
  workbench shows and in which mode. The tabs and the toggle derive from it.
  `OpenFile.mode` is absent until the user chooses one, and `FileView` derives
  the shown mode from it, the change, and the file name, so no stored default
  goes stale. The tree width has one source (`--tree`). `Listing.change`
  separates "not yet listed" from "listed, unchanged" in its type.
- **Ownership.**
  - `createPanels`' `wideMode` and `narrowMode` replace `right` and `tab`. They
    are needed because Files/Changes is one page-wide choice, and crossing the
    breakpoint closes only the narrow one. They last for the page's life, with
    no races.
  - The session control's `file` gains `mode`. It is needed because the chosen
    mode lasts while the file stays open in its session. It is cleared by
    `closeFile` or by opening another file, and goes with the control.
  - `Workbench`'s listing serves the tree's colors, the branch summary, the
    file's diff, and the changes view. It lives while the workbench is mounted
    for one selection.
  - No new timers, retries, or stored fields.

## Check your assumptions

Each entry is a requirement the redesign changes. The proposed text describes
the design as approved.

1. **Panel toggles: the requirement now names the Files and Changes tabs.**
   - **Plan:** "A button shows and hides the session list, showing whether it
     is shown: in the list's header while the list shows, and in the session's
     header otherwise. Files and Changes tabs in the session's header open the
     workbench beside the transcript on the files or the changes, showing which
     one it shows, with neither selected while it is closed. A button at the
     start of the workbench's header closes it."
   - **Alternative:** Keep "Two buttons show and hide the session list and the
     file tree, each showing whether its panel is shown." That text no longer
     describes the page.
2. **Narrow screens: the workbench replaces the transcript, and the tree
   becomes a drawer only over an open file.**
   - **Plan:** "On a narrow screen, such as a phone's, the page shows one pane
     at a time: the transcript, or the workbench in its place. The Files and
     Changes tabs in the session's header open the workbench, which repeats
     them in its own header, and its header's button returns to the
     transcript. With a file open, the file tree opens over the file from a
     button in the file's header; with none, the tree fills the workbench. The
     session list opens over the transcript from a button in the session's
     header. Either one closes when the user taps outside it, picks a session
     or a new-session draft, or opens a file, and panels cannot be resized. At
     load, the session list is open when the URL names no session, and closed
     otherwise. Switching between a narrow and a wide screen closes the narrow
     screen's workbench and leaves the wide screen's panels as they were."
   - **Alternative:** Drop the last sentence and leave the breakpoint
     behavior out of the requirements. It would then be untracked.
3. **File view modes: Markdown preview is replaced by a requirement covering
   Diff, Source, and Preview.**
   - **Plan:** Remove "Markdown preview" ("A Markdown file opens rendered, as
     the transcript renders Markdown, and a button above it switches between
     the rendering and its source. The choice lasts while the file stays open
     in its session, through switching views and reconnecting.") and add
     "**File view modes.** An open file can show its changes in place, its
     source, or, for a Markdown file, its rendering, each offered by a button
     above the file only where it applies. A file with changes opens showing
     the whole file with its removed and added lines in place, colored and
     numbered as the Changes tab shows them. Otherwise a Markdown file opens
     rendered, as the transcript renders Markdown, and any other file opens as
     its source. The choice lasts while the file stays open in its session,
     through switching views and reconnecting. The next file opens in its own
     default."
   - **Alternative:** Keep "Markdown preview" for the Markdown rule and add a
     separate "File diff" requirement. That gives two requirements with
     overlapping defaults.
4. **File tree: it lives under the Files tab, with a branch header and status
   colors.**
   - **Plan:** "The workbench's Files tab shows the directory the selected
     session works in, or a new-session draft's workspace directory, as a tree,
     folders first, whose folders expand and collapse. It hides
     version-control folders and operating-system metadata files at any depth,
     such as `.git` and `.DS_Store`, shows files Git ignores, lists nothing
     outside that directory, and shows why a folder cannot be read. Its header
     shows the branch and the lines added and removed across the changes, or
     why the changes cannot be listed. Each changed file, and each folder above
     one, is colored by its change: modified, added, or deleted, and modified
     for a folder whose files changed in different ways."
   - **Alternative:** Leave the colors and header out of the requirements, as
     looks the user judges. Losing them would then not count as a bug.
5. **File viewer: a file opens between the transcript and the tree.**
   - **Plan:** "Clicking a file in the file tree opens it between the
     transcript and the tree, or in the transcript's place on a narrow screen,
     under a tab with its name that closes it, and marks its row. Clicking
     another file replaces it. Opening the Files tab shows the tree alone
     beside the transcript until a file is opened, and closing the file's tab
     returns to that. The pane shows the file's text, or why, when the file is
     binary, larger than 1 MiB, not UTF-8 text, or cannot be read, and reads
     nothing outside the file tree's directory."
   - **Alternative:** None that keeps the design.
6. **Changes view: it replaces the tree under its own tab.**
   - **Plan:** "The workbench's Changes tab shows, in place of the file tree,
     each file in the selected session's directory, or a new-session draft's
     workspace directory, that differs from where the session's branch left the
     workspace's branch, counting commits, uncommitted edits, and untracked
     files but not files Git ignores. It shows the branch and the lines added
     and removed, in total and for each file, and says why when the directory
     is not in a Git repository."
   - **Alternative:** None that keeps the design.
7. **Change diffs: Open goes to the Files tab.**
   - **Plan:** The first sentence becomes "Each file in the Changes tab has a
     button that opens it under the Files tab, unless it was deleted, and shows
     its changed lines …". The rest is unchanged.
   - **Alternative:** None that keeps the design.
8. **Changes updates: the tree's colors and header and the file's diff update
   too.**
   - **Plan:** "While the page is visible, the Changes tab, the file tree's
     colors and header, and an open file's changes show changes on disk and new
     commits within about a second."
   - **Alternative:** Keep "the Changes tab shows changes on disk and new
     commits within about a second" and leave the other views' freshness
     unstated.
9. **Layout through navigation: the Files/Changes choice is page-wide and
   reload closes it.**
   - **Plan:** "Whether the session list shows, whether the workbench shows the
     files, the changes, or nothing, and the panels' widths are the same for
     every session. Each session and new-session draft keeps its own split
     between the transcript and the workbench, and a draft's split carries to
     the session it creates. The layout lasts through switching views and
     reconnecting. Reloading the page restores the default layout, with the
     workbench closed."
   - **Alternative:** None that keeps the design.
10. **Resizing panels: the split is between the transcript and the file or the
    changes.**
    - **Plan:** "On a wide screen, dragging the boundary beside the session
      list or the file tree, or between the transcript and the open file or
      the changes, resizes them."
    - **Alternative:** None that keeps the design.

## Test plan

All page specs are e2e (`make e2e`) unless marked Node.

- **Helpers in `workspace.spec.ts`:**
  - `showFiles` clicks `getByRole("tab", { name: "Files" })` and expects
    `aria-selected="true"`.
  - Add `showChanges` for the Changes tab, and `closeWorkbench` for the
    "Toggle workbench" button.
  - `changes` becomes `page.getByRole("region", { name: "Changes" })`.
  - `previewButton` gives way to `modeButton(page, "Diff" | "Source" | "Preview")`,
    found in `group "Show as"`.
  - `pane` keeps `viewer(page).getByRole("tabpanel")` (D6).
  - D2 removes the tabs while nothing is selected, so every test that called
    `showFiles` before selecting a session calls it after `newSessionButton` or
    `newSession`. This covers the tree tests (lines 259–575), the file view
    tests (996–1260), and the wide tests.
- **Tree tests** keep their assertions. "each session keeps its own expanded
  folders" hides and reopens through `closeWorkbench` and `showFiles` in place
  of "Toggle files".
- **Changes tests** (577–661) follow the new locators. The listing snapshot's
  root becomes `region "Changes"`, with `button "Toggle workbench"` and
  `text: master +2 −1` in its header. "the Changes tab holds through switching
  sessions and resets on reload" ends with both tabs `aria-selected="false"`
  and no `region "Changes"` after the reload. "a directory outside a Git
  repository" opens Changes from the main header.
- **New, Changes tab replaces the tree:** "the Changes tab shows the changes in
  place of the file tree". Changes open, `files(page)` has count 0, and the
  region shows.
- **Changed, Open button:** "a changed file's Open button opens it under the
  Files tab". Files is `aria-selected`, the tab reads `committed.txt`, and
  Diff is `aria-pressed`.
- **New, branch header:** "the file tree's header shows the branch and the
  lines added and removed". `files(page)` contains `master` and `+2 −1`.
  Outside a repository it contains `not a git repository`.
- **Wide describe:**
  - "the toggles show and hide the panels from the header they sit in" is
    replaced by:
    - "the sessions toggle sits in the session list's header while it shows,
      and in the session's header while it is hidden"
    - "a tab opens the workbench in its mode, and the workbench's toggle closes
      it with neither tab selected"
    - "Files shows the tree alone; a file opens between the transcript and the
      tree, and closing its tab leaves the tree". This checks `region "File"`
      and the "Resize file viewer" separator: count 0, then visible, then 0.
  - **New, D2:** "with nothing selected, the header offers no Files or Changes
    tabs".
  - "dragging a sash resizes its panel…" keeps its assertions, with
    `showFiles` reordered. The tree stays alone.
  - "the file split leaves each side its minimum…" keeps its assertions. Its
    `near(viewer(page), 240)` after the 2000 px drag fails today (D1: 1 px). Its
    tight-window even split now holds with the tree beside the file.
  - **New, D1 at a half-screen window:** "at a 960 px window the transcript and
    the file split the width beside the tree evenly". `near(main, width(viewer))`
    at the default split. This geometry is the behavior. It fails today (336
    vs 96).
  - "panel widths are page-wide…" closes with `closeWorkbench`. After
    switching sessions, both tabs are `aria-selected="false"` and `files` has
    count 0.
  - "a reload hides the file tree…" is renamed "a reload closes the workbench
    and restores the default widths", with the same width checks.
- **Phone describe:**
  - Opening goes through `tab "Files"` / `tab "Changes"` taps.
  - "the file tree drawer closes on tapping outside…" is replaced: open a file,
    tap "Toggle file tree" to show `files` and mark it `aria-pressed`, then
    close it by tapping outside, by pressing Enter on the toggle, and by
    choosing a file. That is one behavior: what closes the drawer.
  - "an open file replaces the transcript until its tab closes" is replaced by:
    - "the Files tab replaces the transcript with the tree, and the
      workbench's toggle brings it back"
    - "closing the file's tab leaves the tree filling the workbench"
  - "a changed file's Open button…" is replaced by "a changed file's Open
    button shows the file in place of the changes". The tab reads the name,
    `region "Changes"` has count 0, and `main` is hidden.
  - "panels cannot be resized…": "Resize files" and "Resize file viewer"
    have count 0, and "Resize sessions" stays hidden.
  - **New:** "crossing the breakpoint closes the phone's workbench and leaves
    the wide one as it was". Changes is open wide. At 390 px the transcript
    shows. Tapping Files opens the tree. Back at 1600 px, Changes is
    `aria-selected`.
- **File view tests:**
  - "clicking a file opens it…" asserts `group "Show as"` has count 0 in place
    of the preview button.
  - "a Markdown file opens as a preview…" becomes "an unchanged Markdown file
    opens in Preview, and Source shows its text". It asserts Preview is pressed
    and the wrap button absent, then clicks Source and sees the text and the
    wrap button.
  - "a Markdown file's source view holds…" follows `modeButton`, with the same
    flow.
  - **New:** "a changed file opens in Diff, showing the whole file with its
    removed and added lines in place". A 12-line committed file is edited at
    line 10. The `pane` snapshot shows lines 1–9 and 11–12 as numbered text,
    `deletion: 10 …`, and `insertion: 10 …`. Lines 1–6 lie outside the hunk,
    so plain hunks would fail this.
  - **New:** "a changed Markdown file offers Diff, Source, and Preview, and
    opens in Diff".
  - **New, D3:** "a changed file waits for its changes to be listed before
    choosing Diff". Open a changed file, switch away, `hold`
    `workspace/list_changes`, and switch back. While held, the pane is empty,
    `group "Show as"` has count 0, and no deletion row shows. After release,
    Diff is pressed.
- **`session.spec.ts`:** the child session test (1579) opens the tree with the
  `Files` tab. Its `tabpanel` read works again under D6.
- **`hero.spec.ts`:** it opens the tree with `page.getByRole("tab", { name: "Files" })`
  and drops the old panel-tab click. `make screenshots` regenerates all four
  images in `docs/images`, since the session header and the file panel changed
  in both. The README's alt text still holds.
- **Node, new `diff.test.mjs`:**
  - `wholeFile` fills context before, between, and after hunks from the text.
  - Removed lines do not advance the new-side line.
  - Text with no trailing newline.
  - Empty text with no hunks gives `[]`.
  - Text with more lines than the hunks cover is extended with context.
  - `statuses`: a folder of only added files is added, a mixed folder is
    modified, a deleted file's folders are deleted, and nested folders all take
    a status.
  - Add the suite to `make test`.

## Implementation plan

1. `src/diff.ts`, `src/highlight.ts`, `src/Changes.tsx`: move `rowsHtml` to
   `highlight.ts`, and move folder statuses into `diff.ts` as `statuses`.
   `createListing` calls `statuses` and replaces `file(path)` with
   `change(path)` (D3).
2. `cmd/ox/web/diff.test.mjs` (new), `Makefile`: add the Node suite and its
   entry in `make test`.
3. `src/FileView.tsx`: label Diff (D4), give each content element
   `role="tabpanel"` (D6), and take `change` as
   `{ file?: ChangedFile } | undefined`, choosing no mode while it is
   `undefined` (D3).
4. `src/Workbench.tsx`, `src/FileTree.tsx`, `src/App.tsx`, `src/style.css`:
   - Render the workbench's parts as `.panes` children.
   - Set `--tree` on `.panes`, size the tree from it, and drop `FileTree`'s
     `width`.
   - Rewrite the split and minimum rules and the narrow hide rule (D1).
   - Rename `Close` to "Toggle workbench" (D5).
5. `src/Panels.tsx`, `src/App.tsx`: add `PanelToggles`'s `workbench` prop and
   pass `files() !== undefined` (D2).
6. `e2e/workspace.spec.ts`, `e2e/session.spec.ts`, `e2e/hero.spec.ts`: make the
   spec changes in the test plan.
7. Run `make test` and `make e2e`. Then run `make screenshots` and keep the
   four regenerated `docs/images/hero*.png`.
8. `docs/functional-requirements.md`: apply the approved Requirement changes.
   `docs/architecture.md` and `cmd/ox/web/AGENTS.md`: apply the Documentation
   updates.

## Requirement changes

- **Changed: Panel toggles.**
  - Old: "Two buttons show and hide the session list and the file tree, each
    showing whether its panel is shown."
  - New: "A button shows and hides the session list, showing whether it is
    shown: in the list's header while the list shows, and in the session's
    header otherwise. Files and Changes tabs in the session's header open the
    workbench beside the transcript on the files or the changes, showing which
    one it shows, with neither selected while it is closed. A button at the
    start of the workbench's header closes it."
- **Changed: Resizing panels.**
  - Old: "On a wide screen, dragging the boundary beside the session list, the
    file tree, or between the transcript and the file viewer resizes them."
  - New: "On a wide screen, dragging the boundary beside the session list or
    the file tree, or between the transcript and the open file or the changes,
    resizes them."
- **Changed: Layout through navigation.**
  - Old: "Which panels show, which tab the file tree's panel shows, and their
    widths are the same for every session. Each session and new-session draft
    keeps its own split between the transcript and the file viewer, and a
    draft's split carries to the session it creates. The layout lasts through
    switching views and reconnecting; reloading the page restores the default
    layout."
  - New: "Whether the session list shows, whether the workbench shows the
    files, the changes, or nothing, and the panels' widths are the same for
    every session. Each session and new-session draft keeps its own split
    between the transcript and the workbench, and a draft's split carries to
    the session it creates. The layout lasts through switching views and
    reconnecting. Reloading the page restores the default layout, with the
    workbench closed."
- **Changed: Narrow screens.**
  - Old: "On a narrow screen, such as a phone's, the page shows one pane at a
    time: the transcript, or the open file in its place until its tab is
    closed. The session list and the file tree open over it from buttons in
    the session's header, and close when the user taps outside them, picks a
    session or a new-session draft, or opens a file; panels cannot be resized.
    At load, the session list is open when the URL names no session, and
    closed otherwise."
  - New: Entry 2's Plan text.
- **Changed: File tree.**
  - Old: the current text, which begins "Beside the transcript, a panel shows
    the directory…".
  - New: Entry 4's Plan text.
- **Changed: File viewer.**
  - Old: the current text, which begins "Clicking a file in the file tree opens
    it beside the transcript…".
  - New: Entry 5's Plan text.
- **Changed: Markdown preview**, retitled **File view modes**.
  - Old: the current text.
  - New: Entry 3's Plan text.
- **Changed: Changes view.**
  - Old: the current text, which begins "The panel that shows the file tree has
    a Files tab…".
  - New: Entry 6's Plan text.
- **Changed: Change diffs.**
  - Old: "…opens it in the file viewer, unless it was deleted…".
  - New: "…opens it under the Files tab, unless it was deleted…". The rest is
    unchanged.
- **Changed: Changes updates.**
  - Old: "While the page is visible, the Changes tab shows changes on disk and
    new commits within about a second."
  - New: "While the page is visible, the Changes tab, the file tree's colors and
    header, and an open file's changes show changes on disk and new commits
    within about a second."

## Documentation updates

- `docs/architecture.md`, "Client modules":
  - Add `Workbench.tsx` to the UI components.
  - Add a `diff.ts` row: diff line counts, the whole-file diff, and changed
    files' and folders' statuses.
  - Add the diff rows to `highlight.ts`.
  - State that `Layout`, through `createPanels`, owns the session list's
    visibility, the workbench's mode (one each for the wide and narrow
    layouts), the panel widths, and the narrow drawer.
- `docs/architecture.md`, "Session control": "open file (and Markdown
  preview/source toggle)" becomes "open file and its chosen mode".
- `docs/architecture.md`, "File tree and viewer": retitle it "Workbench".
  - The file view's diff reads the listing's hunks for its file.
  - The Changes bullet: while the workbench shows, in either mode, it polls
    `workspace/list_changes` once per selection, for the tree's statuses, the
    branch summary, the open file's diff, and the changes view.
- `cmd/ox/web/AGENTS.md`: add `diff.test.mjs` to the Node suites `make test`
  runs.
