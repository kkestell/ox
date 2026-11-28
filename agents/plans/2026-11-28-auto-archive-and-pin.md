# Archive idle sessions automatically, and pin sessions to keep them

## Goal

Sessions pile up until the user archives each by hand. `oxd` archives a
session family once every member has sat parked and unused for a configured
time, and the user pins the sessions that should stay. Resolves
[#195](https://github.com/kkestell/ox/issues/195).

## Related code

- `internal/config/config.go`, `internal/config/default.json`: the
  configuration and its startup checks; `max_child_depth` is the pattern for a
  required integer.
- `cmd/oxd/main.go`: builds the server; passes `parkAfter` from a flag.
- `internal/store/store.go`: the `archived` table and `SetArchived` are the
  pattern for `pinned`; `Workspaces` loads each session record.
- `internal/server/server.go`: `Server.mu` guards membership and archived
  flags; `New` loads sessions and starts their goroutines; `Close` ends them.
- `internal/server/archive.go`: `archive` holds the family, checks every member
  is free, and sets flags in `setArchivedLocked`; `unarchiveLocked`.
- `internal/server/session.go`: `update` publishes `state` under `Server.mu`;
  `adopt` applies setting changes.
- `internal/server/methods.go`: `sessionInfo`, `archiveSession`,
  `unarchiveSession`.
- `cmd/ox/web/src/ox.tsx`: `ListRequest`, `requestFor`, the `archived`
  projection, and the `session/archived` notification handling.
- `cmd/ox/web/src/Sidebar.tsx`: `rowsOf` (order), `SessionRow` (row classes),
  `sessionItems` (menu).
- `cmd/ox/web/src/style.css`: `nav li` rows; `--foreground` is darker than
  `--text` in light mode and brighter in dark mode.
- `internal/server/mcp_test.go` `TestArchive`: archive tests with families.

## Decisions

- **Configuration.** `auto_archive_minutes`, a required integer, global; 0
  turns auto-archive off. The field is a `*int` so a missing field is told
  from 0: missing is refused with `auto_archive_minutes is required`, and a
  negative value with `auto_archive_minutes must be 0 or more`.
  `default.json` sets 60. `Config.ArchiveAfter() time.Duration` converts it,
  and `cmd/oxd/main.go` passes that to `server.New` as `archiveAfter`, beside
  `parkAfter`. `server.New` treats 0 as off, as it does `parkAfter`, so tests
  that do not exercise auto-archive pass 0 and auto-archive tests pass a short
  duration.
- **What is archived.** Only whole families, from the top: `oxd` auto-archives
  a top-level session together with every session beneath it, and never a
  child alone. A child archived while its parent still works would refuse the
  parent's messages. A top-level session another session started is its own
  family.
- **When a family is idle** (`familyIdleLocked`, the one rule, used by both
  the sweep and the archive itself): the top-level session is neither archived
  nor pinned, no member is deleting, every member's published `state` is `parked`,
  and the latest `activeAt` among the members is at least `archiveAfter` ago.
  Requiring `parked` reuses the parking rule for "nothing pending": a session
  does not park while a prompt, setting change, approval, background task, or
  schedule is pending, so none of those is cut off by auto-archive. The
  effective idle time is therefore at least the park period.
- **Activity (`activeAt`).** Set to `time.Now()` by `update` for every update
  except `parked` (prompts, messages, reports, replies, tool calls, state
  changes), by `adopt` for a settings change, by `unarchiveLocked` for every
  session it unarchives, and by `session/unpin` for the unpinned session, so
  unarchiving or unpinning gives a session the full period. Viewing a session
  or typing in its composer does not reach `oxd` and does not count. At load,
  `activeAt` is the time of the session's last stored event that is not
  `parked`, or `created_at` when it has none, so restarts keep the clock;
  `recordEnd`'s `parked` row at startup is excluded by the same rule. A
  settings change, unarchive, or unpin before a restart is not stored, and the
  clock falls back to the last event; the cost is an earlier archive after a
  restart, accepted.
- **Sweep.** A `Server` goroutine started by `New` when `archiveAfter > 0`
  ticks every `min(archiveAfter/10, time.Minute)`. Each tick it collects, under
  `Server.mu`, the top-level sessions in workspace order whose family passes
  `familyIdleLocked`, then releases the lock and, one at a time, posts
  `archive(true)` to each and waits for that op to finish. `archive(true)` runs
  the existing hold walk, then, under `Server.mu` and in the same critical
  section as `setArchivedLocked`, checks `familyIdleLocked` again on the held
  members, so a pin, prompt, or unarchive that lands between the sweep and the
  archive wins. `agent.ErrBusy` and an idle check that now fails are dropped
  silently, since the next tick decides again; any other error is logged with
  the session's ID. The archive is announced as a manual one is:
  `session/archived` for each member, then `parked` for any running agent
  (none, since all are parked).
- **Pinning.** `session/pin` and `session/unpin`, each with `session_id`,
  mirroring `session/unarchive`: under `Server.mu` the method looks the session
  up, stores the flag, sets `pinned`, broadcasts `session/pinned` or
  `session/unpinned`, and replies, all in one critical section. Only a
  top-level session can be pinned: both methods refuse a child with `-32000`
  `only a top-level session can be pinned`, so a child's `pinned` is always
  false. Pinning an archived or pinned session succeeds; so does unpinning an
  unpinned one. A pinned session can be archived and deleted by hand as
  before. `workspace/list` carries `"pinned": true`; `session/created` never
  does.
- **Order.** The page lists pinned top-level sessions first, newest first,
  then the other top-level sessions, newest first. Children keep their order.
- **Row style.** A pinned row's title is `var(--font-semibold)` in
  `var(--foreground)`. No marker or label.
- **Page.** `ListRequest` gains `"pin"` and `"unpin"`; `requestFor` already
  maps a kind to `session/${kind}`. `pinned(session)` projects the request in
  flight over the stored flag, as `archived` does, so the row moves and turns
  bold as soon as the user chooses Pin and reverts with the error if `oxd`
  refuses. A top-level session's menu has Pin or Unpin as its first item,
  disabled with "Wait until Ox answers" while a list request is in flight for
  the session, and "Not connected" while not ready. A child's menu has
  neither.
- **Requirements.** Session order, Menus, Archiving a session, Archived
  sessions, Showing archived sessions, Every page in sync, Actions show at
  once, Every request settles, Surviving restarts, Parking, Configuration
  checks. A pin's reply lost or a reconnect: the page's request fails as every
  list request does and `workspace/list` restores `oxd`'s flag. Two pages
  pinning: each sees the other's `session/pinned`. An auto-archive of the open
  session shows the Unarchive button, as an archive from another page does,
  and its unsent text survives it.
- **Construction.** `pinned` and `activeAt` are guarded by `Server.mu`, beside
  `archived`, because the family rule reads them across sessions in one
  critical section. `pinned` is written only by the pin methods, `activeAt`
  only in the places above, each under the lock. `familyIdleLocked` is the
  single definition of an idle family.
- **Ownership.**
  - `pinned` (stored in a new `pinned` table, `session_id` primary key
    referencing `sessions` on delete cascade): needed for Pin. Races: a pin
    against an auto-archive is settled by the re-check under the lock; against
    a delete, the cascade removes the row. Cleanup: the cascade.
  - `activeAt` (memory only, seeded from events): needed to time the idle
    period. Races: an update landing after the sweep's check is caught by the
    re-check while the family is held. Cleanup: none.
  - The sweep goroutine and its ticker: needed to archive without a request.
    Races: covered above; `setArchivedLocked` refuses once `closed`. Cleanup:
    `Close` closes a `quit` channel before ending sessions and waits for the
    sweep to return.
- **Trust boundaries.** Unchanged.

## Check your assumptions

1. **Whole families are archived together, from the top.** Kept.
   - **Plan:** Ox archives a top-level session and everything beneath it
     together, once all of them have sat parked and unused for the configured
     time. A child is never archived on its own while its parent works, and a
     pinned top-level session keeps its whole family.
   - **Alternative:** Each session times out on its own, so a finished child
     is archived under a parent that is still working. The parent's later
     messages to that child are then refused until the user unarchives it.
2. **What counts as use.** Kept: prompts, messages, reports, the agent's
   replies and tool calls, setting changes, unarchiving, and unpinning.
   - **Plan:** Looking at a session, or typing in its composer without
     sending, does not count; a session left open on screen is still
     archived, and its unsent text comes back when it is unarchived. A session
     with background work, a schedule, or a waiting approval is never archived
     while it is pending, since it does not park.
   - **Alternative:** Viewing counts. Pages would have to report what they
     show, and a session left open in a forgotten tab would never be archived.
3. **Turning auto-archive off.** Changed: the field stays required, and 0
   turns auto-archive off; a negative value is refused.
4. **Pinning child sessions.** Changed: only top-level sessions can be
   pinned, and pinned top-level sessions list first.
5. **Requirement text.** Changed to the text under Requirement changes below.

## Test plan

- `internal/config/config_test.go`: add `"auto_archive_minutes":60` to every
  valid configuration string; add table cases for the field missing (refused
  with `auto_archive_minutes is required`), -1 (refused with
  `auto_archive_minutes must be 0 or more`), and 0 (loads, `ArchiveAfter()`
  is 0); assert the built-in configuration loads with an `ArchiveAfter()` of
  an hour in the existing built-in case.
- `internal/store/store_test.go`, new `TestPinnedAndActiveAt` (no existing test
  covers either column): `SetPinned` true then false round-trips through
  `Workspaces` and a reopen; `ActiveAt` is the time of the last event that is
  not `parked`, and `CreatedAt` for a session with no events.
- `internal/server/mcp_test.go`, new `TestAutoArchive` beside `TestArchive`,
  with a server built by `New` with a short `parkAfter` (10ms) and
  `archiveAfter` (300ms), the fake harness, and the family helpers
  `TestArchive` uses:
  - Create top-level A, pin it (`session/pinned` arrives, `workspace/list`
    shows `"pinned": true`); create B, send it a prompt and wait for its turn
    to end at time t. B's `session/archived` arrives no sooner than t +
    `archiveAfter`, and no `session/archived` for A precedes it (A is first in
    workspace order and idle longer, so an unpinned A would be archived
    first).
  - Unpin A at t1: A is archived no sooner than t1 + `archiveAfter`.
  - Unarchive B at t2: B is archived again no sooner than t2 +
    `archiveAfter`.
  - A parent with a child that sent the last message at t3: both are archived
    together, no sooner than t3 + `archiveAfter`, the parent's and child's
    `session/archived` arriving in one archive.
  - `session/pin` and `session/unpin` on the child return `-32000` `only a
    top-level session can be pinned`, and no `session/pinned` follows.
  - Restart: close the server and start another on the same store with
    `archiveAfter` 0; A's pin is listed.
- `cmd/ox/web/e2e/session.spec.ts`, new test "a pinned session lists first in
  bold through a reload": create two sessions, pin the older from its context
  menu, see it move above the newer with a semibold title in the
  `--foreground` color; reload and see the same; Unpin from the `...` menu
  returns it to creation order. With the parent and child helpers the archive
  tests use, a child's menu has no Pin item.
- `make test`, `make e2e`, and a headless Chrome check through `make dev`
  (Pin and Unpin from both menus, in light and dark mode). `make live` cannot
  observe the change.

## Implementation plan

1. `internal/config/config.go`: add `AutoArchiveMinutes *int
   json:"auto_archive_minutes"` with a comment, the required and nonnegative
   checks after `max_child_depth`'s, and `ArchiveAfter() time.Duration`.
   `internal/config/default.json`: add `"auto_archive_minutes": 60` after
   `max_child_depth`. Update `config_test.go` as above.
2. `cmd/oxd/main.go`: pass `cfg.ArchiveAfter()` through `run` to
   `server.New`. Add the `archiveAfter
   time.Duration` parameter after `parkAfter` to `server.New` and pass 0 at
   every other caller: `client_test.go` `newServer`, `mcp_test.go`,
   `methods_test.go`, `worktree_test.go`, `lifecycle_test.go`,
   `cmd/ox-mcp/main_test.go`.
3. `internal/store/store.go`: add the `pinned` table to the schema;
   `SetPinned(id string, pinned bool) error`; in `Workspaces`, select
   `p.session_id IS NOT NULL` into a new `Session.Pinned` and the last
   non-`parked` event's `created_at` (a correlated subquery ordered by
   `events.id DESC LIMIT 1`, using `json_extract(data, '$.type')` as
   `ToolNames` does) into a new `Session.ActiveAt`, falling back to
   `CreatedAt`. Add `TestPinnedAndActiveAt`.
4. `internal/server/server.go`, `session.go`: add `pinned bool` and `activeAt
   time.Time` to `session` under the "Guarded by Server.mu" block, set from
   the record in `newSession`. In `update`, set `sess.activeAt = time.Now()`
   under the lock it already holds for every update but `parked`. In `adopt`,
   set it under `Server.mu` when the settings change.
5. `internal/server/archive.go`: add `familyIdleLocked(members []*session)
   bool` as described; change `archive()` to `archive(auto bool)`, checking
   `familyIdleLocked` under `Server.mu` before `setArchivedLocked` when `auto`
   (split `setArchived` so the check and the write share the lock). In
   `unarchiveLocked`, set each member's `activeAt` to now. Add the sweep:
   `familyLocked(top)` (the breadth-first walk over `children`), and
   `autoArchive()`, the ticker loop described above. Start it in `New` when
   `archiveAfter > 0`; in `Close`, close `quit` first and wait for it.
6. `internal/server/methods.go`: `archiveSession` calls `archive(false)`. Add
   `Pinned bool json:"pinned,omitempty"` to `sessionInfo` and `infoLocked`.
   Add `session/pin` and `session/unpin`, dispatched to one `pinSession(reply,
   id, params, pinned bool)` that works under `Server.mu` as
   `unarchiveSession` does, refusing a child (`sess.child != nil`), storing
   with `SetPinned` (a store error is
   returned and logged with the session's ID, and the flag is unchanged),
   setting `pinned`, setting `activeAt` on unpin, broadcasting
   `session/pinned` or `session/unpinned` with `session_id`, and replying.
7. `internal/server/mcp_test.go`: add `TestAutoArchive`.
8. `cmd/ox/web/src/protocol.ts`: `pinned?: boolean` on listed sessions; the
   `session/pin` and `session/unpin` methods; the `session/pinned` and
   `session/unpinned` notifications.
9. `cmd/ox/web/src/ox.tsx`: `pinned: boolean` on `StoredSession`, omitted from
   `Session` with `archived`; read it in the list load (default false) and set
   false in `session/created`; handle `session/pinned` and `session/unpinned`
   like `session/archived`; add `"pin" | "unpin"` to `ListRequest`; add the
   `pinned(session)` projection (a `"pin"` or `"unpin"` request in flight for
   the session itself, else the stored flag); expose `pinned`, `pin`, and
   `unpin`.
10. `cmd/ox/web/src/Sidebar.tsx`: `rowsOf` takes a `pinned` predicate and
    orders the top-level list pinned-first, stable, after its newest-first
    order; update its comment. `SessionRow` adds the `pinned` class.
    `sessionItems` puts Pin or Unpin first for a session without `child`. `cmd/ox/web/src/style.css`: `nav li.pinned
    .title { font-weight: var(--font-semibold); color: var(--foreground); }`.
11. `cmd/ox/web/e2e/services.ts`: add `auto_archive_minutes: 60` to
    `fakeConfig` and `haikuConfig`; `session.spec.ts`: add it to the Ur
    configuration near line 1583, and add the pin test.
12. Documentation and requirements, as below. Run `make docs`.

The user's own `~/.config/ox/config.json`, and any `make dev` state's
`config.json` in a worktree stack, need `"auto_archive_minutes"` before `oxd`
starts on this change; the user updates their own file. On the first start, every
unpinned family whose last use is older than the period is archived within a
tick.

## Requirement changes

- **Changed: Session order.**
  - Old: Each workspace lists its top-level sessions newest first, each
    followed by its child sessions, oldest first and indented beneath it; a
    child's own children follow it the same way, indented one level further.
  - New: Each workspace lists its pinned top-level sessions first and then
    the others, each group newest first, each session followed by its child
    sessions, oldest first and indented beneath it; a child's own children
    follow it the same way, indented one level further.
- **Added: Pinning sessions** (Session list, after Session status): The user
  pins or unpins a top-level session from its menu. A pinned session's label
  shows in bold, in a stronger color than the others', on every page and
  through `oxd` restarts.
- **Added: Archiving idle sessions** (Archiving and deleting, after Archiving
  a session): Ox archives a top-level session, with every session beneath it,
  once all of them are parked and none has been used for the time the
  configuration sets, an hour in the configuration Ox writes, unless it is
  pinned. Prompts, messages, replies, tool calls, setting changes,
  unarchiving, and unpinning count as use; viewing a session does not. The
  configuration can turn this off. A pinned session can still be archived by
  hand.

## Documentation updates

- `docs/configuration.md`: the required `auto_archive_minutes`, 0 or more,
  where 0 turns auto-archive off, global, 60 in the built-in configuration;
  add it to the example.
- `docs/protocol.md`: `"pinned": true` in `workspace/list`; the
  `session/pin` and `session/unpin` methods, which refuse a child; the
  `session/pinned` and
  `session/unpinned` events; in `session/archive`, that `oxd` also archives
  idle families itself, announced the same way.
- `docs/architecture.md`: the `pinned` table in the store row; `pinned` and
  `activeAt` in the `Server.mu` row; an "Auto-archive" paragraph under
  Archiving describing the sweep, the family rule, and the re-check under the
  hold; Pin and Unpin in the page's list actions.
