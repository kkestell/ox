# Newest sessions first, and collapsible child sessions

## Goal

The sidebar lists each workspace's top-level sessions oldest first, so the
newest work sits at the bottom, and a session with many children fills the
list with rows the user cannot hide. This change does the two Sidebar items
in `docs/todo.md`, **Newest sessions first** and **Collapse a session's
children**:

- Top-level sessions list newest first. Children and grandchildren stay
  oldest first beneath their parent, so a plan, work, and review read in
  order.
- A session with shown children gets a chevron, after its title, that hides
  and shows every session beneath it. The choice lives in the page's memory:
  children start shown, and a reload shows them all again.

## Related code

- `cmd/ox/web/src/Sidebar.tsx` — `rowsOf` flattens a workspace's shown
  sessions depth-first, oldest first, computing each row's `depth`, `rise`,
  `first`, and `last` for the guide lines. `SessionRow` renders a row: the
  `button.open` (status dot and title) and the ⋯ `MenuButton`. `Sidebar` owns
  the page-memory `shown` set (workspaces whose archived sessions show), the
  model for the collapsed set.
- `cmd/ox/web/src/ox.tsx` — `Workspace.sessions` is oxd's order: `reload`
  installs `workspace/list`'s sessions, oldest first, and `session/created`
  appends.
- `internal/server/methods.go` (`publishLocked`) and `internal/store/store.go`
  (`ORDER BY s.created_at, s.rowid`) — oxd appends a session to
  `workspace.sessions` and broadcasts `session/created` under `Server.mu`, so
  the list order and the notification order agree. After a restart the store
  orders by a nanosecond `created_at`, ties by `rowid`.
- `internal/server/mcp.go` (`addChild`) — a child's store row is written
  before its process starts and published after, but only its parent's
  goroutine creates its children, so siblings are created, stored, and
  published in one order.
- `cmd/ox/web/src/style.css` — `nav li`, `nav li button.open`, `nav li .title`,
  `nav li button.icon` (the ⋯, shown on `li:is(:hover, :focus-within)`), the
  touch rule under `@media (pointer: coarse)` that shows row buttons without
  hover, `.status.requires_action`, and the file tree's
  `.chevron-right.open::before { transform: rotate(90deg) }`.
- `cmd/ox/web/src/FileTree.tsx` — the existing disclosure: a lucide
  `chevron-right` that rotates when open.
- `cmd/ox/web/e2e/session.spec.ts` — `createChild`, `family`, and the tests
  of the session tree ("session list membership and order don't wait for
  transcripts", "children list beneath their parent, …").
- Design reference: `~/src/hypercube/internal/ui/web/sidebar/session-item.tsx`
  (`Title`, the chevron just after the title or over its end, shown on hover
  or keyboard focus; its click toggles and does not choose the session).

## Decisions

- **Order comes from oxd's array, not timestamps.** `rowsOf` lists the
  top-level sessions in reverse of `workspace.sessions` and every child group
  in its order. The array is oxd's creation order on every path: list, live
  `session/created`, and reload after a reconnect. Sessions created in the
  same second, or the same nanosecond, keep oxd's order, because the client
  never compares `created_at`. The store breaks `created_at` ties by `rowid`,
  and sibling children are serialized on their parent's goroutine, so sibling
  order is the same after an oxd restart. A new top-level session appears at
  the top of its workspace; a new child appears last among its siblings.
- **Collapsed set.** `Sidebar` owns `collapsed`, a signal holding a `Set` of
  session IDs, beside `shown`, and toggles it immutably as `shown` is. It
  lives for the page: the sidebar stays mounted while hidden or in the narrow
  drawer, and reconnecting keeps it because session IDs do not change. A
  reload starts it empty, so every child shows. A deleted session's ID stays
  in the set; IDs are never reused, and a deleted session has no children, so
  the entry changes nothing.
- **What a row has.** `Row` gains
  `children: "none" | "shown" | "hidden"`, computed in `rowsOf` from the shown sessions and `collapsed`. A row has
  children when at least one of its children is shown, so a session whose
  only children are archived and hidden gets no chevron, and Show archived
  brings the chevron back with them. `rowsOf` does not descend into a
  collapsed session, so `rise`, `first`, and `last` count only rows that show
  and the guide lines stay right. An ID in `collapsed` whose session has no
  shown children reads as `"none"`.
- **Live updates.**
  - A child created under a collapsed session stays hidden, and the parent
    stays collapsed.
  - A child created under a session that had none shows: the parent was
    never collapsed, or its stale entry hides it, which is the user's last
    choice for that session.
  - Archiving a collapsed parent archives its family; with archived sessions
    hidden, all of it disappears. Shown, it keeps its place, still collapsed.
  - Deleting needs no children, so a deleted session never hides others.
  - Another page's collapsing does not reach this page.
- **The open session hidden.** Collapsing the family of the open session
  leaves it open in the main pane; no row is highlighted, as when an archived
  session's URL opens it while archived sessions are hidden. A hash change to
  a hidden session opens it the same way, without expanding its ancestors.
- **Row markup.** A row becomes: `button.open` (status dot and title), the
  chevron button, then the ⋯ button.
  - `button.open` shrinks to its content (`flex: 0 1 auto`, the title
    ellipsized inside it), so the chevron sits just after the title.
  - The ⋯ button takes `margin-left: auto`.
  - A click whose target is the `li` itself, its padding and empty space,
    selects the session, so the whole row still opens it. Clicks on the
    chevron and ⋯ have their own targets and do not select. The `li` gets a
    biome-ignore for `noStaticElementInteractions`, naming `button.open` as
    the keyboard path.
- **Chevron.** A `<button type="button">` with classes `icon chevron-right`,
  plus `open` while its children show, rotated as the file tree's is.
  - Its accessible name and title are "Child sessions", and `aria-expanded`
    says whether they show.
  - It shows under the same rule as the ⋯ button: the row hovered or focused
    within, and always on a touch screen. While hidden it takes no width as
    well as no opacity, so a parent's title is not cut short until the
    chevron shows. The touch rule gives it its width.
  - Clicking it toggles the session's ID in `collapsed` and does not call
    `onSelect`, so the narrow drawer stays open.
  - On a child row it keeps the row's smaller size, a 1rem icon in a 1.25rem
    button.
- **Requirements.**
  - **Session order:** changed as below. It holds through reconnects and
    reloads because the client's order comes only from oxd's array.
  - **Showing archived sessions:** "they keep their place" holds, since
    archived sessions take part in the order and the collapse as any shown
    session does.
  - **Touch screens:** changed to include the chevron.
  - **Session status:** unchanged. A collapsed session's dot shows its own
    status only, and nothing marks the status of the sessions it hides.
  - **Narrow screens:** "close when the user … picks a session" holds. The
    chevron selects nothing, and a click on the row's empty space selects.
  - **Session URL:** holds; a hidden session's URL opens it.
  - No request reaches oxd, so the request lifecycle concerns do not apply.
- **Construction.** The chevron and `aria-expanded` exist only for a row
  whose `children` is not `"none"`. Readers switch on `children` instead of
  checking a collapsed flag against a child count.
- **Ownership.** `collapsed` is page memory in `Sidebar`. It is needed
  because the user hides a family until they show it again. It has no races:
  only this page's clicks write it. It needs no cleanup, because stale IDs
  are inert.

## Check your assumptions

1. **Top-level sessions newest first.** Kept: the user approved the text.
   - **Plan:** Session order becomes: "Each workspace lists its top-level
     sessions newest first, each followed by its child sessions, oldest first
     and indented beneath it; a child's own children follow it the same way,
     indented one level further." Old: "Each workspace lists its top-level
     sessions oldest first, each followed by its child sessions, oldest first
     and indented beneath it; a child's own children follow it the same way,
     indented one level further."
   - **Alternative:** None proposed; the user chose this order.
2. **Collapsing child sessions.** Changed: the user approved the text
   without its last sentence, as item 3 decides.
   - **Plan:** Adds **Collapsing child sessions**: "A session with child
     sessions shows a chevron after its title while its row is hovered or
     focused; clicking it hides or shows every session beneath it, and
     clicking elsewhere on the row opens the session. Children start shown,
     a child created under a collapsed session stays hidden, hiding the open
     session leaves it open, and reloading the page shows every child again."
     It also changes
     **Touch screens** so the chevron shows without hover, as the ⋯ button
     does. Old: "On a touch screen, session and file rows are tall enough to
     tap, the session list's New session and actions buttons always show,
     focusing a text field does not zoom the page, and the page keeps its
     controls clear of the notch, rounded corners, and browser toolbars."
     New: "On a touch screen, session and file rows are tall enough to tap,
     the session list's New session, actions, and child sessions buttons
     always show, focusing a text field does not zoom the page, and the page
     keeps its controls clear of the notch, rounded corners, and browser
     toolbars."
   - **Alternative:** None; a touch screen has no hover, so without the
     change its users could not collapse.
3. **What a collapsed session shows for its hidden children.** Changed: a
   collapsed session shows nothing for the sessions it hides, whether they
   are waiting on the user, running, idle, or parked.
   - **Plan:** A collapsed session's row shows only its own status.
   - **Alternative:** An amber dot at the end of the collapsed session's row
     while a hidden session waits on the user, or a mark for hidden running
     work too.
4. **Collapsing the family of the open session.** Kept.
   - **Plan:** The open session stays open in the main pane with no row
     highlighted, as an archived session opened by URL is while archived
     sessions are hidden. Opening a hidden session by URL works the same way.
   - **Alternative:** Highlight the collapsed ancestor's row as standing in
     for the open session, or expand the ancestors whenever a hidden session
     opens. Either keeps a row lit for the open session, at the cost of a
     second selected style, or of undoing the user's collapse.

## Test plan

All page behavior, in `cmd/ox/web/e2e/session.spec.ts` with the fake harness.

- **Newest first, reload included.** Update "session list membership and
  order don't wait for transcripts": each `["older session", "younger session"]`
  becomes `["younger session", "older session"]`. Its reload step covers
  `workspace/list` order, and its live step covers `session/created`.
- **Children stay oldest first under a newest-first parent list.** Update
  "children list beneath their parent, each level one deeper, before the
  parent's next sibling": the list becomes
  `["after the root", "tree root", "Tree first", "Tree leaf", "Tree second"]`,
  level 1 `["after the root", "tree root"]`. Rename it to "children list
  oldest first beneath their parent, each level one deeper, after newer
  top-level sessions".
- **New test: "a session's chevron hides and shows every session beneath it".**
  - Starts from `family`; only Parent and Child have a "Child sessions"
    button.
  - Child's chevron is hidden until its row is hovered.
  - Collapsing Child hides Grandchild and sets `aria-expanded="false"`; the
    selected session and URL are unchanged.
  - Collapsing Parent hides Child.
  - After `services.restartRelay()` and the list reloads, Child is still
    hidden.
  - Expanding Parent shows Child with Grandchild still hidden.
  - Clicking the right end of Parent's row, between the chevron and ⋯,
    selects Parent.
  - A reload shows all three expanded.
  - No existing test reaches the chevron.
- **New test: "collapsing the open session's family leaves it open, and a
  new child stays hidden".**
  - With `family`, open Grandchild, then collapse Parent. Grandchild stays
    open (heading), and no row has `aria-current`.
  - `createChild` under the collapsed Parent adds no visible row, and
    expanding Parent shows it last among Parent's children.
  - It covers the open session hidden and live arrival under a collapsed
    parent, which share setup.
- **New test, in a `test.describe` with the touch-screen `test.use` from
  `workspace.spec.ts`'s narrow describe: "on a touch screen a parent's
  chevron shows without hover and keeps the session list open".**
  - Open the drawer and tap Parent's chevron.
  - Child hides, and the session list stays open.
  - It guards the touch rule and the click that must not select.

## Implementation plan

1. `cmd/ox/web/src/Sidebar.tsx`:
   - Give `rowsOf` the `collapsed` set. Reverse the top-level group
     (`parentId === undefined`), and compute `children` per row.
   - Skip descending into collapsed rows.
   - Update its comment: newest first at the top level, oldest first beneath.
2. `Sidebar`: add the `collapsed` signal and a `toggle(id)` beside `shown`.
   Pass `collapsed()` into the `rows` memo. Pass `SessionRow` an
   `onToggle()`.
3. `SessionRow`: render the chevron button when `row.children` is not
   `"none"`. Add the `li` click that selects when
   `event.target === event.currentTarget`. Update its comment.
4. `cmd/ox/web/src/style.css`:
   - `nav li button.open` shrinks to content, and `.title` drops `flex: 1`.
   - The ⋯ button takes `margin-left: auto`.
   - The chevron is zero-width and transparent until the row is hovered,
     focused within, or on a touch screen. Give the chevron its own size and
     margin, apart from the ⋯ button's `margin-right: -0.25rem`.
   - Rotate `nav li .chevron-right.open::before` 90°.
   - Keep the parked and archived dimming on `button.open`.
5. `cmd/ox/web/e2e/session.spec.ts`: the test updates and new tests above.
6. `docs/functional-requirements.md`: apply the approved text.
7. `docs/todo.md`: remove **Collapse a session's children** and **Newest
   sessions first**.

## Requirement changes

- **Changed:** **Session order.**
  - Old: "Each workspace lists its top-level sessions oldest first, each
    followed by its child sessions, oldest first and indented beneath it; a
    child's own children follow it the same way, indented one level further."
  - New: "Each workspace lists its top-level sessions newest first, each
    followed by its child sessions, oldest first and indented beneath it; a
    child's own children follow it the same way, indented one level further."
- **Added:** **Collapsing child sessions.** A session with child sessions
  shows a chevron after its title while its row is hovered or focused;
  clicking it hides or shows every session beneath it, and clicking elsewhere
  on the row opens the session. Children start shown, a child created under a
  collapsed session stays hidden, hiding the open session leaves it open, and
  reloading the page shows every child again.
- **Changed:** **Touch screens.**
  - Old: "On a touch screen, session and file rows are tall enough to tap,
    the session list's New session and actions buttons always show, focusing
    a text field does not zoom the page, and the page keeps its controls
    clear of the notch, rounded corners, and browser toolbars."
  - New: "On a touch screen, session and file rows are tall enough to tap,
    the session list's New session, actions, and child sessions buttons
    always show, focusing a text field does not zoom the page, and the page
    keeps its controls clear of the notch, rounded corners, and browser
    toolbars."

**Collapsing child sessions** goes under Session list, after **Session
status**.

## Documentation updates

- `docs/todo.md`: remove the two Sidebar entries this change does.
