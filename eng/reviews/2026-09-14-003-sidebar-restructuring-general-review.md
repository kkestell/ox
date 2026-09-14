# Sidebar restructuring of the browser client — general review

## Scope and mode

General, change-directed review of the completed top-level item
`Restructure the browser client around a workspace and conversation sidebar`,
implemented by `eng/plans/2026-09-14-012-cross-workspace-conversations.md`,
`eng/plans/2026-09-14-013-sidebar-navigation.md`, and
`eng/plans/2026-09-14-014-workspace-settings.md`. This is a second pass over the
same milestone: the completion review
(`eng/reviews/2026-09-14-002-sidebar-restructuring-review.md`) found three
defects whose fixes are part of this working tree, and those fixes were
re-verified here rather than trusted.

Corpus: the working-tree change to `client/src/browser.tsx`,
`client/src/host.ts`, `client/src/protocol.ts`,
`client/src/workspace-supervisor.ts`, their unit tests,
`client/e2e/smoke.spec.ts`, `client/public/index.html`,
`client/public/styles.css`, `eng/mockups/`, `docs/browser.md`, `docs/spec.md`,
`eng/client-architecture.md`, and `eng/todo.md`, plus the three plans and the
completion review they require as artifacts.

### Selected topics and why

- **correctness** — the conversation list moved from the selected workspace's
  session block onto every workspace's catalog entry with a recent-window
  truncation that must still keep waiting conversations nameable, and the
  primary column became a two-view surface whose view state lives in the browser
  while its content stays host-owned.
- **architecture** — three components collapsed into one navigation region, a
  settings view replaced the primary-column authentication block and a nested
  disclosure, and the host gained a select-after-open side effect on its
  serialized workspace path.
- **api-design** — the protocol moved `sessions.values` onto
  `workspaces.values[].conversations`, added `maximumRecentConversations` and
  the `Conversation` type, and the supervisor split `SessionState` into the
  published `SessionSelection` and a private list.
- **security** — the credential input and the MCP draft are browser-held secrets
  whose target workspace can now change without user action.
- **testing** — this is the completion gate for a top-level item, and the client
  is proved almost entirely through the Playwright matrix.
- **documentation** — `docs/spec.md`, `docs/browser.md`, and
  `eng/client-architecture.md` all own parts of the changed surface.
- **error-handling** — the session-failure lifecycle was the subject of two of
  the completion review's three findings and is core to this change.
- **performance** — the change touches the publish path the previous milestone
  just made cheap, so the cost claim was re-checked rather than inherited.

Not selected: **resources** — the change adds no process, file, timer, or
listener, and socket teardown is unchanged; **concurrency** — no locks or
thread-shared state exist, and the new asynchronous chaining is examined under
correctness and error-handling; **unsafe** — no `unsafe`, `reflect`, or cgo
exists in the client; **dependencies** — `package.json` and imports are
unchanged.

## Findings

### 1. Session failures name no workspace and can render against the wrong one (low)

`client/src/browser.tsx:245`

The browser stores session results only by request id
(`client/src/browser.tsx:139`), never keyed to the workspace a command named,
and renders `sessionError` as a bare alert at the top of the primary column
whatever conversation or settings it is displaying. Two traces follow. First, a
cross-workspace open or create that fails ("unknown session", "conversation
cannot be opened") renders above the transcript of the workspace it did not fail
in, with nothing naming the workspace that failed. Second, a slow failure whose
result arrives after the selection has moved renders there too: the reset effect
(`client/src/browser.tsx:111`) clears errors when the selection changes, but a
late result for the old workspace sets a new one. The user can read the failure
as describing the conversation they are looking at.

Suggested fix: record the workspace the command named — `submitSessionCommand`
already receives it — and either include that name in the alert or drop results
whose workspace no longer matches the selection. The selection-change half of
the completion-review fix also has no direct Playwright assertion (the restart
half does), so the same scenario could carry one.

## Observations

- `client/src/host.ts:245` enqueues the select after an open completes, so two
  racing cross-workspace opens, or an open racing a settings link, can leave the
  selection on the workspace whose operation finished last rather than the one
  the user clicked last. The window is small, the user is one, and the next
  action corrects it; the settled contract ("only once Ox has accepted it")
  holds. A host test that delays one of two opens would settle whether the
  ordering matters enough to change.
- `showSettings` sends `select-workspace` without registering its request id, so
  a host rejection — another browser removing the workspace between render and
  click — is silent, and the view shows the previously selected workspace's
  settings. The settled plan accepts exactly this ("shows the workspace it is
  actually showing"), and the view's heading names the workspace.
- Settings for a workspace whose process is down show the dead process's last
  authentication status and MCP count, because supervisor state survives the
  process. The unavailable alert above the view and the Ox processes list
  disambiguate.
- `authenticate`, `login`, and `logout` discard `sendRouted`'s send-failure
  string, so a click while the socket is closed does nothing silently. This
  pattern predates the change; the move into settings did not alter it.

## Topic results

- **correctness** — finding 1. Everything else checked out: the truncation keeps
  every non-inactive conversation nameable, the sidebar lists conversations only
  for a `ready` workspace, a refused open leaves the selection untouched, and
  the view model derives from `workspaces.selectedId` as the plans settle.
- **architecture** — nothing to report. The rendered structure matches both
  settled mockups; the catalog/selection split gives the conversation list one
  home; the select-after-open runs on the serialized workspace path so it cannot
  interleave with a registry operation.
- **api-design** — nothing to report. `maximumRecentConversations` is named and
  documented, `SessionState` became supervisor-private while the published type
  dropped the list, and `select-workspace` keeps the one caller it has.
- **security** — nothing to report. The credential clears on submit and on
  selection change, the MCP draft and its message reset when the selection
  changes, and the Playwright suite still proves no MCP secret reaches `main` or
  the provider requests.
- **testing** — the gap noted in finding 1. Otherwise the port is faithful:
  protocol rejections, catalog assertions, truncation with retention, the
  refused-open selection invariant, the sidebar layout box, settings navigation,
  and restart for a workspace that lists no conversations.
- **documentation** — nothing to report. The three documents describe the
  implemented surface, including the replaced pre-CSS invariant, which
  `client/public/styles.css` honors.
- **error-handling** — finding 1 aside, error paths are consistent: workspace
  commands report through the workspace message, session commands through their
  results, and host rejections keep human-readable messages such as "workspace
  is not registered".
- **performance** — nothing to report. The list moved rather than grew: the
  selected entry carries what `sessions.values` carried, and unselected entries
  add a bounded ten-plus-active slice. Publishes still stringify the whole
  snapshot per streamed chunk, which predates this change.

## Checks run

- `cd client && bun run check` — clean.
- `cd client && bun run test` — 93 passed.
- `make check-docs` — clean.
- `cd client && bun run test:e2e` — 17 passed.
- Greps for the removed surfaces (`Current workspace` select, the "Workspaces
  waiting for an answer" list, `sessions.values` consumers) — no stale
  references remain in source or tests.
- The completion review's three fixes re-verified: conversations render only
  while a workspace is `ready` (asserted after a stop), `sessionError` clears on
  restart (asserted) and on selection change (implemented; unasserted), and
  `docs/browser.md` names the sidebar, the settings surface, and where
  diagnostics live.
