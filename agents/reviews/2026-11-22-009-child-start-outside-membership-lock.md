# Review: a child's launch and first message happen outside the membership lock

## Scope and coverage

The uncommitted changes on `fix-f2` over `312f3da`, checked against
[the plan](../plans/2026-11-22-006-child-start-outside-membership-lock.md):
`internal/server/{mcp.go,methods.go,server.go}`, the server and forwarder
tests, `docs/architecture.md`, `docs/protocol.md`, and `docs/todo.md`. Traced
`addChild` against `Close`, archive of the parent and of an ancestor, destroy,
`acceptMessage`, `start`, and `handleMCP`.

Gaps:

- None material. The `make live` and `make e2e` results are the implementer's;
  they were not rerun here.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                 |
| -- | -------- | ----------- | -------- | ------ | ----------------------------------------------------- |
| F1 | Low      | Intent      | Rejected | —      | Forwarder tests changed outside the plan's file list  |
| F2 | Low      | Correctness | Rejected | —      | An unknown tool is refused before the session lookup |

## F1. Forwarder tests changed outside the plan's file list

Low · Intent · Rejected · `cmd/ox-mcp/main_test.go:320`

`TestForwarder` and `TestForwarderRefusesUnknownSession` asserted that `ping`
and `initialize` are refused during shutdown or for an unknown session. The
plan's "MCP handshake needs no membership" decision removes that behavior, so
the tests had to change. They now send `tools/call` and keep the `-32000` and
`-32001` coverage through the forwarder.

### Action

Rejected: the edit follows from a plan decision and keeps the same coverage.
The plan's file list missed it.

## F2. An unknown tool is refused before the session lookup

Low · Correctness · Rejected · `internal/server/mcp.go:88`

`tools/call` answers `-32602 unknown tool` before it checks shutdown and
membership, so an unknown tool from a missing session, or during shutdown,
gets `-32602` rather than `-32001` or `-32000`.

### Action

Rejected: `docs/protocol.md` fixes no order between these refusals, every
order is a correct answer to a malformed call, and checking the request's own
fields before reading state matches the other methods. The step-1 checks living
in `checkChildLocked`, with shutdown checked only at publication, is the plan's
Decision as written, and needs no change.

## Checks run

- `go vet ./internal/server ./cmd/ox-mcp`: clean.
- `go test -race` of `TestChildStartHoldsUpNoOtherSession`,
  `TestMCPHandshakeNeedsNoSession`, `TestCreateSession`, and
  `TestFailedLaunchKeepsItsErrorWhenDeleteFails`, and of `./cmd/ox-mcp`: pass.
- Spot-checked removals, each reverted:
  - Shutdown check at publication removed: the `start` case fails, the call
    returns the child.
  - Row delete on the shutdown refusal removed: the `start` case fails,
    `workspace/list` lists the child after a restart.
  - `s.mu` held from the store write through publication, as before the change:
    the `start` case fails in bounded time, `session/get` of the unrelated
    session gets no answer.
  - Session lookup restored ahead of the handshake:
    `TestMCPHandshakeNeedsNoSession` fails, `initialize` returns `-32001`.
- From the implementer: `make test` and `make e2e` passed on the final tree;
  `make live`'s `TestLive` passed, and `approval.spec.ts` passed 4/4 on a rerun
  after one failure matching the known issue "A plan written to a file shows no
  plan in its tool call".

## Verdict

Ready to commit. F1 and F2 are rejected; no finding needs a fix.
