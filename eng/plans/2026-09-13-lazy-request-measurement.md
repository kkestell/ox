# Lazy request measurement

## Goal

Every provider request marshals the whole request to count its bytes for the
diagnostic trace. Go evaluates that argument before the call, so the work
happens even when tracing is off and the trace returns immediately.

The same loop recomputes the request's prefix fingerprint for every request it
sends, although the turn's frozen configuration is what the fingerprint hashes
and that cannot change inside a turn.

## Desired outcome

A request is not encoded for a trace nobody is recording, and a fingerprint that
is constant for a turn is computed once for that turn.

## Summary of approach

Let a turn report whether it is being traced, and count request bytes only then.
Hoist the prefix fingerprint out of the request loop.

## Related code

- `internal/trace/trace.go` - `Turn`, `Turn.Provider`.
- `internal/agent/loop.go` - `runFrom`'s request loop, `providerRequestBytes`,
  `prefixFingerprint`.
- `internal/agent/compact.go` - the compaction request's trace.

## Current state

- Relevant existing behavior: every trace method already returns immediately
  when the sink is absent, so only its arguments cost anything.
- Existing patterns to follow: `ProviderRequest.Complete` guards on a nil field
  for the same reason.
- Constraints from the current implementation: the fingerprint reads the frozen
  turn configuration, which a configuration change cannot alter mid-turn.

## Test plan

- **Key behaviors to verify:** a turn reports whether it is traced, and a traced
  run still records request byte counts.
- **Test levels:** unit, in `internal/trace`; the existing trace end-to-end test
  already asserts recorded byte counts.
- **Edge cases and failure modes:** the zero `Turn`, which callers hold when no
  trace is configured.
- **What not to test:** the byte counts themselves, which are unchanged.

## Implementation plan

- Add `Turn.Enabled` and guard both request byte counts with it.
- Compute the prefix fingerprint once per turn.

## Documentation updates

- Todo list item "Eliminate redundant provider-request encoding (F16)".

## Impact assessment

- Code paths affected: provider request dispatch and compaction.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the trace test, then the agent, integration, and
  end-to-end suites.
- Static checks: `make check-go`.
