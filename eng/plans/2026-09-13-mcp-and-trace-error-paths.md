# MCP and trace error paths

## Goal

Two MCP branches are never reached by a test. A server that negotiates a
protocol revision Ox does not speak is refused at activation, and a tool call
whose arguments are not a JSON object is refused before the call goes out;
neither refusal has a test.

Trace tool spans are bookkeeping: a start time is filed under the session, turn,
and call, and the completion that finds it reports how long the tool ran.
Nothing tests that pairing. The end-to-end trace check asserts
`elapsed_ms >= 0`, which the trace guarantees for every record including one
whose span was never opened, so the assertion cannot fail.

## Desired outcome

Both MCP refusals are covered at the package boundary. Tool span timing is
covered where the start time can be controlled, and the end-to-end check asserts
a relation that a broken span would break.

## Summary of approach

Drive the protocol mismatch through a real client by rewriting the versions the
fixture server advertises, so negotiation settles on a revision Ox refuses.
Cover the argument refusals directly through `Bundle.Call`.

For the trace, seed a tool span's start time in the past and assert the reported
duration, then assert what happens when a completion finds no span, when a
second completion arrives, and when two turns use the same call identifier. In
the end-to-end trace, assert the tool's span fits inside the turn's.

## Related code

- `internal/mcp/mcp.go` - the revision check in `openServer` and the argument
  decoding in `Bundle.Call`.
- `internal/trace/trace.go` - `ToolStarted`, `ToolCompleted`, and the span map.
- `internal/e2e/trace_test.go` - the elapsed assertions.

## Current state

- Relevant existing behavior: a completion with no matching start reports zero
  rather than a duration measured from the zero time.
- Existing patterns to follow: the MCP tests already stand up a fixture server
  behind `httptest` and drive `Activate`.
- Constraints from the current implementation: the server advertises the
  revisions it supports and the client picks one, so the fixture has to change
  what is advertised rather than what is negotiated.

## Test plan

- **Key behaviors to verify:** activation refuses a server that settles on
  another revision; a call whose arguments are not a JSON object is refused; a
  tool span reports the time between its start and its completion, is removed
  once reported, and does not collide with the same call identifier in another
  turn.
- **Test levels:** unit, in `internal/mcp` and `internal/trace`; one assertion
  tightened in `internal/e2e`.
- **Edge cases and failure modes:** absent arguments, which are a valid empty
  object; a JSON `null`, which is not.
- **What not to test:** the negotiation itself, which belongs to the SDK.

## Implementation plan

- Add the protocol-revision and argument-refusal tests.
- Add the tool span bookkeeping test.
- Tighten the end-to-end elapsed assertions.

## Documentation updates

- Todo list item "Add focused MCP and trace error-path coverage (F44)".

## Impact assessment

- Code paths affected: none. Tests only.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the MCP, trace, and end-to-end
  suites.
- Static checks: `make check-go`.
