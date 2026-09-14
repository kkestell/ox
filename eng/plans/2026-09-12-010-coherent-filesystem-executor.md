# Coherent filesystem executor

## Goal

ACP lets a client advertise `fs/read_text_file` and `fs/write_text_file`
independently, and Ox selects each executor on its own. Read evidence and the
mutation that consumes it can then come from different filesystems.

A client that advertises only read serves `read_file` from its editor buffer and
records a hash of unsaved text, while every write and exact edit reads and
replaces the file on disk. The hashes never agree, so every mutation is refused
as stale. A client that advertises only write is the mirror: Ox verifies the
file on disk and then writes somewhere else, which can discard the buffer's
unsaved changes.

## Desired outcome

Ox uses one filesystem for a session's reads and mutations. A client that
advertises exactly one half of the pair is refused at `initialize` with a
message naming what is missing, rather than accepted into a session whose every
edit fails.

## Summary of approach

Treat the filesystem read and write capabilities as one capability. `initialize`
rejects a client that advertises one without the other. Everything downstream is
unchanged, because the pair is then always both or neither.

## Related code

- `internal/agent/agent.go` - `Initialize` records client capabilities;
  `negotiatedExecutorCapabilities` and `clientFileSystem` select the executor.
- `internal/tools/text.go` - `acquireText` falls back to the local read when the
  client advertises no read method.
- `internal/tools/write.go` and `internal/tools/edit.go` - the evidence rule
  those executors have to satisfy.

## Current state

- Relevant existing behavior: with neither capability Ox uses the local executor
  for reads and mutations, which is already coherent.
- Existing patterns to follow: `Initialize` already returns
  `jrpc2.InvalidParams` for a request that fails validation.
- Constraints from the current implementation: capabilities are frozen into each
  activation, so the check belongs at `initialize` rather than per session.

## Test plan

- **Key behaviors to verify:** read-only and write-only filesystem capabilities
  are refused at `initialize` with a message naming the missing method, and a
  client advertising both still reads its own buffer and writes back to it when
  that buffer differs from the file on disk.
- **Test levels:** unit in `internal/agent`, end-to-end in `internal/e2e`.
- **Edge cases and failure modes:** neither capability advertised stays valid.
- **What not to test:** the local executor's own evidence rule, already covered.

## Implementation plan

- Reject a half-advertised filesystem capability in `Initialize`.
- Add the unit coverage for both halves and for neither.
- Add an end-to-end test where the client's buffer differs from the file on
  disk, proving evidence and mutation both use the client.

## Documentation updates

- `docs/spec.md` and `eng/architecture.md` state that filesystem delegation is
  all or nothing.
- Todo list item "Keep read evidence and write verification on a coherent
  filesystem executor (F10)".

## Impact assessment

- Code paths affected: `initialize` capability handling.
- Data, protocol, or schema impact: none. A previously accepted capability
  combination is now refused.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the agent, tools, integration,
  and end-to-end suites.
- Static checks: `make check-go`.
