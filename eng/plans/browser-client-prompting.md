# Add browser-client prompting and controls

## Goal

Let the first-party browser client submit every prompt block type Ox advertises,
cancel a live session turn, and select its durable mode, model, and reasoning
configuration.

## Desired outcome

An active session has a native composer, capability-aware attachments, and
configuration controls. The host sends validated ACP prompt and configuration
requests, publishes live turn state, and permits turns in distinct sessions to
run concurrently while refusing a second turn in one session. A cancellation
stops the owning ACP turn without coupling it to browser disconnects.

## Summary of approach

Extend the browser protocol's transcript configuration projection with the
select choices needed to render controls and add bounded prompt, cancel, and
configuration commands. The workspace supervisor records prompt capabilities at
initialization, owns one active prompt promise per session controller, and
routes prompt, cancellation, and option requests through the existing ACP
connection. The React surface builds prompt blocks from text, native file
attachments, and resource-link inputs only when the agent advertises the
corresponding capability.

## Related code

- `client/src/protocol.ts` - browser-safe snapshots and inbound commands.
- `client/src/session-controller.ts` - converts ACP configuration values into
  the transcript projection.
- `client/src/workspace-supervisor.ts` - owns the ACP connection and sessions.
- `client/src/host.ts` - dispatches validated browser commands.
- `client/src/browser.tsx` - semantic composer and controls.
- `client/src/*.test.ts` and `client/e2e/smoke.spec.ts` - unit and real-process
  proof of the browser contract.

## Current state

- Session updates, replay, and configuration current values already reduce into
  host-owned transcript state.
- The host serializes session lifecycle operations globally, which is suitable
  for catalog changes but not prompt turns in different sessions.
- Ox exposes `mode`, `model`, and `reasoning` as configuration options and
  rejects a second prompt for a busy session.

## Structural considerations

- **Hierarchy:** The supervisor remains the sole ACP owner; React only sends
  browser protocol commands and renders snapshots.
- **Abstraction:** Turn ownership is session-local, while catalog mutation
  remains on the existing serialized lifecycle path.
- **Modularization:** Prompt block validation and browser-safe option metadata
  stay in the shared protocol; attachment conversion remains in React.
- **Encapsulation:** Files selected in the browser cross only as bounded prompt
  payloads, never as host file paths or handles.
- **Testability:** Unit tests drive a small ACP subprocess for host ownership,
  and Playwright drives real Ox with a deterministic held provider.

## Test plan

- Validate bounded prompt, cancellation, and configuration commands, including
  malformed block payloads and unavailable attachment types.
- Verify the supervisor sends the right ACP methods, publishes active-turn
  state, accepts same-session cancellation, and refuses a second local prompt
  before it reaches Ox.
- Verify option choices survive new, load, resume, and live configuration
  updates.
- Drive real Ox in Playwright for text prompting, controls, cancellation, every
  supported prompt block type, two concurrent sessions, and same-session
  refusal.

## Implementation plan

- Extend browser protocol types and transcript option projection with bounded
  prompt blocks, option choices, prompt capabilities, and turn state.
- Add per-session prompt and cancellation ownership to the workspace supervisor
  and retain agent prompt capabilities from initialization.
- Route the new commands through the host and render semantic configuration and
  capability-aware composer controls in React.
- Add focused unit coverage and deterministic real-process browser scenarios.
- Mark the completed browser-client prompting task in `eng/todo.md`.

## Documentation updates

- Mark the completed task in `eng/todo.md`.

## Impact assessment

- Code paths affected: client browser protocol, host dispatcher, session
  projection, ACP supervisor, React surface, and client tests.
- Data and protocol impact: additive browser commands and snapshot fields; ACP
  traffic remains standard `session/prompt`, `session/cancel`, and
  `session/set_config_option`.
- Dependency impact: none.

## Validation

- Run `bun run check`, `bun test --path-ignore-patterns e2e`, and the focused
  Playwright client suite from `client/`.
