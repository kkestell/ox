# Browser client functional gate

## Goal

Finish the first-party browser client's functional milestone: prove its ACP and
reconnect behavior at the real browser boundary, make its focused checks part of
the repository gates, and document how to run it safely.

## Desired outcome

A developer can start the browser client for one local workspace, deliberately
bind it to a trusted network when needed, and rely on repository checks to run
its type, unit, build, and browser-process coverage. The client remains an
unstyled semantic application.

## Summary of approach

Audit the existing real-process Playwright scenarios against the advertised ACP
surface and add only missing reconnect or failure scenarios. Add a single
client-gate Make target and include it in the normal fast and complete gates.
Document the host command, browser access model, workspace selection, and
trusted-network boundary in an end-user guide. Then review the completed client
milestone for redundant state, unsafe browser exposure, missing capability
coverage, and unintended CSS.

## Related code

- `client/e2e/smoke.spec.ts` — public browser-to-host-to-Ox behavior.
- `client/src/{host,workspace-supervisor}.ts` — command line startup,
  supervision, snapshots, and reconnect behavior.
- `client/package.json` — focused client checks.
- `Makefile` — repository developer and CI gates.
- `docs/spec.md#web-client` — shipped web-client product contract.
- `eng/client-architecture.md` — client ownership and coverage boundary.

## Current state

- The browser harness already drives real Ox processes through the complete
  session, prompt, interaction, filesystem, and terminal flows.
- The client package has independent type, unit, build, and Playwright scripts,
  but no Make target invokes them.
- The product contract describes local and trusted-network behavior, but there
  is no end-user startup guide.

## Structural considerations

- **Hierarchy:** The Bun host stays the sole owner of process and ACP state;
  browser tests use only its public HTTP and WebSocket surfaces.
- **Abstraction:** Make invokes existing package scripts rather than recreating
  a second test runner or client build pipeline.
- **Modularization:** Browser fixtures remain in the existing smoke harness.
- **Encapsulation:** Documentation makes clear that an explicit non-loopback
  bind is safe only on a trusted network and does not add authentication or TLS.
- **Testability:** The browser matrix uses deterministic fake-provider responses
  and isolated temporary workspaces.

## Test plan

- Verify every advertised/requested ACP surface has a real browser scenario,
  including process exit, browser refresh, second-browser interaction races, and
  reconnect/reload behavior.
- Verify the focused Make target runs type checking, pure tests, the browser
  build, and Playwright coverage.
- Verify the end-user guide's command line and bind behavior through focused
  host tests or existing browser scenarios; do not test browser styling.

## Implementation plan

- Complete the MCP activation browser scenario that precedes this gate, then
  audit the e2e matrix for uncovered functional or reconnect behavior.
- Add any focused browser scenarios required by that audit.
- Add and integrate the client gate in `Makefile`.
- Write the browser-client startup and trusted-network guide, link it from the
  README, and mark the completed client tasks in `eng/todo.md`.
- Perform the milestone completeness and simplification review, fix findings,
  and run the affected gates.

## Documentation updates

- Add the browser-client startup guide and README link.
- Mark completed client milestone tasks in `eng/todo.md`.

## Impact assessment

- Code paths affected: browser test harness, repository checks, and docs.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Run the client type, unit, build, and Playwright commands through the new Make
  target.
- Run `make check-all` before marking the behavior complete.
