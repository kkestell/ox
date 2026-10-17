# MCP server support review

## Scope and coverage

Reviewed all staged changes against
`agents/plans/2026-10-17-001-mcp-server-support.md`, including settings, ACP and
headless activation, discovery, approval, dispatch, result conversion, process
ownership, tests, and user documentation. Traced the pinned SDK's transport,
decoding, and cancellation behavior and the existing session operation
lifecycle.

Gaps:

- Integration checks use real Go stdio subprocesses and a local model-provider
  fixture. No third-party MCP server or live model provider was exercised.
- Terminal rendering was not changed; the isolated tmux suite was skipped.

## Summary

| ID | Severity | Category    | Fix   | Title                              |
| -- | -------- | ----------- | ----- | ---------------------------------- |
| F1 | High     | Correctness | Fixed | SDK decoding changes MCP payloads  |
| F2 | Medium   | Concurrency | Fixed | Blocked writes ignore cancellation |

## F1. SDK decoding changes MCP payloads

High · Correctness · Fixed · `internal/mcpclient/client.go:219`

The adapter re-encoded SDK results after the SDK had decoded structured JSON
through floating-point values. Large integer identifiers and precise decimals
could therefore change before reaching the model. Discovery used the same lossy
path for input schemas. SDK decoding also rejected unfamiliar content types
before the adapter could produce its intended omission notice.

### Evidence

A real stdio reproduction returned `9007199254740992` for the server's
`9007199254740993`. A successful response with an unfamiliar content block
became an `unrecognized content type` error. Inspection confirmed that input
schemas passed through the same untyped JSON decoding.

### Fix

Preserve discovery and call payloads at the SDK transport boundary, while
leaving protocol framing, request completion, errors, and cancellation to the
SDK. Decode schemas, content blocks, and structured results as raw JSON. Keep
malformed payloads as failures and let the adapter render unsupported blocks
without changing the server's status. Add wire-level regressions for exact
numbers, unsupported content, empty resources, and malformed results, plus a
schema-number regression. The intended behavior was already specified, so no
decision was needed.

## F2. Blocked writes ignore cancellation

Medium · Concurrency · Fixed · `internal/mcpclient/process.go:58`

The SDK writes synchronously to the subprocess pipe. When a server stops reading
and a request fills the pipe, neither the call deadline nor prompt cancellation
interrupts that write. Session close and connection shutdown wait for the
prompt, so they can also remain stuck indefinitely.

### Evidence

A reproduction stopped the initialized server and sent a 1 MiB argument with a
100 ms deadline. The call remained blocked until the test resumed the server at
600 ms, returning after approximately 607 ms. The SDK checks cancellation before
writing but does not interrupt an in-progress pipe write.

### Fix

Observe cancellation while the SDK writes. If cancellation occurs during the
write, close the connection and terminate its process group with the existing
bounded grace period, then join the write. Closing is necessary because a
partial JSON frame cannot be reused safely. Add a regression that leaves the
server stopped, verifies bounded completion and process cleanup, and prevents
connection reuse. This follows the planned cancellation and cleanup behavior and
required no new decision.

## Checks run

- Temporary stdio reproductions confirmed F1 and F2; the blocked-write assertion
  failed before the fix. The temporary file was removed before fixing code.
- `go test ./internal/mcpclient ./internal/tools` passed.
- `go test -race ./internal/mcpclient ./internal/tools ./internal/agent
  ./internal/server ./cmd/ox-server`
  passed.
- `make check` passed.
- `make e2e` was skipped because no terminal behavior changed.

## Verdict

F1 and F2 are fixed. The reviewed implementation meets the plan within the
stated integration coverage.
