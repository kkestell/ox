# Browser workspace supervisor

## Goal

Start, initialize, supervise, and cleanly stop the one Ox process selected at
browser-host startup.

## Desired outcome

The Bun host validates a startup workspace, starts one configured Ox command in
that root, initializes an ACP v1 connection through the official TypeScript SDK,
and publishes a browser-safe ready or unavailable process state. It keeps a
bounded stderr tail for diagnostics, broadcasts process transitions to every
browser, and shuts the child and transport down cleanly. An unexpected child
exit leaves the host available but reports the workspace as unavailable.

## Related code

- `eng/client-architecture.md` — Owns workspace supervision, safe diagnostics,
  capability staging, and shutdown ownership.
- `docs/spec.md#web-client` — Owns the host-started Ox and connection-failure
  behavior.
- `client/src/host.ts` — Owns the HTTP/WebSocket boundary and host snapshots.
- `references/repos/third-party/protocol/typescript-sdk` — Owns the supported
  ACP TypeScript client and NDJSON transport API.

## Summary of approach

Add a focused workspace supervisor that owns the child process and ACP
connection. It will advertise no optional executor capabilities until their
complete callback lifecycles are implemented in later tasks. The host will
construct its snapshot from supervisor state and subscribe to transitions, while
the browser remains a renderer of that state. The command-line entry point will
require a workspace and permit an explicit Ox executable and arguments for local
deployment and tests.

## Test plan

- Unit-test startup workspace validation, bounded diagnostic retention, a
  successful initialization, a failed executable, and an unexpected child exit
  with a controllable ACP fixture process.
- In Playwright, launch a real Ox binary through the host and assert the browser
  receives ready state, then terminate Ox and assert that the same browser sees
  unavailable state and bounded diagnostics.
- Assert host shutdown terminates the child and leaves no live fixture process.

## Implementation plan

- Extend the browser-safe snapshot with workspace process state and bounded
  diagnostics, retaining the existing connection status as its projection.
- Add the workspace supervisor with canonical-root validation, child lifecycle,
  stderr tail retention, SDK-backed initialization, state subscriptions, and
  idempotent shutdown.
- Make host startup asynchronous, wire supervisor changes into revisioned
  snapshots and connected sockets, and expose startup flags for workspace and Ox
  invocation.
- Render the workspace process state and diagnostics with semantic, unstyled
  controls.
- Retain the independent SDK smoke path and add focused supervisor and browser
  lifecycle tests.
- Mark the roadmap task complete.

## Documentation updates

- Mark the workspace-supervision roadmap item complete in `eng/todo.md`.

## Impact assessment

- Code paths affected: client host, browser protocol, browser shell, and new
  workspace supervisor only.
- Data, protocol, or schema impact: browser snapshots gain safe workspace
  process status and a bounded diagnostics tail.
- Dependency or API impact: no new dependency; the existing official ACP SDK
  becomes the production transport implementation.

## Validation

- Run `bun run check`, `bun test`, and `bun run test:e2e` from `client/`.
- Run `make check-docs` and inspect the focused diff.
