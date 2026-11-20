# Review: `oxd` serves MCP to a forwarder

## Scope and coverage

Uncommitted changes on base `099f708` (main): `AGENTS.md`, `Makefile`,
`cmd/ox/web/e2e/global-setup.ts`, `docs/architecture.md`, `docs/protocol.md`,
`internal/server/server.go`, and the new `cmd/ox-mcp/` and
`internal/server/mcp.go`. Checked against
`.agents/plans/2026-11-19-004-oxd-mcp-forwarder.md` (Task 1 of
`.agents/scratch/agent-tools.md`), with the callers and state the change
reaches: `serveConn`, `broadcast`, `conn.queue`, `handle`, `Server.Close`,
`jsonrpc.Parse`, and the relay's `/events` and `/rpc` in `cmd/ox/main.go`.

The change delivers the plan's Goal and keeps its Decisions: attach on the
first line only, removal from `s.conns` under `connsMu` before the result is
queued, MCP answered on the connection goroutine from `s.mu` membership and
the immutable `s.config`, and the forwarder's discard, half-close, and exit
behavior. No requirement changes, and `docs/functional-requirements.md` is
untouched, as the plan says.

Gaps: none material.

## Summary

| ID | Severity | Category | Action | Status | Title                                                   |
| -- | -------- | -------- | ------ | ------ | ------------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Resolved | Shutdown and `protocolVersion` refusals have no test     |

## F1. Shutdown and `protocolVersion` refusals have no test

Low · Testing · Fix · Resolved · `internal/server/mcp.go:51`, `internal/server/mcp.go:62`

Two conditions `handleMCP` adds, both specified by the plan and documented in
`docs/protocol.md` ("MCP methods"), can be removed without any test failing:

- `case closed: reply(shuttingDown(id))` — an MCP request while the daemon is
  shutting down returns `-32000` `daemon is shutting down`.
- `|| input.ProtocolVersion == ""` — `initialize` without a string
  `protocolVersion` returns `-32602` `protocolVersion is required`.

### Evidence

Mutation runs on a copy of the tree, `go test -count=1 ./cmd/ox-mcp`:
`case closed:` changed to `case false && closed:` passes; the
`ProtocolVersion == ""` clause removed passes. Every other condition the
change adds fails a test when removed (see Checks run).

### Action

Keep both in `cmd/ox-mcp/main_test.go`, which already reaches these paths:

- In `TestForwarder`, before the valid `initialize`, call `initialize` with
  `{}` and require `-32602`.
- Have `startOxd` also return the `*server.Server`. In `TestForwarder`, after
  the existing checks and before closing stdin, call `s.Close()` and require
  `ping` to answer `-32000` `daemon is shutting down`. A second `Close` from
  the cleanup is safe: `post` returns false on an ended session and `done` is
  already closed.

## Checks run

- `make test`: passed, including `cmd/ox-mcp`.
- `make e2e`: passed (16 passed, 3 skipped). It drives the page in headless
  Chrome against the race-built binaries, which covers the `serveConn` change
  on the page's path; the build now includes `./cmd/ox-mcp`.
- `make live`: not needed; nothing in how Ox drives Claude Code changes.
- New tests at base: `cmd/ox-mcp` does not exist at `099f708`, so every new
  test depends on the change.
- Conditions removed one at a time on a copy of the tree (the working tree was
  not edited):
  - `delete(s.conns, c)` in `attach`: `TestForwarder` fails (`tools/list`
    reads the `workspace/created` notification).
  - first-line-only attach: `TestForwarder` fails (late `mcp/attach` answers
    `{}`).
  - empty `session_id` refusal: `TestForwarderRefusesUnknownSession` fails.
  - ignoring ID-less lines in `handleMCP`: `TestForwarder` fails.
  - `sess == nil` check: `TestForwarderRefusesUnknownSession` fails.
  - unknown tool refusal: `TestForwarder` fails.
  - the forwarder's discard of lines before the attach result:
    `TestForwarderDropsLinesBeforeAttach` fails.
  - the forwarder's `CloseWrite`: `TestForwarder` fails (stdout never ends).
  - `closed` check and `protocolVersion` check: pass (F1).

## Verification 1

Uncommitted changes on base `099f708`. Since the review, only `cmd/ox-mcp/main_test.go` changed; `internal/server/mcp.go` is byte-identical to the reviewed state and the tracked diff is unchanged.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `TestForwarder` now calls `initialize` with `{}` and requires `-32602`, and after `s.Close()` requires `ping` to answer `-32000` `daemon is shutting down`. On a copy of the tree, removing the `closed` check fails at `main_test.go:302` and removing the `protocolVersion` check fails at `main_test.go:233`; both pass with the conditions in place. |

No new findings: the test change adds a `*server.Server` return to `startOxd` and the two checks, and the second `Close` from cleanup is safe.

Checks: `make test` passed; `make e2e` passed (16 passed, 3 skipped). `make live` not needed.

## Verdict

Ready to commit.
