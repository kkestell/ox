# A relay 503 ends the page's stream

## Scope and coverage

Reviewed the uncommitted changes in the `relay-503-reconnect` worktree against
base `566ec77` (`git merge-base origin/main HEAD`): `cmd/ox/web/src/connection.ts`,
`cmd/ox/web/e2e/session.spec.ts`, and `cmd/ox/web/e2e/workspace.spec.ts`,
checked against
[the plan](../plans/2026-11-24-relay-503-reconnect.md). Also traced the
callers that read `Disconnected` (`retry`, `reload`, `load`, and `onError` in
`cmd/ox/web/src/ox.tsx`, and `createPoll`), the relay's `503` paths in
`cmd/ox/main.go`, and `docs/architecture.md` ("Connection and reload").

The change resolves #84. It touches neither `ox.tsx` nor `poll.ts`, so the
sibling issue #86 is unaffected.

Gaps:

- `/ox-work`'s final response did not reach this review, so its Checks and
  Tests proved could not be compared. The checks below were run here instead.
  Whether the headless Chrome check through `make dev`, which `AGENTS.md`
  requires for a change to how the page reaches `oxd`, was run is unknown. The
  e2e suite drives the same path against real `oxd` and relay binaries,
  including a relay restart.

## Summary

No findings.

The change does what the plan's Decisions say. `send` handles a `503` and a
rejected fetch through one `lost()`, and `drop` stays the only code that ends
the stream. A `503` on the current stream drops it, fails pending calls, shows
Reconnecting, and reopens the stream after a second. A `503` on a stream that
has already ended rejects only its own call. Other non-OK statuses still
reject only their call with the relay's text. No requirement text changes,
which matches the plan's empty Requirement changes. **Lost connection** and
**Send cut off** hold. `docs/architecture.md` and the relay's comment already
describe a `503` as a dropped connection.

## Checks run

- `make test`: passed (Go packages, lint, and Node suites).
- `make e2e`: 177 passed, 4 skipped.
- With `connection.ts` reverted to base, run against the new and rewritten
  tests: "a 503 on a catch-up request reconnects the page",
  "a 503 on a send reconnects the page without resending", and
  "a lost connection keeps the last listing after a 503" fail. The
  aborted-POST variant passes, as the plan expects.
- With `lost()` changed to always `drop`, "a request cut off after its
  connection was replaced leaves the new connection alone" fails. The guard
  for an ended stream is protected.
- `make live`: not run, because the change does not alter how Ox drives
  Claude Code.

## Verdict

Ready to commit.
