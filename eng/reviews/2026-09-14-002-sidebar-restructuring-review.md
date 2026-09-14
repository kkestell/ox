# Sidebar restructuring of the browser client — completion review

## Scope and mode

General, change-directed review of the completed top-level item
`Restructure the browser client around a workspace and conversation sidebar` and
its three tasks, implemented by
`eng/plans/2026-09-14-012-cross-workspace-conversations.md`,
`eng/plans/2026-09-14-013-sidebar-navigation.md`, and
`eng/plans/2026-09-14-014-workspace-settings.md`.

Corpus: the working-tree change to `client/src/browser.tsx`,
`client/src/host.ts`, `client/src/protocol.ts`,
`client/src/workspace-supervisor.ts`, their unit tests,
`client/e2e/smoke.spec.ts`, `client/public/index.html`,
`client/public/styles.css`, `eng/mockups/`, `docs/spec.md`,
`eng/client-architecture.md`, and `eng/todo.md`.

### Selected topics and why

- **correctness** — the conversation list moved from the session projection to
  every workspace's catalog entry, and the browser gained a second
  primary-column view whose content is published for the selected workspace
  only.
- **architecture** — three components collapsed into one sidebar, a settings
  surface replaced two nested disclosures, and `select-workspace` changed from
  the browser's selection control to a navigation side effect.
- **api-design** — `protocol.ts` moved `sessions.values` onto
  `workspaces.values[].conversations` and split `SessionState` into
  `SessionSelection` plus a supervisor-private list.
- **security** — the credential input and the MCP draft are browser-held secrets
  whose target workspace can change without user action.
- **testing** — this is the completion gate for a top-level item, and the client
  is proved almost entirely through Playwright.
- **documentation** — `docs/spec.md`, `docs/browser.md`, and
  `eng/client-architecture.md` all own parts of the changed surface.

Not selected: **unsafe**, **concurrency**, **resources**, **performance**, and
**dependencies** — the change adds no process, timer, lock, or module, and
snapshot assembly gained one bounded slice per unselected workspace.

## Findings

### 1. The sidebar lists conversations a workspace with no process cannot open

`client/src/browser.tsx:343`

A workspace entry renders its status paragraph and its conversation list as
independent siblings, so a workspace whose Ox exited shows
`Ox is not running. [Restart]` _and_ its conversations. The supervisor marks
those conversations `inactive` rather than dropping them, so the list survives
the process. Following one sends `open-conversation` to a supervisor the host
has already discarded, and the browser reports `workspace is not active`.

`eng/client-architecture.md` states that a workspace with no usable process
"offers its restart there instead of its conversations", which is also what
`eng/plans/2026-09-14-013-sidebar-navigation.md` settled. The implementation
does both.

Fix: render the conversation list and its older page only while the workspace is
`ready`, and the problem with its restart otherwise.

### 2. A failed session command outlives the workspace it failed in

`client/src/browser.tsx:136`

`sessionError` is cleared only when a later session command succeeds. When Ox
exits during a turn, the in-flight prompt rejects and its alert stays in the
primary column through an explicit restart, beside a working conversation, and
follows the browser to another workspace whose conversation it never described.
Observed in this session: stopping Ox mid-turn leaves both
`Ox is unavailable. Open workspace settings for diagnostics.` and
`ACP connection closed` in `main`, and nothing in the restart path clears the
second.

Fix: clear it when the selected workspace changes and when the user restarts a
workspace, which are the two points where the failure stops describing what the
browser is showing.

### 3. `docs/browser.md` describes the surface the sidebar replaced

`docs/browser.md:27` and `docs/browser.md:36`

The guide tells the user that "selecting a workspace changes what the browser
shows" and that diagnostics "stay visible in support details". There is no
workspace-selection control any more — opening a conversation or a workspace's
settings is what selects it — and support details are inside workspace settings.
`docs/spec.md` and `eng/client-architecture.md` were updated for both; the
end-user guide was not.

Fix: name the actions that exist and say where support details live.

## Observations

- `client/public/styles.css` sets `height: 100vh` on the layout. On mobile
  browsers that is taller than the visible viewport. The responsive visual
  design work owns this, so it is not a finding here.
- `select-workspace` survived the sidebar's removal of the workspace picker and
  now has exactly one browser caller, the settings link. It is not dead code.
- Secret handling is sound: the MCP draft and the credential input are cleared
  when the selected workspace changes, so neither is submitted to a workspace it
  was not typed for, and the Playwright suite still asserts that no MCP secret
  reaches `main` or the provider requests.

## Resolution

All three findings are fixed in the same change: the sidebar renders a
workspace's conversations only while it is `ready`, a session failure is cleared
when the selection changes and when the user restarts a workspace, and
`docs/browser.md` names the sidebar, the settings surface, and where diagnostics
live. `client/e2e/smoke.spec.ts` gained the two regression assertions in the
restart scenario.

## Checks run

- `make check` — clean.
- `make check-all` — clean, including the race detector, `internal/e2e`, and
  `integration`.
- `cd client && bunx playwright test` — 17 passed, including the new
  settings-surface, cross-workspace settings, unauthenticated-workspace, and
  MCP-draft-across-navigation scenarios.
