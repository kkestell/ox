# Multi-workspace review fixes

## Goal

Repair the eight defects the browser-managed multi-workspace completion review
records in `eng/reviews/2026-09-14-001-multi-workspace-milestone-review.md`.
When this lands: one unusable registered root cannot stop the host from
starting, every published workspace status has a producer, workspace-command
routing cannot silently misroute, a background turn stops rebuilding every
workspace's transcript, the header keeps naming the selected workspace while it
restarts, and the documents describe what logout and restart actually do.

## Related code

- `client/src/workspace-registry.ts` — Owns the canonical-root rule
  (`canonicalWorkspace`, `:197`) and whole-file validation
  (`validatePersistedRegistry`, `:161`).
- `client/src/workspace-supervisor.ts` — `static start` (`:125`) canonicalizes
  before constructing, which is what forces the host to subscribe late; the
  second `canonicalWorkspace` (`:978`); `state` (`:145`) builds interactions and
  a deep-copied transcript; `subscribe` (`:171`) has exactly one caller.
- `client/src/host.ts` — `publish`/`selectedState` (`:59-69`),
  `startSupervisor`/`stopSupervisor` (`:71-96`), the restart case (`:117-127`),
  `browserWorkspaces`/`redactWorkspaceRoots` (`:255-290`), and the
  `workspaceCommands` set beside the `WorkspaceCommand` union (`:369-378`).
- `client/src/browser.tsx` — `sendRouted` (`:114`), the header (`:184`), and the
  `stopped` alert and `restartable` gate (`:174`, `:187`).
- `client/src/filesystem-executor.ts:45` — Confinement compares against the
  workspace root verbatim, so the root a supervisor is given must already be
  canonical.

## Decisions

**The registry owns the canonical-root rule; the supervisor only reports that it
cannot use the root it was given.** `canonicalWorkspace` becomes an export of
`workspace-registry.ts` and the supervisor's copy is deleted. The supervisor
still proves its root is listable, because a spawn into a missing `cwd` reports
`ENOENT` against the _executable_ path (verified with Bun 1.4.0) and would blame
Ox for a deleted directory. That check happens inside `start()`, records the
helper's message as an ordinary diagnostic, and sets `unavailable` instead of
rejecting. It does not re-resolve or replace `this.workspace`.

**A per-entry root failure is a per-entry outcome.** `WorkspaceRegistry.load`
keeps every whole-file rejection — schema, version, selection invariant,
duplicate IDs, duplicate roots — and root absoluteness moves into the persisted
schema, where a relative root stays a malformed file. An entry whose root no
longer canonicalizes keeps its persisted root (canonical when it was written)
and loads normally; its supervisor then reports it `unavailable` with the
listability message, and restart is the retry. Load-time canonicalization is
kept only so a hand-edited file cannot alias one directory into two supervisors;
its result feeds the duplicate-root set as before.

**A supervisor exists from registration until removal, including while it is
starting and stopping.** Dropping canonicalization out of the static factory
makes construction synchronous, so `WorkspaceSupervisor` gains a public
constructor and a public `start()`. The host registers and subscribes the
supervisor, publishes, and only then awaits `start()`; `stopSupervisor` awaits
`stop()` before unsubscribing and deleting. `starting` and `stopped` gain
producers, the restart window reads correctly, and the map has no gap during a
restart because construction no longer awaits.

**Listeners are notified, not handed state.** `subscribe` takes `() => void` and
the host reads what the snapshot needs: a new cheap `catalog` accessor on the
supervisor for the four catalog fields, and one full `state` build for the
selected workspace only. A publish caused by an unselected workspace cannot
change the selected detail, so it reuses the previous snapshot's `workspace`,
`authentication`, and `sessions` and rebuilds only the catalog. Every
host-driven publish rebuilds the whole snapshot, which is what keeps registry
changes and redaction correct.

**Routing is one switch.** `isWorkspaceCommand` and the `workspaceCommands` set
are replaced by a single `switch (command.type)` that returns `performWorkspace`
for the four registry commands and falls through to the supervisor lookup for
the rest. A new registry command that nobody routes then fails to compile twice:
at `command.workspaceId` and at `perform`'s now non-exhaustive switch, whose
declared `Promise<void>` makes a missing case an error under `strict`. No
runtime test can reach the misrouting the review found once this lands.

## Test plan

- `client/src/workspace-registry.test.ts` — `load` keeps an entry whose root was
  deleted, and still rejects a relative persisted root as a malformed file.
- `client/src/host.test.ts` — Starting a host over a registry with one deleted
  root serves a snapshot in which that entry is `unavailable` and the surviving
  entry still accepts a routed command.
- `client/src/host.test.ts` — A restart of the selected workspace publishes
  `stopped` and then `starting` before it settles, asserted over the recorded
  snapshot sequence rather than the latest snapshot; the test client needs to
  retain each snapshot's catalog status.
- `client/src/host.test.ts` — The selected workspace's detail survives a publish
  caused by another workspace, which is the reuse path above.
- `client/src/workspace-supervisor.test.ts` — A supervisor given a path that is
  not a directory reports `unavailable` with `workspace must be a directory`
  instead of rejecting, replacing the current `rejects.toThrow` test.
- No new browser-rendering test. `client/` has no React test harness, and adding
  one is a dependency decision outside this repair; the `stopped` alert and the
  header's use of the catalog entry are proven by the host-level status sequence
  plus the existing Playwright restart test.

## Implementation plan

- Export `canonicalWorkspace` from `client/src/workspace-registry.ts`, move root
  absoluteness into `persistedRegistrySchema`, and make
  `validatePersistedRegistry` fall back to the persisted root when
  canonicalization fails.
- Delete `canonicalWorkspace` and the `realpath`/`stat` imports from
  `client/src/workspace-supervisor.ts`. Replace `static start` with a public
  constructor and a public `start()` that first proves the root is listable and
  reports failure through `unavailable()`.
- Add a `catalog` accessor to `WorkspaceSupervisor` that reads no session
  transcript, and change `subscribe` to a zero-argument listener; drop the state
  build from its private `publish`.
- In `client/src/host.ts`: register and subscribe before awaiting `start()`,
  publish once after each map mutation, await `stop()` before unsubscribing and
  deleting, and drop the now-redundant intermediate `publish()` in the restart
  case. Give `publish` the publishing workspace's ID and take the catalog-only
  path when it is not the selected one. Point `browserWorkspaces` at `catalog`,
  and sort the registry roots once per `browserWorkspace` instead of once per
  diagnostic.
- Collapse `isWorkspaceCommand` and `workspaceCommands` into the single routing
  switch in the WebSocket message handler.
- In `client/src/browser.tsx`: name the header from `selectedWorkspace`, and
  make the routed-send helper return the reason it could not send so a missing
  selection reports `No workspace is selected` rather than a lost connection.
- Canonicalize the root in `temporaryWorkspace()` in
  `client/src/workspace-supervisor.test.ts`; the supervisor no longer resolves
  it, and macOS `tmpdir()` is a symlink that would break filesystem confinement
  in the callback test.

## Documentation updates

- `docs/browser.md:33-35` — Drop the logout case from the restart paragraph.
  Restart recovers a process that failed or exited; logging out leaves the
  process running and clears the stored credential.
- `eng/client-architecture.md:152` — Startup exposes every registered entry; a
  root that stopped being usable makes that one workspace unavailable rather
  than failing startup.
- `eng/client-architecture.md:175-180` — Same logout correction, and state that
  a supervisor exists for every registered entry from registration until
  removal, which is what makes the starting and stopped statuses observable.
- `eng/todo.md` — This repair completes the fixes the milestone's completion
  review requires, so the finished
  `Add browser-managed multi-workspace
  operation` subtree comes out once the
  affected gates rerun clean.
