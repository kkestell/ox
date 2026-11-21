# Panel toggles and resizable panels

## Goal

The user can show and hide the session list (left panel) and the file tree
(right panel) with two toggle buttons, and resize both panels and the split
between the transcript and the file viewer by dragging. The toggles sit at the
right of the file tree's header while the tree shows, and otherwise at the right
of the session's header, whether or not a file is open. A toggle is in full
color while its panel shows and dimmed while it is hidden. The panels' visibility
and widths last for the life of the page and apply to every session; each
session keeps its own split. A reload restores the defaults.

## Related code

- `cmd/ox/web/src/App.tsx` — `Layout` renders `Sidebar` | `main` | `FileView`
  (when a file is open) | `FileTree`, and decides what `main` and the files
  panel show. It lives for the life of the page, through reconnects.
- `cmd/ox/web/src/Sidebar.tsx` — the session list. Its `shown` signal holds the
  workspaces whose archived sessions are shown, which
  **Showing archived sessions** says lasts until reload. The `folder-plus`
  button is the color reference for a shown panel's toggle.
- `cmd/ox/web/src/FileTree.tsx` — the files panel. Its header
  (`aside.files > p`, the workspace name) scrolls with the tree today.
- `cmd/ox/web/src/ox.tsx` — `createSessionControl` holds each session's and
  draft's open file (`file`) and expanded folders; it outlives the session's
  view, and a draft's control becomes its session's.
- `cmd/ox/web/src/FileView.tsx` — the file pane, `flex: 1` beside `main`.
- `cmd/ox/web/src/SessionView.tsx`, `cmd/ox/web/src/NewSessionView.tsx` — each
  renders `header.context` with its `h1` title row and path row.
- `cmd/ox/web/src/style.css` — `--sidebar-width` sizes `nav` and `aside.files`;
  `button.icon` is the icon button; icons are lucide masks
  (`.folder-plus { --icon: url(lucide-static/icons/folder-plus.svg) }`).
- `cmd/ox/web/e2e/workspace.spec.ts` — "the file viewer shows each session's
  open file" asserts `main` and the viewer split evenly by default.
- `cmd/ox/web/e2e/session.spec.ts` — "an archived session is read-only and
  hidden until shown" reaches Show archived.

## Decisions

- **Requirements:** **Panel toggles**, **Resizing panels**, and **Layout
  through navigation** are added. The layout is page state with no requests,
  so it holds through every request's lifecycle by not taking part in it:
  reconnecting keeps `Layout` mounted, and a second page has its own layout.
  **Showing archived sessions** holds because hiding the session list keeps
  `Sidebar` mounted (below). The split lives in the session's control beside
  the open file, so it follows **Open file**: kept through switching views and
  reconnecting, carried from a draft to the session it creates, and reset by a
  reload. **Open file** and **File tree expansion** hold
  because both live in the session's control, which outlives the file tree;
  hiding the tree unmounts it, which stops its polling, and showing it again
  rebuilds it from the control. **File viewer** keeps its pane open when the
  tree is hidden.
- **Ownership:** One `Panels` value, made by `createPanels()` in `Layout`,
  owns which panels show and their widths, for the life of the page. It is
  page-wide: the session list is how the user moves between sessions, so it
  cannot come and go with the selection. Each session's control owns its
  split (`split`, `setSplit`), as it owns the open file the split sizes. No
  requests, timers, or stored fields; `Panels` goes when the page unloads, and
  a split goes with its control. Each drag
  keeps its start position and widths in a closure made on `pointerdown` and
  dropped on `lostpointercapture`.
- **Construction:**
  - Widths stay in range because their setters clamp them; no reader checks.
    Panel widths: 160–640 px, default 288 px (today's 18rem), clamped by
    `Panels`. The split is the transcript's share of the width it shares with
    the file viewer, default 0.5, clamped by the control's
    `setSplit(transcript, total)` so each side keeps at least 240 px.
  - JavaScript is the one source of the widths: `--sidebar-width` goes, and
    `nav` and `aside.files` take their width from `Panels` inline.
  - The toggles render in exactly one place, decided by one accessor in
    `Layout`: `tree = () => (panels.right() ? files() : undefined)`. The
    `FileTree` `Show` and the toggles' placement both read it. When `tree()` is
    defined the toggles go to `FileTree`; otherwise to `main`'s title row: the
    view's header, or a bare title row when nothing is selected or the session
    is loading, so the left toggle is always reachable.
  - A panel's sash renders inside the same `Show` as the panel, so a sash
    never exists without both of its neighbors.
- **Hiding:** The session list is hidden with the `hidden` attribute and stays
  mounted, keeping its Show archived choice and its dialogs' state. The file
  tree is unmounted, since it polls while mounted and its state lives in the
  control.
- **Sash:** A zero-width flex item between two columns, with a 4 px hit area
  centered on the boundary (`::before`, `cursor: col-resize`), `role="separator"`,
  `aria-orientation="vertical"`, and an `aria-label`, not focusable, as VS
  Code's sashes are. Hover, after VS Code's short delay, and an active drag
  show the hit area in `--ring`. On a primary-button `pointerdown` it calls
  `preventDefault`, captures the pointer, reads the widths of its previous and
  next element siblings, and calls `drag(before, after)`, which returns the
  function that takes the pointer's horizontal travel. `pointermove` with
  capture calls it. While dragging, `:root` shows `col-resize` and disables
  text selection (`:root:has(.sash.resizing)`).
  - Session list: `drag: (before) => (dx) => setLeftWidth(before + dx)`.
  - File tree: `drag: (_, after) => (dx) => setRightWidth(after - dx)`.
  - Split: `drag: (before, after) => (dx) => control.setSplit(before + dx, before + after)`,
    on the control of the session or draft whose file is open, where
    `setSplit` clamps the transcript's width to `[240, total − 240]` and
    stores the share.
- **Split layout:** `main` and `.viewer` take `flex: <share> 1 0`, `main` with
  the selected control's split and the viewer with `1 − split`. With no viewer
  `main` fills the space alone, so `main` always carries its share; with
  nothing selected it takes 1.
- **Toggles:** `PanelToggles` renders two `button.icon` toggles, left then
  right, as VS Code orders its layout controls: `panel-left` labelled "Toggle
  sessions" and `panel-right` labelled "Toggle files", each with `title` equal
  to its label and `aria-pressed` set to whether its panel shows. A pressed
  toggle keeps the inherited `--text` color, as `folder-plus` does; an
  unpressed one is `--muted-foreground`. Icons are lucide-static's
  `panel-left.svg` and `panel-right.svg`.

## Check your assumptions

1. **One layout for the whole page.** Changed: the panels' visibility and
   widths are page-wide, and the split between the transcript and the file
   viewer is kept per session.
   - **Plan:** Hiding a panel or dragging a width changes it for every session;
     switching sessions leaves the layout as it is.
   - **Alternative:** Each session keeps its own layout, so switching sessions
     shows that session's panels and widths. The session list would appear and
     disappear as the user moves between sessions, though it is how they move.
2. **A reload restores the default layout.** Kept.
   - **Plan:** Reloading the page shows both panels at their default widths and
     splits the transcript and file viewer evenly, as an open file and expanded
     folders already reset.
   - **Alternative:** The layout survives reloads, stored in the browser. It
     would then also outlive restarts of Ox, which the request ruled out.

## Test plan

- New e2e test in `workspace.spec.ts`, "panel toggles and drags change the
  layout, which lasts until reload". No existing test toggles or resizes, and
  the file viewer test is already long; this one shares its setup pattern
  (fake harness, two sessions, a file in the workspace).
  - With nothing selected, both toggles show in `main`'s header, pressed.
  - With a session selected, both toggles show in the files panel's header,
    pressed, with the same computed color as Add workspace.
  - Open a file. Toggle files: the files panel is gone, the toggles show in
    `header.context` and not in the file viewer, Toggle files is unpressed and
    its color differs from Add workspace's, and the viewer stays open.
  - Toggle sessions: `nav` is hidden, the toggle is unpressed. Toggle both
    back.
  - Drag the session list's sash 100 px right: `nav` is 100 px wider. Drag it
    far right: `nav` is 640 px. Drag the files sash 100 px left: the files panel
    is 100 px wider. Drag the split sash 100 px left: `main` narrows and the
    viewer widens by 100 px.
  - Switch to the other session and open a file there: its split is even,
    while the panel widths match the first session's. Switch back: the first
    session's split and the panel widths are unchanged. Hide files, switch
    sessions: files stay hidden in the other session.
  - A draft's split carries to the session it creates: drag the split in a
    draft with a file open, send, and the created session keeps it.
  - Reload: both panels show at 288 px, and `main` and the viewer split
    evenly once a file is open.
- In `session.spec.ts`, "an archived session is read-only and hidden until
  shown": after Show archived, toggle sessions off and on, and the archived
  row is still shown. It guards keeping `Sidebar` mounted, and this test
  already reaches Show archived.
- The file viewer test's even-split and header-height assertions keep
  guarding the defaults and the title row's height.

## Implementation plan

1. `src/Panels.tsx` (created): `createPanels()` with `left`, `right`,
   `leftWidth`, `rightWidth` and their setters (`setLeft`, `setRight`, and
   the clamping `setLeftWidth`, `setRightWidth`); `export type Panels`; `PanelToggles(props:
   { panels: Panels })`; `Sash(props: { label: string; drag(before: number,
   after: number): (dx: number) => void })`.
2. `src/ox.tsx`: add `split` (default 0.5) and the clamping
   `setSplit(transcript: number, total: number)` to `createSessionControl`
   beside `file`, and name the split in its comment.
3. `src/App.tsx`: create `panels` in `Layout`. Add `tree()` as in Decisions.
   Render `Sidebar` with `width` and `hidden`, then the session list's sash
   (`label="Resize sessions"`) when `panels.left()`. Give `main` the flex of
   `files()?.control.split()`, or 1 with nothing selected. Pass `toggles={<PanelToggles panels={panels} />}` to `SessionView`
   and `NewSessionView` when `tree()` is undefined, and render a bare
   `header.context` title row with the toggles in the loading fallback and
   when nothing is selected. Render the split sash (`"Resize file viewer"`)
   before `FileView`, passing `FileView` its share. Render `FileTree` under
   `tree()` with its sash (`"Resize files"`) before it, its `width`, and the
   toggles.
4. `src/Sidebar.tsx`: take `width` and `hidden`; set them on `nav`.
5. `src/FileTree.tsx`: take `width` and `toggles`. Put the workspace name and
   the toggles in a fixed `header`, and the tree in a scrolling `div` beneath
   it.
6. `src/FileView.tsx`: take `share` and set the section's `flex-grow`.
7. `src/SessionView.tsx`, `src/NewSessionView.tsx`: take `toggles?:
   JSX.Element`; wrap `h1` and the toggles in a `div class="title"` row.
8. `src/style.css`: add the `panel-left` and `panel-right` icons; remove
   `--sidebar-width`; `nav[hidden] { display: none }`; `main` and `.viewer`
   take `flex-basis: 0`; `.context .title` is the title row
   (`height: var(--context-title)`, `h1` `flex: 1; min-width: 0`); the files
   panel is a column with a fixed `header` of `--context-title` height and a
   scrolling tree; `.panel-toggles` and its unpressed color; `.sash` as in
   Decisions.
9. `e2e/workspace.spec.ts`, `e2e/session.spec.ts`: the tests above.
10. `docs/functional-requirements.md` and `docs/architecture.md`: the updates
   below.

## Requirement changes

- **Added**, in a new section `## Layout` before `## Files`:
  - **Panel toggles.** Two buttons show and hide the session list and the file
    tree, each in full color while its panel shows and dimmed while it is
    hidden. They sit at the right of the file tree's header while the tree
    shows, and otherwise at the right of the session's header, whether or not
    a file is open.
  - **Resizing panels.** Dragging the boundary beside the session list, the
    file tree, or between the transcript and the file viewer resizes them.
  - **Layout through navigation.** Which panels show and their widths are the
    same for every session. Each session and new-session draft keeps its own
    split between the transcript and the file viewer, and a draft's split
    carries to the session it creates. The layout lasts through switching
    views and reconnecting; reloading the page shows both panels at their
    default widths, with the transcript and file viewer splitting evenly.

## Documentation updates

- `docs/architecture.md`, Web UI: add `Panels.tsx` to the components row, and
  state that `Layout` in `App.tsx` owns the page's panels, which show and
  their widths, for the life of the page. Add the split between the
  transcript and the file viewer to what a session's control owns and keeps.
