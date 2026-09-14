# Browser-managed multi-workspace operation — completion review

## Scope and mode

General, change-directed review of the commits that make up the
`Add browser-managed multi-workspace operation` milestone:

- `afc2a55` Add a browser-managed workspace registry
- `809c431` Drop ignores for the removed browser scratch tree
- `afae85a` Supervise and route one Ox process per workspace
- `257bff5` Restart stopped workspaces and route waiting interactions

Corpus: `client/src/workspace-registry.ts`, `client/src/host.ts`,
`client/src/protocol.ts`, `client/src/workspace-supervisor.ts`,
`client/src/session-controller.ts`, `client/src/browser.tsx`, their unit tests,
`client/e2e/smoke.spec.ts`, `docs/browser.md`, `docs/spec.md`,
`eng/client-architecture.md`, and the three owning plans
(`eng/plans/2026-09-14-008/009/010`).

### Selected topics and why

- **correctness** — a new persistent store, a new process-lifecycle state
  machine (register, start, select, restart, remove), and a four-value status
  published to the browser.
- **error-handling** — the milestone's stated principle is that one bad
  workspace must not take down the host or another workspace; that principle has
  to hold on every failure path.
- **architecture** — the single supervisor became a map, a second canonical-root
  rule appeared, and browser command routing split into two shapes.
- **performance** — snapshot assembly moved from one supervisor to every
  supervisor and runs on every streamed chunk.
- **resources** — one child process per registered workspace, with subscriptions
  and terminations to pair.
- **concurrency** — a serialized registry queue that supervisor-routed commands
  deliberately bypass.
- **api-design** — `protocol.ts` is the shared host/browser contract and every
  command in it changed shape.
- **documentation** — `docs/browser.md`, `docs/spec.md`, and
  `eng/client-architecture.md` all changed to describe the new boundary.
- **testing** — the milestone is the completion gate for a top-level todo item.

Not selected: **unsafe** (no `unsafe`, `reflect`, or cgo; the change is
TypeScript only), **dependencies** (`go.mod`, `go.sum`, `package.json`, and
`bun.lock` are unchanged), **security** beyond the trust-boundary note recorded
under Observations (the one new external surface, `register-workspace`, is
bounded, canonicalized, and covered by a snapshot-leak assertion in the
Playwright suite).

## Findings

### 1. One unusable registered root makes the whole host refuse to start

`client/src/workspace-registry.ts:87` and `:179`

`WorkspaceRegistry.load` runs `canonicalWorkspace` over every persisted entry
and rejects the entire registry when any one of them fails. `startHost`
(`client/src/host.ts:44`) awaits that load before anything else, so a single
registered root that was deleted, renamed, or unmounted since the last run makes
`startHost` reject and the browser host exit. The user cannot reach the browser
to remove the entry, because the browser is what failed to start; the only
recovery is hand-editing `$XDG_CONFIG_HOME/ox/workspaces.json`.

This directly contradicts the principle the same milestone states at
`client/src/host.ts:82-85` and in `eng/plans/2026-09-14-009`: "A supervisor
whose start rejects leaves its entry supervisor-less rather than failing host
startup or the registration command; that entry reports `unavailable` and
refuses commands." Launch failure degrades one entry; validation failure of the
same root takes down everything.

Failure scenario, reproduced: register `/tmp/x/good` and `/tmp/x/gone`, delete
`/tmp/x/gone`, restart the host. `WorkspaceRegistry.load` throws
`workspace must be a listable directory` and `startHost` rejects. Verified with
a scratch `bun test` that asserted both rejections.

Suggested fix: make per-entry validation a per-entry outcome. Keep the strict
checks for shape, version, duplicate IDs, and duplicate roots — those are
genuinely whole-file problems — but let an entry whose root does not
canonicalize load as a registered entry with no supervisor. It already has a
browser-visible representation (`status: "unavailable"`) and a browser control
(remove, restart).

### 2. The host can only ever publish two of the four workspace statuses

`client/src/host.ts:71-96`, `client/src/workspace-supervisor.ts:17`,
`client/src/browser.tsx:174` and `:187`

`startSupervisor` puts the supervisor in the map and subscribes to it only after
`WorkspaceSupervisor.start` resolves, by which point its status is already
`ready` or `unavailable`. `stopSupervisor` unsubscribes and deletes from the map
_before_ awaiting `stop()`, which is the only thing that sets `stopped`. An
entry with no supervisor falls back to `unavailable` in `browserWorkspaces`
(`client/src/host.ts:268`). So `starting` and `stopped` are unreachable in any
snapshot.

Consequences that are live today:

- `client/src/browser.tsx:187` — the `Ox is stopped for this workspace.` alert
  is dead code, and nothing tests it.
- `client/src/browser.tsx:174` — `restartable` reduces to
  `status === "unavailable"`.
- A restart of the selected workspace reports the workspace as `unavailable` for
  the entire replacement window (spawn plus up to the 2s initialization
  timeout), when `starting` is what is actually true and is what the enum exists
  to say.
- The `stopped` member of `WorkspaceStatus` and of the `workspaceStatus` enum in
  `client/src/protocol.ts:6` is contract surface with no producer, which the
  repository's rules treat as speculative flexibility.

Suggested fix: register and subscribe the supervisor before awaiting its start,
and keep it in the map until `stop()` resolves. Both states then become real,
the restart window reads correctly, and the existing browser branch stops being
dead. Alternatively, drop `starting` and `stopped` from `WorkspaceStatus`, the
protocol enum, and the browser. The first is the better shape because the
restart window is exactly the case the milestone added.

### 3. `docs/browser.md` and `eng/client-architecture.md` claim a logout stops Ox

`docs/browser.md:33`, `eng/client-architecture.md:176`

Both documents say a workspace whose user logged out has no usable process and
is recovered by a restart. `WorkspaceSupervisor.logoutImpl`
(`client/src/workspace-supervisor.ts:867-882`) sends `agent/logout` and sets
authentication back to `required`. It does not stop the child, does not change
`status`, and the workspace stays `ready`.

The follow-on claim is also wrong in the opposite direction:
`docs/browser.md:34` says a restart "reopens the workspace's stored
conversations". `Agent.Logout` (`internal/agent/agent.go:322`) clears the stored
credential, so a replacement process started after a logout cannot authenticate
(`authenticateStoredCredential` at `client/src/workspace-supervisor.ts:829`) and
therefore does not open any conversation. Restarting after a logout costs the
user a working process and returns nothing.

Note also that after a logout `restartable` is false (status is `ready`), so the
header restart control the docs point at is not even offered; only the
per-process button in support details is.

Suggested fix: delete the logout case from both documents. Describe restart as
recovery for a process that failed or exited. If recovering a logged-out
workspace is wanted product behavior, it is a separate change — the supervisor
would have to stop the child on logout, which is also what would make finding
2's `stopped` state real.

### 4. Every publish rebuilds every workspace's full state, transcripts included

`client/src/host.ts:63-69` and `:260-271`

`publish()` ignores the state its listener is handed
(`supervisor.subscribe(publish)` at `client/src/host.ts:79`) and instead calls
`selectedState()` plus one `supervisors.get(id)?.state` per registered workspace
inside `browserWorkspaces`. `WorkspaceSupervisor.state`
(`client/src/workspace-supervisor.ts:145`) builds the active session's
`interactions` and `transcript`, and `SessionController.transcript`
(`client/src/session-controller.ts:88`) deep-copies every entry.
`browserWorkspaces` reads exactly three booleans off that: `awaiting`, `busy`,
`status`.

`publish()` runs on every `session/update` notification — once per streamed
chunk. Measured: with three registered workspaces, one publish triggers **5**
`state` builds (instrumented the prototype getter and counted); the formula is
N + 2 where the pre-milestone code did 1. A 600-entry transcript copies in 0.033
ms (1000 iterations, Bun 1.4.0), so three workspaces mid-turn burn about 0.13 ms
of pure waste per chunk, growing linearly with the number of registered
workspaces — the exact axis this milestone exists to extend.

`redactWorkspaceRoots` (`client/src/host.ts:286-290`) sits on the same path and
re-sorts the registry and runs a `split`/`join` per root per diagnostic on every
publish, for up to 16 diagnostics and 1024 roots.

Suggested fix: give `WorkspaceSupervisor` cheap accessors for what the catalog
needs — a `catalogEntry()` returning `{ awaiting, busy, name, status }` that
touches no transcript — and have `browserWorkspaces` call that. Have `publish`
use the `WorkspaceState` its subscription already receives for the selected
workspace instead of re-deriving it. This is a simplification as much as a
speed-up: the current code computes the full state three ways for one snapshot.

### 5. Restarting the selected workspace tells the user nothing is selected

`client/src/browser.tsx:184`

The header renders `snapshot.workspace?.name ?? "No workspace selected"`, but
`snapshot.workspace` is absent whenever the selected entry has no supervisor —
which is precisely the restart window opened by `publish()` between
`stopSupervisor` and `startSupervisor` at `client/src/host.ts:124-126`. For the
whole replacement (spawn plus up to 2s of initialization) the header claims no
workspace is selected while one plainly is, and every gated section below it
unmounts.

The catalog entry carries the name and survives the window — `selectedWorkspace`
is already computed two lines above for exactly this reason
(`client/src/browser.tsx:171`).

Failure scenario: select a workspace, click `Restart Ox`, read the header. It
says `No workspace selected` until the replacement finishes.

Suggested fix:
`{snapshot ? <p>{selectedWorkspace?.name ?? "No workspace selected"}</p> : null}`.

### 6. Workspace-command routing depends on a `Set` and a type union agreeing, and nothing checks that they do

`client/src/host.ts:369-378`

`WorkspaceCommand` is an `Extract` over four literals and `workspaceCommands` is
a `new Set([...])` of the same four strings, typed `Set<string>`.
`isWorkspaceCommand` is the runtime split between `performWorkspace` and
`perform`. Adding a fifth registry command to the union without adding it to the
set type-checks cleanly.

The failure is silent rather than loud: the command falls through to `perform`,
whose `switch` has no matching case, so it returns `undefined`. `respond`
(`client/src/host.ts:310`) awaits that, hits no catch, and replies `ok: true`
with a bumped revision. The browser reports the workspace registry updated and
nothing happened.

Suggested fix: derive the membership test from the union so a gap is a compile
error, e.g.
`const workspaceCommands: Record<WorkspaceCommand["type"], true> = { "register-workspace": true, ... }`
— a missing key fails to type-check. A single exhaustive `switch` over
`command.type` covering both groups would also work and is closer to the
repository's preference for a direct conditional over an indirect dispatch.

### 7. Two `canonicalWorkspace` functions with two different rules

`client/src/workspace-registry.ts:197`, `client/src/workspace-supervisor.ts:978`

Same name, same concept, two implementations with different strictness. The
registry resolves the real path and proves the directory is _listable_ by
opening it and reading an entry (with a comment explaining why). The supervisor
resolves the real path and only checks `stat().isDirectory()`. The supervisor
then re-runs `realpath` on a path the registry already canonicalized.

Canonical-root validation is the registry's responsibility —
`eng/client-architecture.md` says the host "canonicalizes and validates before
registration". The supervisor's copy is a second implementation of an owned rule
that can accept a root the registry rejects and produces a different message for
the same problem.

Suggested fix: the supervisor should take the already-canonical root from the
registry and drop its own canonicalization, or call a single exported helper.
Its `options.workspace` comes from `RegisteredWorkspace.root` on every
production path.

### 8. A missing workspace selection is reported as a lost host connection

`client/src/browser.tsx:114-127`

`sendRouted` returns `false` for two different reasons — no selected workspace,
or the socket is not open — and every caller reports the second.
`submitSessionCommand` sets `The host connection is not open`; `saveMCPServers`
sets the same string. `eng/plans/2026-09-14-009` asked for "a visible error when
no workspace is selected", which this does not give.

Reachability is narrow, since the controls that call `sendRouted` render only
under `snapshot?.workspace`, but the window is real: a snapshot that clears the
selection (the last workspace removed, or the restart window in finding 5) can
land between render and click.

Suggested fix: have `sendRouted` distinguish the two, or check
`snapshot?.workspaces.selectedId` at the call site and report
`No workspace is selected`.

## Testing

Coverage for the milestone's own contracts is good and sits at sensible
boundaries: the registry's persistence, validation, symlink deduplication, and
failure atomicity are unit-tested; the protocol's catalog, `awaiting`, and
per-command `workspaceId` are schema-tested including negative cases; the host
proves per-workspace routing, isolation on removal, restart with carried-forward
diagnostics, and that a restart does not stall another workspace; and the
Playwright suite covers the two scenarios the plans called for — concurrent
turns with failure isolation across two real roots, and answering a permission
raised in an unselected workspace. The stale-answer path named in
`eng/plans/2026-09-14-010` is covered at
`client/src/session-controller.test.ts:150` and again through the supervisor at
`client/src/workspace-supervisor.test.ts:337`.

Gaps, each tied to a finding above:

- No test loads a registry whose root has disappeared (finding 1). The nearest
  test, `rejects non-directory and unusable persisted roots`, only exercises
  `register`, not `load`, and no test asserts what `startHost` does with a
  partially unusable registry.
- No test asserts any snapshot carrying `starting` or `stopped` (finding 2), and
  no test renders the `Ox is stopped for this workspace.` branch. Asserting the
  status sequence across a restart would have caught the gap.
- The Playwright restart test never checks the header during the replacement
  window (finding 5); `expect` retries hid the wrong intermediate text.
- Nothing asserts that a misrouted registry command fails rather than silently
  reporting success (finding 6).

## Checks run

- `cd client && bun run check` — clean (`tsc --noEmit`).
- `cd client && bun test` — 87 pass, 0 fail, 7 files.
- Scratch `bun test` instrumenting `WorkspaceSupervisor.prototype.state`: 5
  state builds per publish with 3 registered workspaces (finding 4). Removed
  after.
- Scratch `bun test` micro-benchmark of `SessionController.transcript` on a
  600-entry transcript: 0.033 ms per copy over 1000 iterations (finding 4).
  Removed after.
- Scratch `bun test` reproducing finding 1: `WorkspaceRegistry.load` and
  `startHost` both reject when one registered root is deleted. Removed after.
- `grep` for every `stopped` and `starting` producer and consumer across
  `client/src`, `client/e2e`, and the two documents (finding 2).
- Read `internal/agent/agent.go:322` to confirm `Logout` clears the stored
  credential (finding 3).
- Not run: `make check-all` and `bun run test:e2e`. The Playwright suite builds
  the Go binary and drives real processes; the findings above are static or
  reproduced with focused scratch tests, and none of them is contradicted by the
  suite passing.

## Observations, no finding

- **Security trust boundary.** `register-workspace` is the milestone's only new
  external surface. It is bounded (`startsWith("/")`, 4096 characters),
  canonicalized through `realpath`, and proven listable before it is stored, and
  the canonical root never travels outward: the browser sees a UUID and a
  basename, and `redactWorkspaceRoots` strips registered roots from diagnostics.
  The Playwright suite asserts a raw snapshot contains neither workspace path.
  Registering any readable directory is the intended privilege of a local user
  who already controls the host, and `docs/browser.md` states the exposure for
  non-loopback binds.
- **Registry mutations are serialized twice**, once by
  `WorkspaceRegistry.serialize` and once by `host.serializeWorkspace`. The host
  queue is load-bearing because it pairs a mutation with supervisor lifecycle,
  and the registry queue is exercised directly by its own tests and the
  Playwright fixture, so both have a caller. Worth collapsing if the registry
  ever stops being driven from outside the host.
- **Deliberately unserialized**, and correct: supervisor-routed commands bypass
  the workspace queue so a slow shutdown or a failing restart cannot stall
  another workspace's turn. `host.stop` awaits `workspaceOperation` before
  stopping supervisors, so a restart in flight cannot leave an orphan.
- **Child-process lifetime** is paired correctly on every path I traced:
  `stopSupervisor` always precedes removal from the map, `stop()` is memoized,
  and `terminateChild` escalates SIGTERM to SIGKILL with bounded waits. The one
  theoretical leak — `WorkspaceSupervisor.start` rejecting after `spawn` — needs
  `ndJsonStream`, `connect`, or `recordAuthentication` to throw, and Ox exits on
  stdin EOF when the host dies. Not worth defending against today.
- **`eng/todo.md`** still carries the top-level item with its three children
  checked, which is correct: the repository's rules put this review before the
  subtree is removed.

## Verdicts

- correctness — findings 1, 2, 5.
- error-handling — findings 1, 8.
- architecture — findings 6, 7; plus the note on doubled serialization.
- performance — finding 4.
- documentation — finding 3.
- testing — four gaps, all tied to findings above.
- resources — nothing to report; process and subscription lifetimes pair on
  every path traced.
- concurrency — nothing to report; the queue boundary is deliberate and
  `host.stop` orders correctly against it.
- api-design — nothing to report; `workspaceId` on every routed command is the
  right shape, the catalog's `superRefine` encodes the selection invariant, and
  the optional `workspace` detail correctly expresses "nothing selected". The
  one unproduced enum member is reported under finding 2.

## Follow-up needing evidence outside this corpus

None. Every finding is reproducible inside `client/`.
