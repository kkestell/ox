# Browser session lifecycle

## Goal

Give the first-party browser client one host-owned view of durable Ox sessions,
so a browser can create, page through, activate, close, delete, and reconnect to
sessions without becoming the source of truth.

## Desired outcome

After connecting to a configured workspace, every attached browser sees the same
paginated session catalog and selected active session. New sessions are active
immediately; loading attaches update routing before Ox replays; resume activates
without a replay; close releases host routing; and delete removes only the
inactive durable session. A refresh or a second browser receives the current
host snapshot rather than reconstructing state locally.

## Summary of approach

Extend the shared browser protocol with session catalog state and explicit
lifecycle commands. The workspace supervisor owns a small controller per active
session and registers its `session/update` route before a load or resume
request. It owns cursor pagination, refreshes after mutations, and publishes
immutable state changes. The host forwards those snapshots to every connected
browser; React renders semantic navigation and lifecycle controls only.

## Related code

- `client/src/protocol.ts` - Browser-safe session commands and snapshots.
- `client/src/workspace-supervisor.ts` - Ox ACP lifecycle calls and active
  session ownership.
- `client/src/host.ts` - Browser-command dispatch and fan-out snapshots.
- `client/src/browser.tsx` - Semantic session navigation and controls.
- `client/src/*test.ts`, `client/e2e/smoke.spec.ts` - Unit and real-process
  browser proof.
- `eng/client-architecture.md` - Host ownership and replay-routing boundary.

## Current state

- The host supervises and authenticates one initialized Ox process but exposes
  no session catalog or session routing.
- The SDK's active-session helper only attaches around `session/new`; loading
  therefore needs a host-owned notification route registered before its request.
- Ox supports cursor-paginated list, load/replay, resume, close, and delete.

## Structural considerations

- **Hierarchy:** The workspace supervisor remains the only owner of Ox and its
  active controllers; browser sockets only submit commands and render snapshots.
- **Abstraction:** A small active-session controller names update routing
  without prematurely implementing the transcript reducer owned by the next
  task.
- **Modularization:** Protocol validation, ACP lifecycle calls, host transport,
  and React rendering remain separate.
- **Encapsulation:** ACP updates and workspace paths stay inside the host;
  browser state contains only deliberate session summaries.
- **Testability:** The controller and supervisor are exercised with
  deterministic ACP fixtures; the browser harness proves refresh and two-browser
  coherence through a real Ox process.

## Test plan

- Verify browser command and snapshot validation, including invalid session IDs
  and cursor commands.
- Verify supervisor pagination, lifecycle state, early replay routing, failures,
  and cleanup with a deterministic ACP fixture.
- Verify a real Ox browser host can create, reload after browser refresh, attach
  two browsers to the same session state, and close/delete through visible
  controls.
- Do not duplicate transcript chunk-merging tests; the next task owns that
  projection.

## Implementation plan

- Add browser-safe session summary/catalog values and lifecycle commands.
- Add the active-session route and supervisor operations, capability checks,
  cursor ownership, mutation refreshes, and state publication.
- Dispatch lifecycle commands from the host and render unstyled semantic session
  navigation in React.
- Add focused unit coverage and extend the real-process Playwright fixture for
  coherent refresh and two-browser lifecycle behavior.
- Mark the completed roadmap child task and retain the architecture because its
  ownership model already covers this implementation.

## Validation

- Run `bun run check`, focused Bun tests, and the client Playwright suite.
- Run the repository's relevant formatting and documentation checks, then
  inspect the diff.
