# Browser session transcript projection

## Goal

Give every active browser session a host-owned, browser-safe projection of the
ACP updates Ox emits, so live streaming and replay render identically after a
refresh or a second browser attaches.

## Desired outcome

The selected active session renders user and agent content, thought chunks, tool
calls and their replaced output, the current plan, context usage and cost,
configuration options, and completed or failed tool outcomes. Chunks with the
same message identity append in arrival order; updates for the same tool-call
identity replace only the fields ACP supplies. A load replay follows the same
reducer as live traffic without duplicate entries. Unknown future update or
content shapes remain visible as an explicit safe fallback instead of breaking
the host or browser snapshot.

## Summary of approach

Replace the temporary raw-update collector in the session controller with a
transcript reducer that owns one immutable browser-safe projection per active
session. The workspace supervisor publishes after each reduced update and
exposes the selected controller's projection in the existing session snapshot.
The shared browser protocol validates the projection, and React renders it with
semantic HTML and native content elements. The controller will accept ACP's
known values directly, reduce future-shaped values defensively, and never expose
ACP metadata or raw inputs to the browser.

## Related code

- `client/src/session-controller.ts` - Current active-session update owner;
  becomes the transcript reducer.
- `client/src/workspace-supervisor.ts` - Routes notifications and publishes
  active-session state.
- `client/src/protocol.ts` - Browser-safe transcript snapshot types and
  validation.
- `client/src/host.ts` - Projects the supervisor state into revisioned browser
  snapshots.
- `client/src/browser.tsx` - Unstyled semantic transcript rendering.
- `client/src/session-controller.test.ts` and `client/e2e/smoke.spec.ts` -
  Reducer and real-process browser coverage.
- `eng/client-architecture.md` - Owns transcript-reducer and replay-routing
  responsibilities.
- `references/repos/personal/alpha/extension/src/transcript.ts` - Prior art for
  identity-keyed chunk and tool-update merging.

## Structural considerations

- **Hierarchy:** The session controller remains the only owner of its reduced
  ACP state; the supervisor publishes snapshots, and React only renders them.
- **Abstraction:** A compact browser projection names the presentation contract
  without leaking ACP metadata, raw tool input/output, or SDK objects.
- **Modularization:** Reduction stays beside session routing; protocol
  validation and React rendering remain in their existing boundaries.
- **Encapsulation:** Unknown values are converted to a small labelled fallback,
  not forwarded as arbitrary protocol objects to browser clients.
- **Testability:** Pure reducer tests cover ordering, replacements, replay, and
  unknown values; the browser harness proves host publication and rendering
  through real Ox traffic.

## Test plan

- Verify user, assistant, and thought text chunks merge by message identity,
  while non-text content remains ordered and safely represented.
- Verify tool creation and sparse updates merge by tool-call identity, replace
  supplied content, and preserve terminal success or failure state.
- Verify plan, usage, and configuration updates replace their current
  projections and that a replayed duplicate does not create duplicate content.
- Verify unknown update and content shapes reduce to labelled fallback entries
  and browser protocol validation rejects malformed snapshot values.
- In Playwright, load a deterministic real Ox session and assert its replayed
  user and agent content, usage, and configuration survive a browser refresh.
  Focused reducer tests cover the remaining update kinds.
- Do not add prompting, cancellation, configuration mutation controls, or client
  filesystem and terminal callbacks; their dedicated tasks own those request
  lifecycles.

## Implementation plan

- Define bounded browser-safe transcript, tool, plan, usage, and configuration
  projection values in the shared protocol and add them to the selected active
  session snapshot.
- Implement identity-keyed transcript reduction in the session controller,
  including defensive conversion of known ACP content and extensible values.
- Publish controller changes through the supervisor and host snapshots while
  retaining early routing before `session/load` replay begins.
- Render the selected transcript with unstyled semantic HTML, including native
  image, audio, resource, plan, usage, configuration, and tool-outcome views.
- Add reducer/protocol tests and extend the real-process browser fixture for
  live and replayed transcript projection.
- Mark the completed roadmap child task in `eng/todo.md`.

## Documentation updates

- Mark the completed browser-client transcript task in `eng/todo.md`.

## Impact assessment

- Code paths affected: client session controller, supervisor, shared browser
  protocol, host snapshots, React shell, and focused client tests.
- Data, protocol, or schema impact: snapshots gain a selected active session's
  browser-safe transcript projection; ACP stays unchanged.
- Dependency or API impact: no new dependency or Ox API.

## Validation

- Run `bun run check`, `bun test`, and `bun run test:e2e` from `client/`.
- Run `make check-docs` and inspect the focused diff.
