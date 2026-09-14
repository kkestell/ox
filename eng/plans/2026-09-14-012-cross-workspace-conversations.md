# Cross-workspace conversation lists

## Goal

Every registered workspace carries its own bounded recent-conversation list in
the host snapshot, and opening or creating a conversation in a workspace the
browser is not showing switches to that workspace in one command. Today a
workspace entry carries only `id`, `name`, `status`, `busy`, and `awaiting`, so
the only conversation list a browser can see belongs to the selected workspace.

## Related code

- `client/src/protocol.ts:405` — `snapshotSchema`, where `workspaces.values`
  entries and the selected workspace's `sessions` block are defined.
- `client/src/host.ts:275` — `browserWorkspaces` and `browserSessions` build the
  catalog and the selected workspace's session block from supervisor state.
- `client/src/host.ts:224` — the command dispatch that routes a workspace-named
  command either through `performWorkspace` or straight to a supervisor.
- `client/src/host.ts:67` — `publish` rebuilds only the catalog when an
  unselected workspace publishes, which is what keeps the new list cheap.
- `client/src/workspace-supervisor.ts:45` — `WorkspaceCatalog` and its `catalog`
  getter, the projection that deliberately avoids building a transcript.
- `client/src/workspace-supervisor.ts:145` — the `state` getter, which currently
  derives `sessions.values` with live `awaiting` from the controllers.
- `client/src/browser.tsx:369` — `History`, the only consumer of
  `snapshot.sessions.values`.

## Decisions

The conversation list moves out of `snapshot.sessions` and onto each workspace
catalog entry, so the list has one home rather than a catalog copy beside a
selected-workspace copy. `snapshot.sessions` keeps `active`, `selectedId`, and
`nextCursor`. `WorkspaceSupervisor.state.sessions` drops `values` for the same
reason; `catalog.conversations` becomes the supervisor's public conversation
projection, and the supervisor keeps its own `#state.sessions.values`
internally.

Paging belongs to the workspace the user is browsing. An unselected workspace's
entry carries at most `maximumRecentConversations` (10) conversations and the
selected workspace's entry carries its complete paged list up to
`maximumSessions`. Selecting a workspace is therefore what expands its list, and
`nextCursor` stays on `snapshot.sessions` because `next-history-page` is only
meaningful for the workspace whose full list is shown. The host applies the
truncation, because only the host knows which workspace is selected.

Truncation keeps every conversation the host holds active. Ox orders
`session/list` by recency, but the browser client re-lists only on lifecycle
changes, so a conversation that started waiting since the last listing can sit
below the recent window. Taking the first `maximumRecentConversations` entries
and then appending any later entry whose status is not `inactive` preserves the
existing invariant that a waiting conversation is always nameable.

`open-conversation` and `new-conversation` select the workspace they name, and
only after the supervisor operation succeeds. A failed load therefore leaves the
browser on the workspace it was showing, and a slow load is visible as that
conversation's `loading` status in its own workspace's list. `select-workspace`
stays, because a user can still want a workspace's settings and full history
without opening a conversation.

## Test plan

- `client/src/protocol.test.ts` — a workspace entry accepts a bounded
  conversation list including a waiting conversation, rejects a malformed
  conversation entry, and `sessions` no longer accepts `values`.
- `client/src/workspace-supervisor.test.ts` — `catalog.conversations` carries
  the summaries with live `awaiting` that `state.sessions.values` carried, and
  still bounds itself at `maximumSessions` with no `nextCursor`. Port the
  existing assertions rather than duplicating them.
- `client/src/host.test.ts` — with a scripted ACP program supplying more than
  `maximumRecentConversations` sessions in two workspaces: every registered
  workspace's entry carries conversations without being selected; the unselected
  entry is truncated to the bound while the selected entry carries the full
  list; `open-conversation` naming the unselected workspace makes it selected
  and its conversation the active one; a refused open leaves the selection
  unchanged.

## Implementation plan

- Add `maximumRecentConversations` and a conversation entry schema to
  `client/src/protocol.ts`, move it onto `workspaces.values` entries bounded at
  `maximumSessions`, remove `values` from the `sessions` block, and update
  `initialSnapshot`.
- Replace `SessionState.values` on the supervisor's public `state` with
  `conversations` on `WorkspaceCatalog`, keeping the live `awaiting` derivation
  and the internal `#state.sessions.values` source.
- Build each catalog entry's conversations in `client/src/host.ts`, truncating
  every unselected workspace to the recent bound while retaining non-inactive
  conversations, and drop `values` from `browserSessions`.
- Select the named workspace in `client/src/host.ts` after a successful
  `open-conversation` or `new-conversation`, reusing the serialized workspace
  path so selection cannot interleave with another workspace operation.
- Port `History` in `client/src/browser.tsx` to read the selected workspace
  entry's conversations. The sidebar that renders other workspaces' lists is not
  part of this change, so no other browser surface moves.

## Documentation updates

- `eng/client-architecture.md` — record that the workspace catalog owns each
  workspace's bounded recent conversations, that only the selected workspace
  pages, and that opening or creating a conversation selects its workspace.
- `docs/spec.md` — the web client shows recent conversations for every
  registered workspace, and choosing one switches to its workspace.
- `eng/todo.md` — check off the recent-conversation list task.
