# Multi-workspace supervision and routing

## Goal

Run one independently supervised Ox process for every registered workspace, and
route each browser command to the workspace it names. Turns in two workspaces
must progress concurrently, and an Ox failure or removal in one workspace must
leave the other workspace's process, sessions, and callbacks untouched.

## Related code

- `client/src/host.ts` — Owns the single selected supervisor, the global
  workspace-operation queue, and snapshot assembly.
- `client/src/protocol.ts` — Owns the workspace catalog, the selected-workspace
  detail, and the browser command union.
- `client/src/workspace-supervisor.ts` — Owns one child process, connection,
  session controllers, and `#activePrompts`; already isolates its own failures.
- `client/src/workspace-registry.ts` — Owns canonical roots, opaque IDs, and
  selection.
- `client/e2e/smoke.spec.ts` — `createFixture` seeds one workspace and one Ox
  pid file; `startBrowserHost` supplies the Ox invocation.

## Decisions

- Every registered entry is active: the host starts a supervisor per entry at
  startup (concurrently), starts one on registration, and stops one on removal.
  Selection no longer starts or stops a process, so switching workspaces cannot
  interrupt a running turn.
- A supervisor whose start rejects leaves its entry supervisor-less rather than
  failing host startup or the registration command; that entry reports
  `unavailable` and refuses commands.
- Every supervisor-routed browser command carries `workspaceId`. The host
  rejects a command whose workspace has no running supervisor with
  `workspace is not active`, so a concurrent selection change cannot redirect a
  command to a different Ox process. Registry commands keep their existing
  shape.
- The snapshot keeps full detail (`workspace`, `authentication`, `sessions`) for
  the selected workspace only, and extends each catalog entry with the
  supervisor's `status` and a `busy` flag that is true while any session in that
  workspace has an active prompt. Broadcasting every workspace's transcript on
  every chunk is the cost this avoids, and `busy` is what makes a background
  turn observable.
- Registry mutations stay serialized because they persist one file, but
  supervisor-routed commands stay off that queue so a slow or failing workspace
  cannot stall another workspace's turn.
- Per-workspace `status` and activity are support information, not primary
  navigation: they render inside the existing support details.

## Test plan

- Host: two registered workspaces both appear in the catalog with a status;
  `set-mcp-servers` addressed to one workspace changes only that workspace's
  detail; a command naming a removed or unregistered workspace fails with
  `workspace is not active` while the other workspace still accepts commands; a
  workspace whose Ox command cannot start leaves the other workspace usable.
- Protocol: catalog entries validate `status` and `busy`, and a
  supervisor-routed command without a valid `workspaceId` is refused.
- Registry: removing a workspace stops its supervisor and leaves the remaining
  supervisor running.
- Playwright, two real roots: hold a turn in the first workspace, select the
  second, run a turn there to completion while the first still reports working,
  release the held response, reselect the first, and assert its transcript
  completed while unselected. Then terminate the second workspace's Ox and
  assert it reports unavailable while the first stays ready and prompts again.

## Implementation plan

- Add `busy` to `WorkspaceState` in `workspace-supervisor.ts`, derived from the
  active prompt map, and publish it with existing state changes.
- Extend the snapshot catalog entries with `status` and `busy`, and add a
  required `workspaceId` to every supervisor-routed browser command in
  `protocol.ts`.
- Replace the single supervisor in `host.ts` with a map from workspace ID to
  supervisor: start all entries at startup, start on register, stop on remove,
  stop all on host shutdown, and build the catalog from registry entries joined
  with supervisor state.
- Route supervisor-routed commands by `workspaceId`, keeping registry mutations
  on the serialized queue and everything else off it.
- Stamp the selected workspace ID onto commands in `browser.tsx`, surface a
  visible error when no workspace is selected, and list each workspace's status
  and activity in the support details.
- Extend the Playwright fixture with an optional second registered workspace and
  a per-workspace Ox pid file (the runner keys the file by its working
  directory), making `stopOx` and `waitForOxExit` workspace-scoped.

## Documentation updates

- `docs/browser.md`: every registered workspace runs its own supervised Ox
  process, and selecting a workspace changes what is displayed rather than
  replacing the process.
- `eng/client-architecture.md`: state that each registered entry has an
  independent supervisor and that browser commands name their workspace,
  removing the framing that describes this as later work.
- Check off this child task in `eng/todo.md`.
