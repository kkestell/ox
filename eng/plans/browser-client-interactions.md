# Add browser-client pending interactions

## Goal

Let the first-party browser ACP client faithfully present and resolve Ox
permission requests and form elicitations without making a browser connection
the authority for a live turn.

## Desired outcome

The host advertises the implemented permission and form capabilities, retains
each pending callback in its session controller, and publishes browser-safe
interaction data in every snapshot. Any attached browser can select an exact
permission option or submit, decline, or cancel a schema-valid form. The first
valid reply settles the ACP request; a later reply reports a stale interaction.
Refreshing a browser does not dismiss an interaction, while cancellation by Ox,
session cancellation, process exit, or host shutdown removes it and completes
the callback as cancelled.

## Summary of approach

Add a browser-protocol interaction projection and correlated answer commands.
The workspace supervisor registers the two ACP request handlers before
initialization and routes each callback to the owning active session controller.
A concrete pending-interaction owner validates the supported form schema and
browser answers, publishes changes, races an answer with the ACP abort signal,
and resolves exactly once. React renders permission options verbatim and native
controls derived from the browser-safe form schema; it holds only a draft, while
the host retains the interaction. The client advertises only form elicitation,
not URL elicitation.

## Related code

- `client/src/protocol.ts` - browser-safe interaction projections and answer
  commands.
- `client/src/session-controller.ts` - session-owned pending interaction state.
- `client/src/workspace-supervisor.ts` - ACP handlers, capability advertisement,
  callback routing, cancellation, and active-session lifecycle.
- `client/src/host.ts` - command dispatch.
- `client/src/browser.tsx` - semantic permission and schema-derived form UI.
- `client/src/*.test.ts` and `client/e2e/smoke.spec.ts` - protocol, race, and
  real-process browser proof.
- `internal/tools/question.go` and `internal/acp/validate.go` - Ox's current
  single-answer form contract.

## Current state

- The host owns active session controllers and snapshots survive browser
  reconnects, but it currently advertises neither callback capability.
- Ox already emits standard permission callbacks and form elicitation for its
  question tool, and validates an accepted answer strictly.
- The ACP SDK supplies a per-callback abort signal; permission cancellation must
  return the standard cancelled outcome.
- Alpha's approval surface confirms host-side presentation patterns, but this
  browser client needs the stronger multi-browser ownership and stale-answer
  semantics defined in `eng/client-architecture.md`.

## Structural considerations

- **Hierarchy:** The supervisor remains the sole ACP connection owner;
  controllers own interactions for their sessions; React only renders snapshots
  and submits answers.
- **Abstraction:** One concrete interaction owner handles both callback
  lifecycles and their shared resolve-once behavior, without turning browser
  sockets into callback state.
- **Modularization:** Browser-safe schema conversion and answer validation stay
  at the shared protocol boundary, leaving ACP SDK values at the supervisor
  boundary.
- **Encapsulation:** Snapshots exclude ACP metadata, raw request handles, and
  arbitrary schema extensions. Browser answers cannot select an unoffered option
  or add form fields.
- **Testability:** Pure protocol/controller tests cover schemas and races; the
  supervisor test program and Playwright exercise the real ACP callback path.

## Test plan

- Validate interaction snapshots and bounded answer commands; reject unknown
  interaction IDs, unoffered permission options, malformed schemas, invalid form
  values, and browser-added fields.
- Verify the supervisor advertises only permission and form capabilities,
  retains callbacks through an observer refresh, accepts only the first valid
  answer across concurrent browsers, and answers cancellation with the ACP
  cancelled forms.
- Exercise exact permission option and tool presentation, required text and
  single-selection form rendering, decline and cancel actions, and stale-answer
  reporting through Playwright against a real Ox process and deterministic
  provider responses.
- Do not test URL elicitation or generic arbitrary JSON-schema extensions;
  neither is advertised by this client.

## Implementation plan

- Define the browser-safe permission and form projections, exact answer command
  unions, and their validation limits.
- Add a session-owned pending interaction owner that converts supported ACP
  callbacks, publishes state changes, validates answers, and races callback
  cancellation against first-answer-wins settlement.
- Register and advertise the ACP permission and form handlers in the workspace
  supervisor; route browser answers, turn cancellation, session close, process
  failure, and shutdown through the interaction owner.
- Route the answer commands through the host and render semantic permission
  options and native schema-based form controls in React.
- Add focused unit and browser-harness coverage, then mark the completed task in
  `eng/todo.md`.

## Documentation updates

- Mark the completed browser-client interaction task in `eng/todo.md`.

## Impact assessment

- Code paths affected: browser protocol, session state, ACP callback routing,
  host dispatch, React rendering, and client test fixtures.
- Data and protocol impact: additive browser snapshot fields and answer
  commands; standard ACP `session/request_permission` and `elicitation/create`
  traffic only.
- Dependency impact: none.

## Validation

- Run `bun run check`, `bun test --path-ignore-patterns e2e`, and the focused
  Playwright client suite from `client/`.
