# Deterministic test signals

## Goal

Two tests wait on the clock for something the code could tell them directly.

An OpenRouter retry test serves `Retry-After: 2` and then asserts the call took
between 1.8 and 3.5 seconds. It spends two real seconds proving a value the
client already computes, and it fails on a machine that stalls for two more.

The integration harness collects session updates by sleeping 25 milliseconds and
assuming that a list which did not grow is a list that is finished. Nothing
connects that interval to the runtime, so the harness is slow when the interval
is generous and wrong when it is not.

## Desired outcome

The retry test asserts the delay the client chose, without waiting for it. The
harness returns when every update the server has written has arrived, and waits
no longer than that takes.

## Summary of approach

The client already routes every retry sleep through an injectable wait, and one
retry test captures the delay that way. Use the same seam for `Retry-After`.

Give the harness its own in-memory channel pair so it can see each message the
server writes before the client dispatches it. A request's response is written
after the updates that precede it, so once a call returns, the number of updates
the server sent is final; the harness waits for exactly that many to arrive.

## Related code

- `internal/openrouter/client_test.go` -
  `TestClientRetriesRetryAfterAndSucceeds`, and
  `TestClientRetriesTransientStatusesWithBoundedJitter`, which already captures
  the delay.
- `integration/agent_loop_test.go` - `newHarnessWithCallback` and `updates`.

## Current state

- Relevant existing behavior: `Client.wait` calls `retryWait` when it is set, so
  a test can observe the delay and return immediately.
- Existing patterns to follow: the harness already builds its client and server
  through `server.Local`, whose fields it can fill itself.
- Constraints from the current implementation: the client delivers a batch of
  received messages on its own goroutine, so a notification written before a
  response is not necessarily dispatched before that response is returned. The
  count has to be taken where the message is read, not where it is dispatched.

## Test plan

- **Key behaviors to verify:** a `Retry-After` header produces a two-second
  delay and one retry; every existing integration assertion about session
  updates still holds.
- **Test levels:** unit, in `internal/openrouter`; harness change in
  `integration`.
- **Edge cases and failure modes:** a received message that carries several
  JSON-RPC messages at once.
- **What not to test:** the harness itself.

## Implementation plan

- Capture the retry delay instead of sleeping through it.
- Build the harness channel pair directly and count the updates the server
  writes.
- Wait on that count in `updates`.

## Documentation updates

- Todo list item "Replace wall-clock test heuristics with deterministic signals
  (F45)".

## Impact assessment

- Code paths affected: none. Tests only.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the OpenRouter and integration suites, then the whole
  suite.
- Static checks: `make check-go`.
