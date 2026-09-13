# Provider stream retry

## Goal

The provider client retries a failed stream unless it already handed content to
its caller. It decides that from whether the delta callback fired, and the
stream assembler calls that callback only for text and reasoning. Tool-call
fragments and reasoning-detail blocks are accumulated silently, so a stream that
produced a partial tool call before a transient failure is retried as if nothing
had been observed.

The same retry decision also tests whether the wrapped error's message contains
`read OpenRouter stream`. Rewording that wrap silently changes retry behavior.

## Desired outcome

Any observed response content makes a failed attempt final, whatever kind of
delta carried it. A read failure is recognized by an error value rather than by
its text.

## Summary of approach

Let the assembler record that it observed content, since it is what decodes
every delta, and have the attempt report that instead of whether the callback
fired. Give the SSE reader a sentinel for a transport read failure and test it
with `errors.Is`.

## Related code

- `internal/openrouter/client.go` - `Stream` retry loop and `streamAttempt`.
- `internal/openrouter/stream.go` - `streamAssembler.push`.
- `internal/openrouter/sse.go` - `readSSE` and `errStreamEnded`.

## Current state

- Relevant existing behavior: `errStreamEnded` already shows the shape a
  retry-classified condition takes.
- Existing patterns to follow: `classify` maps a status and error to a retry
  decision in one place.
- Constraints from the current implementation: the callback is optional, so the
  observation cannot depend on a caller supplying one.

## Test plan

- **Key behaviors to verify:** a stream carrying only tool-call fragments before
  a transient failure is not retried, the same holds for reasoning details, and
  a read failure is still retried.
- **Test levels:** unit, in `internal/openrouter`.
- **Edge cases and failure modes:** a stream that fails before any content still
  retries; a nil delta callback does not change the decision.
- **What not to test:** the retry delay schedule, which is unchanged.

## Implementation plan

- Record observed content in the assembler and report it from the attempt.
- Add a read-failure sentinel and classify with `errors.Is`.
- Add the tests above.

## Documentation updates

- Todo list item "Correct provider stream retry detection and classification
  (F13, F25)".

## Impact assessment

- Code paths affected: provider streaming retries only.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the openrouter, agent, and
  integration suites.
- Static checks: `make check-go`.
