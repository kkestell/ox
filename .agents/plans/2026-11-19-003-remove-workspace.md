# Remove a workspace

## Goal

From [`docs/todo.md`](../../docs/todo.md) (Sessions and workspaces): "Remove a
workspace. Needs a new method and a store delete; neither exists. Decide what
happens to its sessions (destroy them, or refuse while any exist)."

When done, a workspace with no sessions can be removed from the sidebar. `oxd`
serves `workspace/remove`, which refuses a workspace that still has sessions,
deletes the workspace's row, and tells every connection with a
`workspace/removed` event. The workspace's directory is untouched. Adding the
same path again registers it as a new workspace with a new ID.

## Related code

- `internal/server/methods.go:104` — `createWorkspace`: parses `path`, then
  under `s.mu` checks `s.closed`, writes the store, appends to `s.workspaces`,
  broadcasts `workspace/created`, and replies. `removeWorkspace` mirrors it.
- `internal/server/methods.go:174-250` — `createSession` looks up the
  workspace and appends the new session to `w.sessions` under `s.mu`, the same
  lock removal holds, so a session can never be added to a removed workspace.
- `internal/server/methods.go:418` — `destroySession` removes the session from
  `w.sessions` under `s.mu` only after its process has stopped and its row is
  deleted.
- `internal/server/server.go:18-42` — `Server.mu` guards `workspaces`,
  `sessions`, and `closed`; `workspace(id)` looks one up.
- `internal/store/store.go:133-155` — `AddWorkspace` and `DeleteSession`; the
  schema's `sessions.workspace_id` has `ON DELETE CASCADE`.
- `cmd/ox/web/src/protocol.ts:36-80` — `Notification` and `Methods`.
- `cmd/ox/web/src/ox.tsx:333-380` — `reload` reconciles `state.workspaces`
  from `workspace/list` (so a workspace removed while disconnected drops out
  on reconnect); `receive` handles `workspace/created` and `session/destroyed`.
- `cmd/ox/web/src/ox.tsx:400-410` — the provider's `addWorkspace` and `destroy`.
- `cmd/ox/web/src/Sidebar.tsx` — the workspace row (`<p>` with the New session
  button), `sessionsOf`, and the delete-session `<dialog>` with its
  `createAttempt`.
- `cmd/ox/web/src/style.css:126-140` — `nav p button.icon` is absolutely
  positioned at the row's right edge and shown on hover or focus.
- `cmd/ox/web/src/App.tsx:30-90` — `Layout` keeps one draft per workspace;
  `draft()` and the failed-draft alerts read `drafts()`.
- `internal/server/methods_test.go:115` — `TestSessionLifecycle` creates a
  workspace and a session and ends by destroying the session.
- `cmd/ox/web/e2e/workspace.spec.ts`, `cmd/ox/web/e2e/ui.ts` — the workspace
  spec, `open`, and `hold` (which can fail a held request as a relay error).

## Decisions

- **Sessions:** removal is refused while the workspace has any session. The
  user deletes its sessions first.
- **Method:** `workspace/remove` with required `workspace_id`; result `{}`.
  - Missing or empty `workspace_id`: `-32602`, `workspace_id is required`.
  - Shutting down: `-32000`, `daemon is shutting down` (`shuttingDown`), as
    `createWorkspace` does.
  - Unknown ID, including one already removed: `-32001`, `workspace not found`.
  - Sessions present: `-32000`, `workspace has sessions`.
  - Store failure: `-32000` with the store's error; the workspace stays.
- **Event:** `workspace/removed` with `workspace_id`, broadcast before the
  reply, under `s.mu`, after the row is deleted and the workspace is dropped
  from `s.workspaces`.
- **Store:** `DeleteWorkspace(id string) error` runs
  `DELETE FROM workspaces WHERE id = ?`. The cascade never removes a session,
  because the server deletes only an empty workspace.
- **Page:**
  - The workspace row shows a "Remove workspace" trash button only while the
    page has no sessions loaded for it (`sessionsOf(workspace.id)` is empty).
    A session the page has not loaded yet is caught by `oxd`'s refusal, shown
    in the dialog.
  - The button opens a `<dialog role="alertdialog">` titled "Remove
    workspace?" with the description `This removes "{path}" from Ox. The
    directory stays on disk.`, Cancel, and a plain submit button "Remove". Its
    request runs through its own `createAttempt`; the error shows in the
    dialog, and the dialog closes on success.
  - The page drops the workspace only on the `workspace/removed` event, as it
    drops a session only on `session/destroyed`.
  - `Layout` reads drafts through one derived list,
    `drafts().filter((d) => ox.state.workspaces.some((w) => w.id === d.workspaceId))`,
    used by both `draft()` and the failed-draft alerts. A removed workspace's
    draft and its unsent text disappear with it, and a selected draft of a
    removed workspace leaves the main view empty.
- **Guarantees:**
  - 2: a draft's unsent text belongs to its workspace and goes when the
    workspace is removed. `docs/guarantees.md` says so.
  - 4: `workspace/remove` answers at once from under `s.mu`; every path above
    replies. A lost reply is followed on reconnect by `workspace/list`, which
    no longer lists the workspace if it was removed. A second page acting on
    the same workspace sees `workspace/removed`, and its own later remove gets
    `workspace not found`. A `session/create` racing a removal is ordered by
    `s.mu`: either the session lands first and the removal is refused, or the
    removal lands first and the creation gets `workspace not found`. Both are
    valid outcomes.
  - 5: the removal's error shows in its dialog.
  - 10: `oxd` deletes only its own database row; the workspace's directory and
    any Claude transcripts are untouched.
- **Ownership:** no new state, timer, lock, or stored field. Workspace
  membership stays owned by `s.mu`, with the change and its broadcast made
  under it, like `workspace/create`.
- **Trust boundaries:** `workspace/remove` is reachable by whatever already
  reaches the socket and the relay's `/rpc`; it deletes one row of `oxd`'s
  database and nothing on disk.

## Check your assumptions

1. **Removing a workspace with sessions is refused.** Kept.
   - **Plan:** The remove button appears only on a workspace with no sessions,
     so the user deletes a workspace's sessions one by one before removing it.
   - **Alternative:** Removing a workspace deletes all its sessions and their
     history in one confirmed step. It costs a removing state for the
     workspace that turns away new sessions while each session's process is
     stopped (up to ten seconds each), and an error path for a removal that
     stops partway with some sessions already gone.

## Test plan

- Server, extend `TestSessionLifecycle` (`internal/server/methods_test.go`),
  which already has a workspace, a session, and its destroy. Keep the store in
  a variable (`st := openStore(t)`).
  - Before the destroy: `workspace/remove` fails with `-32000` and message
    `workspace has sessions`, and `workspace/list` still lists the workspace.
  - After `session/destroyed`: `workspace/remove` succeeds; the next
    `workspace/removed` notification carries the workspace's ID;
    `workspace/list` returns no workspaces; a second `workspace/remove` fails
    with `notFound`; `session/create` in the removed workspace fails with
    `notFound`.
  - `New(st, …)` over the same store loads no workspace (reach it with a fresh
    client calling `workspace/list`), which fails without the store delete.
- e2e, a new test in `cmd/ox/web/e2e/workspace.spec.ts`, "a workspace is
  removed once it has no sessions", since no existing test has a workspace to
  remove (`startServices(fakeConfig)` and `open`):
  - Create a session through New session and a send; the workspace row has no
    "Remove workspace" button.
  - Delete the session; the button appears.
  - Open New session for the workspace and type text, so a draft is selected.
  - Click "Remove workspace"; `hold(page, "workspace/remove")` and `fail` it;
    the dialog shows the error and the workspace stays.
  - Click Remove again without the hold; the dialog closes, the workspace row
    is gone, and the Prompt textbox is gone (the draft went with it).
- The refusal racing a session the page has not loaded is a server rule,
  covered by the server test.

## Implementation plan

1. `internal/store/store.go`: add `DeleteWorkspace` beside `DeleteSession`.
2. `internal/server/methods.go`: route `workspace/remove` in `handle`; add
   `removeWorkspace` after `listWorkspaces`, per Decisions, holding `s.mu`
   for the check, the store delete, `slices.DeleteFunc` on `s.workspaces`,
   the broadcast, and the reply.
3. `internal/server/methods_test.go`: the server cases above.
4. `cmd/ox/web/src/protocol.ts`: add `"workspace/remove": [{ workspace_id: string }, Empty]`
   and the `workspace/removed` notification `{ workspace_id: string }`.
5. `cmd/ox/web/src/ox.tsx`: handle `workspace/removed` in `receive` by
   removing the workspace from `s.workspaces`; add
   `removeWorkspace: (id: string) => connection.rpc("workspace/remove", { workspace_id: id })`
   to the provider.
6. `cmd/ox/web/src/Sidebar.tsx`: the trash button in the workspace row,
   `aria-label` and `title` "Remove workspace", and its dialog and
   `createAttempt`, per Decisions.
7. `cmd/ox/web/src/style.css`: place the workspace row's trash button left of
   the New session button (`nav p button.icon.trash { right: 1.625rem; }`).
8. `cmd/ox/web/src/App.tsx`: the derived draft list per Decisions.
9. `cmd/ox/web/e2e/workspace.spec.ts`: the e2e test above.
10. Documentation below.

## Documentation updates

- `docs/protocol.md`: a `### workspace/remove` section after
  `workspace/list`, with its refusal while sessions exist and an example; a
  `workspace/removed` row (`workspace_id`, "A workspace was removed.") in the
  workspace and session events table.
- `docs/guarantees.md`, guarantee 2: unsent draft text is also discarded when
  its workspace is removed, e.g. "Reloading or closing the page discards
  unsent text, and a new session's text before it is submitted belongs to its
  workspace draft, which goes when the workspace is removed."
- `docs/todo.md`: delete the "Remove a workspace" item and its now-empty
  "Sessions and workspaces" section.
