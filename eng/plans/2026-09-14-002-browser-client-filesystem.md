# Add browser-client filesystem callbacks

## Goal

Let the first-party browser ACP client safely provide Ox's paired text-file
callbacks for its startup-selected workspace.

## Desired outcome

The host advertises both filesystem capabilities and executes each callback only
for an active session in its canonical workspace. Reads return valid UTF-8 with
ACP line paging, and writes create or replace regular files without following
symbolic links. Escaping paths, inactive sessions, non-regular files, oversize
or invalid text, and cancellation fail cleanly. Real Ox file-tool turns use the
host filesystem rather than Ox's local executor.

## Summary of approach

Add a session-scoped filesystem executor at the Bun host boundary. It validates
the callback session and confines every path by walking from the canonical root
with `lstat`, rejecting every symbolic-link component and non-regular target.
Its bounded UTF-8 reader applies ACP's one-based line and limit semantics; its
writer creates only safely checked missing parent directories. The workspace
supervisor registers both SDK handlers and advertises the paired capability.
Focused executor tests cover the hostile boundary, while the browser harness
drives Ox through read, write, and exact-edit tool calls.

## Related code

- `client/src/filesystem-executor.ts` - confined host-side ACP filesystem
  adapter.
- `client/src/workspace-supervisor.ts` - callback registration, capability
  negotiation, and active-session routing.
- `client/src/filesystem-executor.test.ts` - stable boundary tests.
- `client/src/workspace-supervisor.test.ts` - SDK callback wiring proof.
- `client/e2e/smoke.spec.ts` - real Ox browser-host scenario.
- `internal/tools/{read,write,edit}.go` - Ox's delegated filesystem contract.

## Current state

- The supervisor owns canonical workspace identity and active session
  controllers, but currently offers no filesystem callback capability.
- Ox requires read and write to be advertised together because exact edits read
  current content from the same executor that receives the replacement.
- The workspace boundary limits whole-file reads to 8 MiB; the browser executor
  preserves that bound rather than allowing callback delegation to widen it.
- Alpha confirms the value of host-owned editor callbacks; this client applies
  the stronger session, confinement, and symlink rules from
  `eng/client-architecture.md`.

## Structural considerations

- **Hierarchy:** The supervisor remains the ACP connection owner and delegates
  only a session-valid callback to its workspace executor; React receives no
  file capability.
- **Abstraction:** One concrete executor owns pathname validation, bounded text
  reads, and replacement writes, avoiding separate and inconsistent read/write
  confinement.
- **Modularization:** The executor is a focused host adapter, separate from
  session control and browser protocol state.
- **Encapsulation:** ACP metadata, absolute server paths, and filesystem handles
  never cross into browser snapshots or commands.
- **Testability:** Direct executor tests exercise filesystem edge cases, and
  real-process tests prove the advertised capability cannot drift from Ox tool
  behavior.

## Test plan

- Verify full and paged reads, replacement writes, absent parent creation,
  active-session validation, invalid paths, symlink traversal, non-regular
  targets, oversized and non-UTF-8 reads, and already-aborted requests.
- Verify the supervisor advertises paired filesystem capabilities and routes
  callbacks only for a current active session.
- Drive a real Ox process through read, write, and exact-edit tool calls; prove
  the workspace reflects the callback results and callbacks cease when a turn is
  cancelled.
- Do not add browser file browsing or editing controls: callbacks are an ACP
  executor, not a general browser API.

## Implementation plan

- Add the confined, bounded filesystem executor and direct boundary coverage.
- Register its paired ACP SDK handlers in the workspace supervisor and advertise
  them together.
- Extend the real-process browser fixture with filesystem tool responses and
  assert the resulting workspace state.
- Run focused client type, unit, and browser gates and mark the roadmap task
  complete.

## Documentation updates

- Mark the completed browser-client filesystem task in `eng/todo.md`.

## Impact assessment

- Code paths affected: host-side client callback handling, ACP initialization,
  client test fixtures, and the browser-client roadmap.
- Data and protocol impact: standard paired `fs/read_text_file` and
  `fs/write_text_file` callbacks only; no browser protocol additions.
- Dependency impact: none.

## Validation

- Run `bun run check`, `bun test --path-ignore-patterns e2e`, and
  `bun run
  test:e2e` from `client/`.
