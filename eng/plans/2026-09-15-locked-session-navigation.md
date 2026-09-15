# Disable sessions owned by another runtime

## Goal

Report when a durable session is exclusively owned by another Ox runtime and
render that conversation as a dimmed, non-interactive sidebar row instead of
letting the user attempt a load that must fail. Releasing the other runtime and
refreshing history makes the row available again.

## Related code

- `docs/spec.md` — Owns session locking and the web client's observable history
  behavior.
- `eng/architecture.md` — Owns the exclusive activation lock and ACP extension
  boundary.
- `eng/client-architecture.md` — Owns session catalog projection and navigation
  semantics.
- `internal/agent/store.go`, `internal/agent/lock.go`, and
  `internal/agent/agent.go` — Project durable session listings and enforce
  cross-runtime exclusion.
- `internal/acp/types.go` — Owns namespaced ACP metadata keys.
- `client/src/workspace-supervisor.ts` and `client/src/protocol.ts` — Translate
  ACP session metadata into browser-safe conversation state.
- `client/src/components/sidebar.tsx` — Renders conversation navigation.

## Decisions

- Preserve exclusive session ownership. Detect another runtime's lock with a
  non-blocking probe while building `session/list`, and expose only a namespaced
  boolean in the session's ACP `_meta`; do not add a nonstandard root field.
- A session already active in the listing agent remains available even though
  its log is locked by that agent. Lock availability is advisory, so a direct
  load still reports a race normally if ownership changes after the listing.
- Treat only the exact Ox metadata value as locked. Agents that omit or do not
  understand the extension keep the existing openable behavior.
- Represent the browser state explicitly as `locked`. A locked row has no `href`
  or open handler, uses disabled semantics and subdued styling, and says
  `Open in another client` so the reason is visible rather than hidden in a
  failed-command alert.

## Test plan

- Prove that `session/list` marks a session held by a second agent runtime,
  leaves a session owned by the listing runtime available, and clears the mark
  after the owner closes it.
- Prove ACP metadata is mapped to the browser's locked conversation status and
  accepted by snapshot validation without affecting agents that omit it.
- Add a real-process browser scenario whose locked conversation is dimmed and
  absent from link navigation while an available conversation still opens.
- Run the focused Go, Bun, and Playwright tests, then `make check-all` because
  the behavior spans an OS lock, the ACP boundary, the host, and the browser.

## Implementation plan

- Add a non-blocking lock-availability projection to durable session listing
  without changing activation or deletion locking.
- Publish the cross-runtime ownership fact through a namespaced `SessionInfo`
  metadata key, overriding it for sessions active in the current agent.
- Carry the fact through the workspace supervisor and browser protocol as the
  locked conversation state, preserving it when an attempted activation loses an
  availability race.
- Render locked conversations as labelled, dimmed non-links while leaving
  active, inactive, and loading rows unchanged.
- Add the agent, host/protocol, and browser regression coverage described above.

## Documentation updates

- Update `docs/spec.md` to define advisory locked-session listings and disabled
  web-client history rows.
- Update `eng/architecture.md` and `eng/client-architecture.md` with the lock
  projection and namespaced ACP metadata boundary.
