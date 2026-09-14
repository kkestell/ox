# Browser client foundation

## Goal

Establish the first-party browser client's buildable, testable foundation before
adding Ox process supervision or session behavior.

## Desired outcome

`client/` is a Bun, TypeScript, and React package that serves an accessible,
unstyled shell from a loopback-friendly same-origin host. Browser messages are
validated at the host boundary, rejected cross-origin WebSocket upgrades cannot
reach host state, and focused unit, type, and Playwright gates prove the
foundation. The browser harness also proves its deterministic provider fixture
can drive the shipped Ox binary through the official ACP TypeScript SDK.

## Summary of approach

Create one package with a small shared browser protocol, a Bun static host, and
a React shell. The host owns complete snapshots and revision assignment now,
while its only command is a typed health ping; later slices extend this protocol
and connect workspace supervision behind it. A Playwright harness starts the
compiled host, validates the shell in Chromium, and independently runs a real Ox
prompt against a local scripted OpenRouter endpoint through an SDK client.

## Related code

- `eng/client-architecture.md` - Defines the client ownership, same-origin
  protocol, unstyled functional milestone, and test boundary.
- `docs/spec.md` - Owns the observable browser-client contract.
- `internal/e2e/harness_test.go` - Supplies the process configuration pattern
  for a deterministic Ox invocation.
- `references/repos/third-party/protocol/typescript-sdk` - Provides the official
  ACP TypeScript SDK and stdio client pattern.

## Current state

- Ox has no `client/` package or browser gate.
- The existing Go process harness already demonstrates the temporary settings,
  credential, model catalog, and provider configuration a real Ox process needs.
- The client architecture reserves process supervision, ACP initialization,
  sessions, and callbacks for subsequent tasks.

## Structural considerations

- **Hierarchy:** The Bun host owns the browser connection and snapshots; React
  only renders protocol state.
- **Abstraction:** A compact protocol module names the browser boundary without
  prematurely modeling sessions or ACP values.
- **Modularization:** Host, protocol, browser shell, and browser harness are
  separate focused entry points in the single client package.
- **Encapsulation:** The browser sees a health snapshot only. It receives no
  filesystem paths, process handles, credentials, or ACP transport details.
- **Testability:** Protocol parsing is pure, while browser and Ox behavior use
  their real public process and network boundaries.

## Test plan

- Reject malformed, unknown, and invalid request-ID browser commands; accept a
  valid ping.
- Reject a WebSocket whose origin differs from the host's origin and send a
  complete snapshot plus a correlated ping result to an allowed browser.
- In Playwright, render the semantic shell and connected status from a real
  same-origin WebSocket.
- Drive a freshly built Ox binary through initialize, session creation, and a
  prompt using the official SDK and a fake OpenRouter SSE response; assert the
  fixture receives exactly one provider request.
- Do not test process supervision, session UI, prompt controls, or callback
  behavior before their dedicated tasks.

## Implementation plan

- Add the Bun package manifest, locked dependencies, TypeScript configuration,
  build/type/test scripts, and generated-artifact ignore rules.
- Define browser-safe discriminated command, snapshot, and result values with
  runtime validation and static types.
- Implement the same-origin Bun host and its immutable asset and health
  surfaces; open WebSockets receive a complete snapshot and validated pings
  receive a correlated result.
- Add an unstyled semantic React shell that renders connection state without
  importing host or ACP modules.
- Add Bun protocol/host tests and a Playwright harness with the deterministic
  OpenRouter fixture and real SDK-driven Ox smoke path.
- Mark the completed roadmap child and retain the package gates as focused
  commands until the final client task integrates them into repository-wide
  checks.

## Documentation updates

- Mark the completed client-foundation item in `eng/todo.md`.

## Impact assessment

- Code paths affected: New browser-client package only, plus the roadmap.
- Data, protocol, or schema impact: Introduces a private, versionless,
  same-origin browser protocol with only browser-safe health values.
- Dependency or API impact: Adds client-local Bun dependencies, including the
  official ACP TypeScript SDK, React, Zod, and Playwright.

## Validation

- Run `bun run check`, `bun test`, and `bun run test:e2e` from `client/`.
- Run the repository documentation check after updating the roadmap.
