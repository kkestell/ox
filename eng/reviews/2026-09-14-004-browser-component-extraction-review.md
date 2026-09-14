# Browser component extraction review

Scope: unstaged changes to `client/src/browser.tsx`, the new
`client/src/components/` modules, and `eng/client-architecture.md`.

Mode: general.

## Reviewed topics

- Correctness — the extraction retains command routing, selected-workspace
  binding, snapshot rendering, and transcript interactions at the browser
  protocol boundary.
- Security — the surface handles credentials, MCP secrets, browser-selected
  files, and agent-provided transcript content.
- Testing — this is a browser behavior-preserving refactor whose ACP-visible
  paths require client and end-to-end coverage.
- Readability — the change divides one large browser entry point into modules
  with focused presentation and connection responsibilities.
- Architecture — the component boundaries must continue to match the client
  architecture's ownership model.
- Documentation — the architecture description changes with the browser module
  boundary.

## Findings

No findings.

## Topic verdicts

- Correctness: the extracted callbacks preserve the original commands, workspace
  identifiers, session identifiers, request correlation, and display conditions.
  Success, missing-selection, and rejected-command paths continue to reach the
  same state and messages.
- Security: credentials and MCP secret values remain write-only browser command
  inputs; agent-provided resource links retain their HTTP(S)-only rendering
  restriction; attachment bounds and capability checks remain unchanged.
- Testing: the existing unit suite and Playwright suite cover the changed
  surface's registration, authentication, lifecycle, prompting, callback,
  interaction, multi-browser, and multi-workspace paths. No behavior changed
  that requires a new test.
- Readability: `App` now coordinates host state and commands, while the
  connection hook and each rendered region have focused owners. The resulting
  boundaries make the browser entry point a simple mount.
- Architecture: the move implements the documented React-application boundary;
  it adds no protocol, host, or domain-state owner.
- Documentation: `eng/client-architecture.md` accurately records the new mount,
  component, and connection-hook split without duplicating implementation
  mechanics.

## Unresolved suspicions

None.

## Checks run

- `git diff --check` — passed.
- `bun run check` — passed (Tailwind build, browser bundle, and TypeScript).
- `bun test --path-ignore-patterns 'e2e/**'` — passed, 93 tests.
- `bun run test:e2e` — passed the Playwright browser suite.

The first unit-suite attempt under the restricted sandbox could not bind Bun's
loopback test server (`EADDRINUSE`); the same suite passed once run with normal
local networking.
