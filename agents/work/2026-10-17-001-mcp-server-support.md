# Local MCP server support

## Plan

`agents/plans/2026-10-17-001-mcp-server-support.md`

## Summary

Implemented local MCP tools for ACP sessions and headless runs, including
configuration, discovery, approvals, result conversion, and process cleanup. The
plan's goal is met; changes are staged for review without a commit.

## Decisions

- Pinned the official Go SDK at v1.8.0 and selected protocol 2025-11-25. The
  newer protocol supports cached discovery, which could hide changes to tool
  permissions between turns. The selected protocol keeps discovery fresh.
- Wrapped the SDK command transport to terminate process groups while leaving
  protocol handling and the child's single wait to the SDK.

## Checks run

- Focused Go tests for settings, MCP clients, tool conversion, agent turns, ACP
  sessions, and headless runs passed.
- `make check` passed: Markdown, Go formatting, vet, all Go tests, and the
  Python evaluation tests.
- `go test -race ./internal/mcpclient ./internal/tools ./internal/agent
  ./internal/server ./cmd/ox-server`
  passed.
- The SDK drops empty embedded-resource text when re-encoding it. Added an
  adapter regression test so an empty text resource does not claim binary
  content was omitted.

## Manual verification

The shared Go subprocess fixture exercises real stdio without installed MCP
servers or network services. Reproduce the headless call and signal checks:

```sh
go test -count=1 ./cmd/ox-server -run TestHeadlessMCPCallAndSignalCleanup
```

Observed the final answer alone on stdout, startup cancellation without a model
request, cancellation during execution without replaying the call, and reaped
server processes on exit. ACP tests additionally cover connection reuse,
isolation, changed permissions, offline transcript replay, failed deletion, and
cleanup on close, successful deletion, and connection loss.
