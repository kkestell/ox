# Archive a session

## Goal

The user can archive a session: a soft delete that keeps the session and its
history in Ox but makes it read-only until they unarchive it. Archiving stops
the session's process, cascades from a top-level session to its children, and
hides archived sessions in the sidebar until the user shows them for a
workspace. The sidebar's trash buttons become a ⋯ menu, also opened by
right-click, that holds Archive/Unarchive and Delete for a session and Show
archived/Hide archived and Remove workspace for a workspace. Other sessions'
Claude no longer lists archived sessions and cannot message them, but can
still read them.

## Related code

- `internal/store/store.go` — the schema (`CREATE TABLE IF NOT EXISTS`, no
  migrations, foreign keys on), `Session`, `Workspaces` (joins `children`),
  `AddSession`, `DeleteSession` (cascades to `events` and `children`).
- `internal/server/session.go` — `session` (fields fixed at creation, fields
  owned by `run`), `newSession`, `run`, `snapshot`, `sendPrompt`,
  `acceptMessage`, `startForInput` (the one place an input starts a parked
  session), `post`, `stop`, `destroy` (reads children under `s.mu`, stops the
  agent, deletes, broadcasts `session/destroyed`).
- `internal/server/server.go` — `Server.mu` (membership, `closed`, and the
  order of broadcasts), `children`, `Close` (posts `end` to every session and
  waits for each).
- `internal/server/methods.go` — `handle` (method table), `failure`
  (`agent.ErrBusy` → `-32002 session is busy`, others `-32000`),
  `onSession`, `listWorkspaces`, `removeWorkspace` (`workspace has sessions`),
  `configureSession` (a parked session adopts at once), `destroySession`.
- `internal/server/mcp.go` — `listSessions` (collects sessions under `s.mu`),
  `sendMessage` (the recipient's `acceptMessage`), `createSession` (the
  parent's operation stores the child under `s.mu`), `readSession`, the
  `list_sessions` description.
- `internal/fake/fake.go` — `approve <tool> <input JSON>` holds a fake
  session in `requires_action` until answered.
- `cmd/ox/web/src/protocol.ts`, `ox.tsx` (`Session`, `reload`, `receive`,
  `createSessionControl`, the returned `destroy`), `Sidebar.tsx`
  (`WorkspaceDialog`, `SessionRow`, the trash buttons), `SessionView.tsx`
  (`.composer`, `SettingsForm`), `SettingsFields.tsx` (`popover="auto"`
  pattern), `style.css` (`nav` rules, `.trash` icon, the parked row's
  opacity).
- Tests: `internal/server/mcp_test.go` `TestCreateSession` (fake harness,
  parent and child, `c.tool` over an attached MCP connection),
  `internal/server/methods_test.go` `TestSessionLifecycle` (restart over the
  same store); `cmd/ox/web/e2e/session.spec.ts` ("a cancelled deletion's
  reply cannot close the next confirmation", "a child session shows beneath
  its parent with its title"), `e2e/workspace.spec.ts` ("a cancelled add or
  remove cannot settle the next dialog", "a workspace is removed once it has
  no sessions"), `e2e/ui.ts`.

## Decisions

- **Protocol.**
  - `session/archive` `{session_id}` archives the session and, for a
    top-level session, every child. Refused with `-32002 session is busy`
    when any of them is neither `idle` nor `parked`, archiving none. Each
    archived session's process is stopped as parking stops it (`parked`
    update). Repeating it on an archived family succeeds.
  - `session/unarchive` `{session_id}` unarchives the session and, for a
    top-level session, every child. A child whose parent is archived is
    refused with `-32000 parent session is archived`. Repeating it succeeds.
  - Notifications `session/archived` and `session/unarchived`, params
    `{session_id}`, one per session, broadcast under `s.mu` like
    `session/destroyed`, before the response.
  - `workspace/list` gives each archived session `"archived": true`
    (omitted otherwise). `session/created` sessions are never archived.
  - An archived session refuses `session/send`, `session/configure`, and
    `send_message` with `-32000` / tool error `session is archived`.
    `create_session` called by an archived session is refused with
    `session is archived`, so no child of an archived parent is ever
    unarchived. `list_sessions` leaves archived sessions out. `read_session`,
    `session/get`, `session/interrupt` (nothing to stop), and
    `session/destroy` are unchanged.
- **Construction.**
  - *An archived session has no process.* The archive operation stops the
    agent on the session's own goroutine in the same operation that holds it
    while the flag is set, and `startForInput`, the only place an input
    starts a parked session, refuses when the flag is set. So at every
    operation boundary, archived implies `agent == nil`, and every input
    path (prompt, message, `create_session`'s first message) is refused in
    that one place. `configureSession` checks the flag only on its
    `agent == nil` branch, since a live agent cannot be archived.
  - *A visible child never lacks its parent's row* (a child of an archived
    parent is archived). Enforced under `s.mu`, which guards every flag:
    archiving a top-level session sets its children's flags in the same
    critical section; unarchiving a child checks its parent's flag there;
    `create_session` checks its caller's flag where it stores the child. The
    page renders child rows only under their parent's row, so even a
    transient mismatch cannot show an orphan.
  - *Family archive is all or nothing.* `Server.archive` holds every member's
    goroutine before deciding, so none can start between the check and the
    flag (see Ownership).
- **Ownership.**
  - `session.archived bool`, guarded by `Server.mu` (not `run`), loaded from
    the store in `newSession`. It is a membership-like fact: list filtering,
    `workspace/list`, the cascade, and the parent check all read it with
    membership under `s.mu`, and the session goroutine reads it under `s.mu`
    in `startForInput` and `configureSession`. User-visible behavior: the
    archived flag on every page, refusals, `list_sessions` filtering.
    Cleanup: goes with the session.
  - Stored as a new table `archived (session_id TEXT PRIMARY KEY REFERENCES
    sessions ON DELETE CASCADE)`; a row means archived. A new table needs no
    migration of existing databases, and deleting a session removes the row.
    `Store.SetArchived(ids []string, archived bool)` writes a family in one
    transaction (`INSERT OR IGNORE` / `DELETE`). `Workspaces` reads it with a
    `LEFT JOIN`. A failed write fails the request and changes nothing in
    memory, as `AddSession` does.
  - The hold. `session.hold()` posts an operation that sends whether the
    session is `idle` or `parked` (its `snapshot().State`) and then waits on
    a release channel; on `true` it calls `stop` if an agent runs. It
    returns false when the session has ended. `Server.archive(sess)`, on the
    request goroutine: holds `sess`; for a top-level session reads
    `s.children(sess)` under `s.mu` (children are created only on the
    parent's goroutine, which is now held, so the list is complete) and
    holds each, skipping one that has ended; if any is busy, releases all
    with `false` and returns `agent.ErrBusy`; otherwise under `s.mu` checks
    `closed` (release `false`, `daemon is shutting down`), calls
    `SetArchived` (release `false` on error), sets the flags, broadcasts
    `session/archived` for each, unlocks, and releases all with `true`.
    Every path releases every held goroutine. Races: two archives of one
    family both hold the parent first, so the second waits for the first to
    release everything; a child archived alone holds only the child and
    waits on nothing else; `Close` posting `end` to a held session waits
    until release, and a hold posted to a session `Close` already ended
    returns false; `list_sessions` and `send_message` posting to a held
    session wait briefly. No session goroutine posts to another or waits on
    one; a held goroutine waits only on the request goroutine.
  - A configuration in flight when an archive stops an idle process ends as
    any exit does under `session/configure`: its change is kept for the next
    process and broadcast. Archive and configure come from independent
    requests, and the change was accepted before the archive.
  - Unarchive runs on the request goroutine under `s.mu` only: archived
    sessions have no process to check.
  - Page: `Session.archived: boolean` in the store, set by `reload` from
    `workspace/list` (existing sessions included) and by the two
    notifications; `false` on `session/created`. The Show archived choice is
    a `Set` of workspace IDs in a `Sidebar` signal, which lives as long as
    the page, so a reload resets it. The menu's target and position are one
    `Sidebar` signal (`{target: {kind: "session" | "workspace", id}, x, y}`
    or undefined).
- **Requirements.**
  - *Archiving a session* (new): the menu's Archive is enabled only when the
    page is connected and the session and, for a top-level session, each
    child has a live snapshot whose state is `idle` or `parked`, matching
    `oxd`'s rule. The view turns read-only only on `session/archived`, so
    the page never shows an archive `oxd` has not confirmed. A lost reply:
    the notification or the next `workspace/list` shows the outcome. Another
    page sees the notification; a send in flight from another page is
    refused with `session is archived` and shows in that page's control
    error. Archive and Unarchive ask no confirmation.
  - *Archived sessions* (new): `SessionView` renders the transcript as
    usual and, in place of the approvals, composer form, and settings, a
    line "This session is archived." with an Unarchive button. The control's
    error and notice alerts stay rendered. Unsent text lives in the
    session's control, which outlives the view, so it returns when the
    composer does. For a child whose parent is archived the line reads
    "This session is archived with its parent." and Unarchive is disabled.
    Unarchive goes through the session control (`control.unarchive()`), so
    its error shows in the session view like Stop's.
  - *Showing archived sessions* (new): rows of archived sessions are left
    out unless the workspace is in the shown set; shown, they keep their
    place, get class `archived` (dimmed like a parked row), and an archive
    icon (`role="img"`, `aria-label="archived"`) replaces the status dot. The
    hash selection is independent of the sidebar, so an archived session's
    URL opens it while hidden, and archiving the selected session keeps the
    view on it.
  - *Session status*: the archived marker above.
  - *Deleting a session*: Delete is in the session menu, opening the existing
    confirmation, for archived sessions too. For a session with children it
    is shown disabled with the reason "Delete its child sessions first".
  - *Removing a workspace*: Remove workspace is in the workspace menu,
    opening the existing confirmation. With any session it is disabled with
    the reason "Delete its sessions first", or "Delete its sessions first,
    including archived ones" when it has an archived session.
  - *Claude can message other sessions*, *Message delivery*: the
    `list_sessions` filter and the `startForInput` refusal.
  - *When settings can change*: the `configureSession` refusal; the page
    shows no settings for an archived session.
  - *Every request settles*: sidebar menu requests (Archive, Unarchive,
    Show/Hide is local) run through one `createAttempt` in `Sidebar`, whose
    error shows as an alert at the top of the sidebar. The view's Unarchive
    uses the control's attempt.
  - *Lost connection*: menu actions that send a request are disabled while
    the page is not ready.
- **Menu.** One `Menu` component in `Sidebar.tsx`: a `popover="auto"` element
  with `role="menu"` and `role="menuitem"` buttons, shown with
  `showPopover()` at fixed coordinates: the ⋯ button's bottom-right for a
  click, the pointer for a `contextmenu` event (default prevented). Light
  dismiss and Escape close it; choosing an item closes it. Items read the
  store, so they update while open. A disabled item shows its reason as a
  second line in the item. The ⋯ buttons are `button.icon.ellipsis` labeled
  "Session actions" and "Workspace actions", shown on hover and focus as the
  trash buttons were; the workspace's + button stays. Session menu: Archive
  or Unarchive (Unarchive disabled with "Unarchive its parent first" for a
  child of an archived parent), Delete. Workspace menu: Show archived or
  Hide archived (always available), Remove workspace.

## Check your assumptions

1. **Unavailable menu items show disabled with a reason.**
   - **Plan:** Delete on a session with children, Remove workspace on a
     workspace with sessions, Archive while busy, and Unarchive on a child of
     an archived parent all stay in the menu, disabled, with a one-line
     reason. Today the trash button simply isn't there.
   - **Alternative:** Leave unavailable items out of the menu, except
     Remove workspace when every session is archived and hidden. The menu is
     shorter, but the user can't tell why an action is missing.
2. **Hidden archived rows hide even when selected.** Archiving the selected
   session while archived sessions are hidden removes its row; the view stays
   on it, read-only.
   - **Alternative:** Keep the selected archived session's row visible until
     the user moves away. Costs a second visibility rule in the sidebar.
3. **New: Archiving a session** — see Requirement changes.
4. **New: Archived sessions** — see Requirement changes.
5. **New: Showing archived sessions** — see Requirement changes. The request
   asked for one new requirement; the behavior is split across three to keep
   each to the file's one or two sentences.
6. **Changed: Session status**, **Deleting a session**, **Removing a
   workspace**, **Claude can message other sessions**, **Message delivery**,
   **When settings can change** — old and new text under Requirement
   changes.

## Test plan

- **Server, new `TestArchive` in `internal/server/mcp_test.go`.** No existing
  test has a parent with a child and the archive methods; it uses
  `TestCreateSession`'s setup (fake harness, `c.tool` over attached MCP
  connections) with a parent `a`, a child `k` created by `create_session`,
  and an unrelated session `b`. In order:
  - `a` sent `approve Bash {}` waits in `requires_action`;
    `session/archive` on `a` → `-32002`, no `session/archived`, and
    `workspace/list` shows neither archived. Answer the approval, wait idle.
  - `session/archive` on `a` → `session/archived` for `a` and `k`, then a
    `parked` update for each running one; `workspace/list` marks both
    `archived`.
  - `session/send` to `a` → `-32000 session is archived`;
    `session/configure` on `a` → `session is archived`, and settings
    unchanged in `session/get`.
  - From `b`: `list_sessions` omits `a` and `k`; `send_message` to `k` →
    `session is archived`; `read_session` on `a` returns its transcript and
    `(parked)`.
  - From `a`'s MCP connection: `create_session` → `session is archived`.
  - `session/unarchive` on `k` → `parent session is archived`.
  - `New` over the same store: `workspace/list` still marks both archived.
  - `session/unarchive` on `a` → `session/unarchived` for both;
    `session/send` to `a` succeeds.
  - `session/archive` on `k` alone → only `k` archived; `session/unarchive`
    on `k` succeeds.
  - Archive `k`, then `session/destroy` on `k` succeeds;
    `workspace/remove` with only archived `a` left → `workspace has
    sessions`.
- **e2e, new test in `e2e/session.spec.ts`** "an archived session is
  read-only and hidden until shown": create a session, type unsent text,
  check Archive is disabled while an `approve` prompt waits and enabled once
  idle; Archive from the ⋯ menu → the row disappears, the view shows the
  archived line and no prompt box or settings; a second page at the same URL
  shows the archived view; workspace menu Show archived → the row shows with
  class `archived` and the archived marker; right-click on the row opens the
  menu with Unarchive and Delete; Hide archived hides it; Unarchive in the
  view → the composer returns with the unsent text; archive again, Show
  archived, reload → hidden again.
- **e2e, extend "a child session shows beneath its parent with its title"**:
  the parent's menu shows Delete disabled with its reason; archiving the
  parent hides both rows; Show archived shows both; the child's view has
  Unarchive disabled; the child's menu deletes it as before.
- **e2e, extend "a workspace is removed once it has no sessions"** (in
  `workspace.spec.ts`): with its only session archived and hidden, the
  workspace menu shows Remove workspace disabled with "Delete its sessions
  first, including archived ones"; after showing and deleting it, Remove
  workspace works as before.
- **e2e, update** "a cancelled add or remove cannot settle the next dialog"
  and "a cancelled deletion's reply cannot close the next confirmation" to
  open Remove workspace and Delete through the menus. Add a `ui.ts` helper
  that opens a row's menu by its ⋯ button.

## Implementation plan

1. `internal/store/store.go`: the `archived` table in `schema`;
   `Session.Archived`; `LEFT JOIN archived` in `Workspaces`;
   `SetArchived(ids []string, archived bool) error` in one transaction.
2. `internal/server/session.go`: `archived` field with a comment that
   `Server.mu` guards it, set in `newSession`; `errArchived =
   errors.New("session is archived")`; `isArchived()` reads it under
   `s.mu`; `startForInput` refuses with `errArchived` before `start`;
   `hold()`; `Server.archive(sess)`; `Server.unarchive(sess)` (under `s.mu`:
   `closed`, the parent check, `SetArchived`, flags, `session/unarchived`
   broadcasts).
3. `internal/server/server.go`: extend `mu`'s comment to the archived flags.
4. `internal/server/methods.go`: `session/archive` and `session/unarchive`
   in `handle`, each parsing `session_id`, looking the session up under
   `s.mu` (`-32001` when missing, shutting down when closed), and replying
   after the call; `configureSession` refuses with `errArchived` on its
   parked branch when archived; `listWorkspaces` adds `archived`.
5. `internal/server/mcp.go`: `listSessions` skips archived sessions while
   collecting under `s.mu`, and its description says it lists sessions that
   are not archived; `createSession`'s parent operation refuses with
   `errArchived` when the caller is archived, beside the `closed` check.
6. `cmd/ox/web/src/protocol.ts`: `archived?: boolean` on `workspace/list`
   sessions; the two notifications; the two methods.
7. `cmd/ox/web/src/ox.tsx`: `Session.archived`; `reload`, `session/created`,
   and the two notifications set it; `archive(id)` and `unarchive(id)` on
   the provider beside `destroy`; `control.unarchive()` through `start`.
8. `cmd/ox/web/src/Sidebar.tsx`: the shown set, the menu signal, `Menu`,
   the ⋯ buttons and `onContextMenu` on workspace headers and session rows,
   the archived row class and marker, row filtering, the sidebar's
   `createAttempt` and alert; remove the trash buttons.
9. `cmd/ox/web/src/SessionView.tsx`: the archived line in place of the
   approvals, form, and `SettingsForm`.
10. `cmd/ox/web/src/style.css`: `.ellipsis` and `.archive` icons
    (`lucide-static` `ellipsis.svg`, `archive.svg`), the menu, the archived
    row's opacity, the archived line; drop `.trash` rules no longer used.
11. Tests above; docs below.

## Requirement changes

- **Added:** **Archiving a session.** While a session and its child sessions
  are idle or parked, the user can archive it from its menu without
  confirmation, which archives its children too and stops their Claude
  processes, schedules, and background tasks. Unarchiving a session
  unarchives its children; a child can be archived alone, but not
  unarchived while its parent is archived.
- **Added:** **Archived sessions.** An archived session keeps its transcript
  and shows it with an Unarchive button in place of the composer and
  settings, refuses prompts, messages, and setting changes, and stays
  archived through `oxd` restarts and on every page. Its unsent text returns
  when it is unarchived.
- **Added:** **Showing archived sessions.** A workspace's archived sessions
  are hidden until the user shows them from the workspace's menu, a choice
  that lasts until the page reloads; shown, they keep their place, dimmed,
  with an archived marker in place of their status. An archived session's
  URL opens it even while it is hidden.
- **Changed:** **Session status.**
  - Old: Each session shows whether it is parked, idle, running, or waiting
    on the user.
  - New: Each session shows whether it is parked, idle, running, waiting on
    the user, or archived.
- **Changed:** **Deleting a session.**
  - Old: A session can be deleted only when it has no child sessions.
    Deleting it asks first, then stops Claude, deletes Ox's history of it,
    leaves Claude's transcript and the workspace on disk, and removes it
    from every page.
  - New: A session, archived or not, can be deleted only when it has no
    child sessions. Deleting it asks first, then stops Claude, deletes Ox's
    history of it, leaves Claude's transcript and the workspace on disk, and
    removes it from every page.
- **Changed:** **Removing a workspace.**
  - Old: A workspace can be removed only when it has no sessions. Removing
    it asks first, and leaves the directory on disk.
  - New: A workspace can be removed only when it has no sessions, archived
    ones included; otherwise its menu shows Remove workspace unavailable,
    with the reason. Removing it asks first, and leaves the directory on
    disk.
- **Changed:** **Claude can message other sessions.**
  - Old: Claude can list the other sessions, with each one's label,
    workspace, settings, and status, and send any of them a message. It
    does so without asking for approval, except in plan mode, where each
    message asks first.
  - New: Claude can list the other sessions that are not archived, with
    each one's label, workspace, settings, and status, and send any of them
    a message. It does so without asking for approval, except in plan mode,
    where each message asks first.
- **Changed:** **Message delivery.** Only the fourth sentence changes.
  - Old: A message to a session whose Claude cannot be launched is refused,
    and the sending Claude gets the reason.
  - New: A message to an archived session, or to a session whose Claude
    cannot be launched, is refused, and the sending Claude gets the reason.
- **Changed:** **When settings can change.**
  - Old: Settings can change only while the session is idle or parked.
  - New: Settings can change only while the session is idle or parked, and
    not while it is archived.

## Documentation updates

- `docs/protocol.md`: `session/archive` and `session/unarchive` under
  Session methods; `session/archived` and `session/unarchived` in Workspace
  and session events; `archived` in `workspace/list`; the `session is
  archived` refusals in `session/send`, `session/configure`,
  `send_message`, and `create_session`'s refusal list; `list_sessions`
  leaving archived sessions out; `workspace/remove` counting archived
  sessions.
- `docs/architecture.md`: Sessions — `Server.mu` guards the archived flags
  with membership; an archive holds each family member's goroutine, parent
  first, while it checks and stores the flags, and an archived session has
  no agent; `startForInput` refuses an archived session. The `store` row
  names the `archived` table.
