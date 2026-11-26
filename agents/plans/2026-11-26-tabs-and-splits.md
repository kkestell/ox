# Tabs and splits

## Goal

The page has three headers (transcript, file view, Changes), two toggles for the
same panel, and a Files/Changes workbench that shows one file or the changes
beside the transcript. Replace them with an editor-group layout as VS Code
has: one tab strip per group is the only header in the center; the chat, the
Changes view, and each open file are tabs; groups split by dragging a tab; the
Files | Changes panel stands at the right. The approved mockup is
`agents/scratch/tabs-and-splits/mockup-c.html` and its PNGs. Web client only:
no protocol, `oxd`, or harness change.

## Related code

- `cmd/ox/web/src/ox.tsx` — `createSessionControl` owns, per session and draft,
  `file`, `split`, and `expanded`. `file`, `split`, `openFile`, `setMode`,
  `closeFile`, `setSplit` are replaced by the tab layout below.
- `cmd/ox/web/src/App.tsx` — `Layout` composes sidebar, `main`, and
  `Workbench`; `BareHeader`, `PanelToggles`, `wrap` live here.
- `cmd/ox/web/src/Panels.tsx` — `createPanels` (session list flag, per-layout
  workbench mode, drawers, widths), `ModeTabs`, `Sash` (horizontal drags only).
- `cmd/ox/web/src/Workbench.tsx` — deleted; its listing (`createListing`) moves
  to the new per-selection component.
- `cmd/ox/web/src/FileView.tsx` — header, file bar, Diff mode go; keeps the
  read, highlight, preview.
- `cmd/ox/web/src/Changes.tsx` — `ChangesView` (header goes; gains wrap and
  reveal), `FileCard`, `Counts`, `createListing`, `BranchSummary`.
- `cmd/ox/web/src/FileTree.tsx` — tree header becomes the panel's; rows become
  draggable.
- `cmd/ox/web/src/SessionView.tsx`, `NewSessionView.tsx` — header and `toggles`
  prop removed.
- `cmd/ox/web/src/Sidebar.tsx` — header gets the toggle (left) and Add
  workspace (right); footer removed; `MenuButton` (popover menu) is the
  pattern for the phone's tab picker.
- `cmd/ox/web/src/Attachments.tsx` — `createImageDrop` already ignores drags
  that carry no `Files`, so tab and tree drags pass through it unchanged.
- `cmd/ox/web/src/diff.ts` — `wholeFile` serves only Diff mode; deleted with
  its test in `diff.test.mjs`.
- `cmd/ox/web/e2e/workspace.spec.ts` — most Files/Changes/panel specs are
  rewritten; `session.spec.ts` (child header, hash) and `hero.spec.ts` touch
  the old headers.

## Decisions

- **Construction — one layout value per selection.** New `src/tabs.ts` holds
  pure, DOM-free types and operations, owned by `SessionControl` as one signal
  `tabs`:

  ```ts
  type Tab =
    | { kind: "chat" }
    | { kind: "changes"; reveal?: { path: string } }
    | { kind: "file"; path: string; mode?: "source" | "preview" };
  type Group = { id: number; tabs: Tab[]; active: number }; // never empty
  type Node = { kind: "group"; group: Group }
    | { kind: "split"; direction: "row" | "column"; children: Node[]; sizes: number[] };
  type Layout = { root: Node; focused: number };
  ```

  A tab is identified by `tabKey` (`chat`, `changes`, `file:<path>`), unique in
  the layout; every operation (`openTab`, `closeTab`, `moveTab`, `dropOnEdge`,
  `focus`, `setMode`, `reveal`) returns a new layout that keeps this, so a tab
  exists in one group, a group is never empty (the operation that empties one
  removes it and collapses a split left with one child), the chat tab is never
  closed, and `focused` always names an existing group. `openTab` of an
  existing key activates and focuses it where it is; otherwise it appends to the
  focused group. `dropOnEdge` splits the target group with the tab in the new
  group, joining the parent split when it has the same direction (VS Code); a
  tab dropped on the edge of its own group when it is that group's only tab does
  nothing. Initial layout: one group holding the chat. A draft's control is the
  session's control (same ID), so a draft's tabs carry to its session as its
  open file does.
- **Drop zones.** `dropZone(rect, x, y)` returns `left | right | top | bottom |
  center`: an edge when the pointer is in that edge's outer quarter (the nearer
  edge in a corner), else `center`. A tab strip and a tab are drop targets of
  their own (`position` = before/after a tab by its half, or append). Pure, so
  unit tested.
- **Drag transport.** HTML5 drag and drop. Tabs and tree file rows are
  `draggable` and carry `application/x-ox-tab` (a `tabKey`) or
  `application/x-ox-file` (a path); a file dropped in the middle or on a strip
  opens in that group, on an edge opens in a new group. The drag payload is read
  during `drop` only.
  The shaded drop preview is derived from the `dragover` zone held in one
  signal in the tab area (the page's fact; cleared on `dragleave`/`drop`/
  `dragend`). Narrow: `draggable` off.
- **Focused group.** `pointerdown`/`focusin` inside a group focuses it. Opening
  from the tree or Changes panel goes to the focused group.
- **Chat stays mounted.** A group renders its active tab; a chat tab that is not
  active stays mounted and `hidden` so the transcript keeps its scroll
  position. File and Changes tabs mount only while active. Moving a tab to
  another group remounts it; chat's state is in `SessionControl`, so only
  scroll position is lost.
- **Changes tab.** `reveal` is part of the tab, replaced with a fresh object on
  each Changes-list click and removed with the tab; `ChangesView` scrolls the
  card (`article[aria-label=path]`) when `reveal` changes, including at mount.
  The tab label counts are the listing's total.
- **View actions per tab.** The strip's right end shows actions chosen from the
  active tab: file → Source | Preview when `languageFor(path) === "markdown"`
  (default Preview, other files no control, mode never `diff`); wrap toggle for
  file in Source and for Changes. `wrap` stays page-wide in `Layout` and `FileView`/
  `ChangesView` take it as a prop (ChangesView newly honors it).
- **Toggles.** `leftmostTop(layout)` and `rightmostTop(layout)` (first child of
  every split on the left/top path; for the right edge, last child of a row
  split and first of a column) name the groups whose strips carry the sessions
  toggle (when the list is closed) and the panel toggle (when the right panel is
  closed). With no selection the center shows one bare strip with both.
  The sidebar header and the right panel header carry the toggles when open.
- **Panels.** `createPanels` becomes: `sessions` (wide flag / phone drawer),
  `right` (wide flag / phone drawer), `rightMode: "files" | "changes"`
  (page-wide, default `files`), widths, `narrow`. A phone has one drawer at a
  time: `"sessions" | "right" | undefined`, starting with `sessions` when the
  URL names no session; crossing the breakpoint closes the drawer and keeps
  everything else (the layout is shared, so the phone shows the focused group's
  active tab). `treeDrawer`, per-layout `narrowMode`, and `Mode`-as-workbench
  go. The right panel is closed by default (as the workbench was) and reload
  restores that.
- **Phone.** One shared layout; the phone renders only the focused group's
  active tab, no splits, no drag. Header row: sessions toggle, `TabPicker`,
  panel toggle. The picker is a `MenuButton`-style popover listing every tab
  of the layout in group order, active one `aria-checked`, Changes with its
  counts, each closable tab with a × button; choosing a tab activates it and
  focuses its group.
- **Listing.** `createListing` is created once per selection in the new
  `Workspace` component (keyed by selection, as `Workbench` is) and polls
  while the selection shows, since the tab count, the Changes list, and the
  tree's colors read it. Previously it polled only while the workbench
  showed; the cost is one `workspace/list_changes` per second per shown
  selection.
- **Sashes.** `Sash` gains an `orientation` (column splits use a horizontal
  bar). A split's `sizes` are fractions; a drag writes them with the minimum
  size enforced by the existing range logic. Sizes are layout state and live in
  the `Layout` value. The side panels' widths stay in `createPanels`.
- **Requirements, lifecycle.** All state is page-side, so no request exists to
  lose, reorder, or reconnect; tabs persist through switching views and
  reconnecting because `SessionControl` outlives views. Reload resets, as the
  open file does. A tab for a file that is deleted or unreadable stays and
  shows why (`FileView` unchanged in that). A session removed takes its control
  with it as today.
- **Settings, Settling, Process I/O, Claude Code, Trust boundaries:** not
  touched.
- **Ownership.** New state: `Layout` in `SessionControl` (one owner; the
  tab area reads it; no copy); drop-zone signal in the tab area (UI-only,
  cleared on drop/leave/end); `rightMode` and `right` in `createPanels`.
  Nothing is stored, timed, or retried.

## Check your assumptions

1. **The session's directory leaves the center.** The transcript's path row
   goes with the header, so a session's directory is no longer on screen except
   as the hover tooltip of its chat tab (and the file tree's root).
   - **Plan:** Chat-tab tooltip shows the directory; requirement *Session
     header* becomes that.
   - **Alternative:** keep a path line under the strip for the chat tab, at the
     cost of the header the mockup removes.
2. **How the Changes tab opens.** The Files | Changes control switches the
   panel's list only. The Changes list starts with a row showing the branch and
   the total +/− counts; clicking it, or a changed file, opens (or focuses) the
   Changes tab.
   - **Plan:** that row is the only way to open the tab without picking a file.
   - **Alternative:** switching the panel to Changes also opens the tab; one
     less click, but flipping to Changes for a look at the list would also
     open a tab.
3. **The chat tab can be dragged.** It cannot be closed, but it can be moved to
   another group, or split off, like any tab.
   - **Plan:** movable, so chat can sit right of the files.
   - **Alternative:** fixed in the first group; simpler, but the layout in
     `c-a.png` with chat at the right cannot be made.

The user chose the plan for all three, and approved the requirement changes
as written.

## Test plan

- Layout operations (open, focus, close removes empty group and collapses
  splits, move, edge drop incl. same-direction join and single-tab no-op,
  `leftmostTop`/`rightmostTop`) and `dropZone`: new `tabs.test.mjs`
  (`node:test`, style of `diff.test.mjs`); cheapest layer, registered in
  `make test`.
- e2e (`workspace.spec.ts`, replacing the workbench/panel/Files specs):
  - Files click opens a tab in the focused group; again focuses it; closing it
    removes it; chat has no ×.
  - Changes list row opens the Changes tab and scrolls to that card; summary row
    opens it; card Open button opens the file tab; tab shows total counts.
  - Source | Preview only for Markdown, no Diff anywhere; wrap toggle for
    file and Changes (existing wrap/Markdown specs adapted).
  - **Drag to split:** dragging a file tab to the right edge makes a second
    group; to the middle of another group moves it; the emptied group
    disappears; dragging a tree file onto an edge opens it in a new group;
    a sash drag resizes (geometry exception). Uses Playwright `dragTo` with
    `targetPosition`.
  - Toggles: sessions toggle in the sidebar header, then at the left end of the
    leftmost top strip when closed (and in the right group after a split);
    panel toggle likewise at the right.
  - Per-session tabs through switching sessions, draft → session, and
    reconnect; reset on reload.
  - **Phone (`setViewportSize` narrow):** header row, picker shows the active
    tab, menu lists tabs with the active checked, choosing a tab shows it, ×
    closes a tab; drawers open from each side and close on scrim tap or on
    opening a file; no drag handles; session list open at load without hash;
    crossing the breakpoint keeps tabs.
  - Existing specs referencing the old headers/path row (`session.spec.ts` child
    header, panel specs at 960 px, `hero.spec.ts` selectors) updated; specs for
    Diff mode, per-session `split`, `Toggle workbench` removed.
- `diff.test.mjs`: drop `wholeFile` test.
- Not observable by tests: how it looks. The user judges via `make dev`;
  report names views touched: wide with splits, phone, sidebar header, right
  panel.

## Implementation plan

1. `src/tabs.ts` + `tabs.test.mjs`: types, operations, `dropZone`,
   `leftmostTop`/`rightmostTop`; wire the test into `package.json` and the
   Makefile if needed.
2. `ox.tsx`: replace `file`/`split` API with `tabs`, `openTab`, `closeTab`,
   `moveTab`, `dropOnEdge`, `focusGroup`, `setMode`, `revealChange`,
   `setSizes`.
3. `Panels.tsx`: reshape `createPanels`; generalize `Sash`; `SessionsToggle`,
   `PanelToggle`; delete `ModeTabs`/`PanelToggles`.
4. New `TabArea.tsx` (groups, strips, tab drag/drop, preview overlay, sashes,
   view actions, bare strip) and `TabPicker.tsx` (phone); new `Workspace`
   in `App.tsx` (per-selection listing, tab area, right panel).
5. `FileView.tsx`: remove header/bar/Diff, take `mode`; `Changes.tsx`: drop the
   header, add `wrap`/`reveal`, Changes list + summary row for the panel;
   `FileTree.tsx`: panel header, draggable rows; right panel component with
   `Files | Changes` segmented control and panel toggle.
6. `SessionView.tsx`/`NewSessionView.tsx`: drop headers; `Sidebar.tsx`: header
   buttons, remove footer.
7. `style.css`: strips, tabs, groups, sashes, drop preview, picker, drawers.
8. Delete `Workbench.tsx`, `wholeFile`; rewrite e2e as above; update
   `hero.spec.ts`; run `make test`, `make e2e`, check in headless Chrome.

## Requirement changes

- **Changed: Session header.** Old: "A session's view shows its label and the
  directory it works in." New: **Session header.** A session's chat tab shows
  its label, or "New session" for a draft, and the directory it works in as its
  tooltip.
- **Changed: Panel toggles.** New: **Panel toggles.** A button shows and hides
  the session list, and a button shows and hides the Files | Changes panel at the
  right, each showing whether its panel is shown. A button sits in its panel's
  header while the panel shows. While the session list is closed its button is
  at the left end of the leftmost top tab strip; while the panel is closed its
  button is at the right end of the rightmost top tab strip.
- **Added: Tabs.** The center is a tab area. Each session and draft has a chat
  tab, labeled with its title, that cannot be closed; the Changes view, opened
  from the Changes list, with the total lines added and removed on its tab; and a
  tab for each open file. Clicking a file in the Files tab opens it in the group
  last used, or shows its tab. Each session and draft keeps its tabs, groups, and
  splits through switching views and reconnecting, a draft's carry to the session
  it creates, and reloading resets them.
- **Added: Splitting tab groups.** Dragging a tab, or a file from the Files tab,
  onto the edge of a group opens a new group beside or above or below it; onto
  the middle of a group or its tab strip moves the tab into it; onto a tab places
  it there. A group whose last tab leaves or closes disappears. On a wide screen
  the boundaries between groups resize by dragging.
- **Added: Tab actions.** The active tab's view actions sit at the right end of
  its group's tab strip: Source and Preview for a Markdown file, and a word wrap
  button for a file's source and for the Changes view.
- **Changed: Resizing panels.** New: on a wide screen, dragging the boundary
  beside the session list, the Files | Changes panel, or between tab groups
  resizes them.
- **Changed: Layout through navigation.** New: Whether the session list and the
  Files | Changes panel show, the panel's tab, and the side panels' widths are the
  same for every session; each session and new-session draft keeps its own tabs
  and groups (see Tabs). The layout lasts through switching views and
  reconnecting. Reloading the page restores the default layout: panel closed,
  only chat tabs.
- **Changed: Narrow screens.** New: on a narrow screen the page shows one tab at
  a time and offers no splitting or dragging. The header holds the session list's
  button, a picker showing the active tab that opens a menu of the session's tabs
  (the active one checked, each closable tab with a close button), and the panel's
  button. The session list and the Files | Changes panel open over the page from
  the left and the right; either closes when the user taps outside it, picks a
  session or draft, or opens a file; panels cannot be resized. At load the session
  list is open when the URL names no session. Switching between a narrow and a wide
  screen keeps the tabs.
- **Changed: File tree.** "The workbench's Files tab" → "The panel's Files tab"
  (the rest unchanged).
- **Changed: File viewer.** New: Clicking a file in the file tree opens it as a
  tab, and marks its row. The pane shows the file's text, or why, when the file is
  binary, larger than 1 MiB, not UTF-8 text, or cannot be read, and reads nothing
  outside the file tree's directory.
- **Changed: File view modes.** New: A Markdown file can show its source or its
  rendering, from buttons in its tab strip, and opens rendered; any other file
  shows its source. The choice lasts while the tab stays open in its session.
- **Changed: Word wrap.** "A button above the file's text" → "A button in the tab
  strip, for a file's source and the Changes view".
- **Changed: Changes view.** New: The panel's Changes tab lists each file in the
  selected session's directory, or a draft's workspace directory, that differs
  from where the session's branch left the workspace's branch (counting commits,
  uncommitted edits, and untracked files but not files Git ignores), with its
  folder and lines added and removed, and the branch and totals at its top. Clicking
  a file, or the top row, opens the Changes tab; the Changes tab shows every
  file's diff, and a click on a file scrolls to it. It says why when the directory
  is not in a Git repository.
- **Changed: Change diffs.** "opens it under the Files tab" → "opens its file
  tab".
- **Changed: Changes updates.** "the Changes tab" → "the Changes list and tab".
- **Removed: Open file.** Folded into Tabs.
- **Removed:** the Diff view mode (in File view modes).

## Documentation updates

- `docs/architecture.md`: `createPanels` description (row 477), Session control
  (tabs and layout replace open file, mode, split), the Workbench section
  (renamed to the tab area and right panel; the listing polls while the
  selection shows), the component list.
- `docs/screenshots`/README images: regenerated only if the user runs
  `make screenshots`; `hero.spec.ts` selectors updated here.
