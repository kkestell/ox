# Multi-workspace restart and interaction routing

## Goal

Make a workspace whose Ox process stopped recoverable without restarting the Bun
host, and make a pending permission or form discoverable when it belongs to a
workspace or conversation the browser is not currently showing. Finish the
browser coverage for multi-workspace operation.

## Related code

- `client/src/host.ts` — Owns the supervisor map, the serialized workspace
  operation queue, and the browser-safe workspace catalog.
- `client/src/workspace-supervisor.ts` — `start()` runs the full launch,
  initialize, stored-credential, and default-conversation sequence; `logout()`
  leaves the workspace `stopped` with no running child; `unavailable()` and the
  child `exit` handler clear sessions and record why.
- `client/src/session-controller.ts` — `interactions` and the stale-answer
  errors in `resolvePermission` and `resolveElicitation`.
- `client/src/browser.tsx` — `WorkspacePicker`, `History`, the unavailable
  alert, and the `Ox processes` support list.
- `client/e2e/smoke.spec.ts` —
  `createFixture({ registerSecondWorkspace: true })` and the per-workspace
  `stopOx` and `waitForOxExit` helpers.

## Decisions

- Add one `restart-workspace` browser command. It stops the entry's supervisor
  when one exists and starts a replacement for the same registered root. It runs
  on the existing serialized workspace queue so a restart cannot interleave with
  a removal of the same entry.
- Restart recovers a workspace that is `unavailable` after an Ox exit and one
  that is `stopped` after logout. It is honest about what returns: the
  replacement process reconstructs durable history through the ordinary list and
  load path, and interrupted turns are not resumed.
- A restart seeds the replacement supervisor with the stopped supervisor's
  bounded diagnostics, so the reason the workspace failed survives the recovery
  that follows it.
- Startup and registration keep ignoring a supervisor that cannot start, because
  they must not fail the host or the registration. An explicit restart is a user
  request, so its failure is returned as the command's error.
- Publish `awaiting` on each catalog entry, true while any session in that
  workspace has a pending interaction, and on each session summary of the
  selected workspace. Nothing else routes a blocked turn to the user when the
  blocked session or workspace is not the one on screen; `busy` alone reports a
  turn as running.
- The unavailable alert and the workspaces-waiting list carry their own
  controls, because a stopped process and a blocked turn are actionable problems
  rather than status to read in support details. Per-process restart stays in
  the support list so a workspace that is not selected can be recovered.

## Test plan

- Host: restarting an unavailable workspace replaces its supervisor and keeps
  the earlier diagnostics; restarting an unregistered workspace fails; a restart
  leaves the other workspace's supervisor untouched.
- Protocol: `restart-workspace` validates its workspace identifier, and catalog
  entries and session summaries validate `awaiting`.
- Supervisor: `awaiting` becomes true while a permission is pending and false
  once it is answered, including for a session that is not selected.
- Session controller: answering an interaction that already settled reports that
  it is no longer pending rather than resolving the ACP callback twice.
- Playwright, two real roots: terminate the second workspace's Ox, restart it
  from support details, and prove it prompts again with its durable history
  restored while the first workspace keeps running. Separately, block a turn on
  a permission in an unselected workspace, follow the waiting control to it, and
  answer it there.

## Implementation plan

- Derive `awaiting` from the controllers in `workspace-supervisor.ts`, publish
  it on `WorkspaceState` and on each `SessionSummary`, and accept an optional
  diagnostics seed in `WorkspaceSupervisorOptions`.
- Add `restart-workspace` to the browser command union and `awaiting` to the
  catalog entry and session summary schemas in `protocol.ts`.
- In `host.ts`, make `startSupervisor` report why a start failed, keep that
  failure silent for startup and registration, and add a serialized restart that
  stops the current supervisor, seeds its diagnostics, and starts a replacement.
- In `browser.tsx`, add a restart control to the unavailable or stopped alert
  and to each entry of the `Ox processes` list, list workspaces waiting for an
  answer with a control that selects them, and mark a waiting conversation in
  the history list.
- Extend the Playwright fixture only as the two new scenarios require, reusing
  the existing per-workspace pid file.

## Documentation updates

- `docs/browser.md`: a workspace whose Ox stopped can be restarted from the
  browser, and what a restart does and does not restore.
- `eng/client-architecture.md`: the host can start a replacement process for a
  registered workspace on request, and pending interactions remain routable to
  the workspace and conversation that own them.
- Check off this child task and remove the completed top-level item in
  `eng/todo.md`. This finishes the browser-managed multi-workspace milestone,
  which is the boundary that calls for the repository's completion review.
