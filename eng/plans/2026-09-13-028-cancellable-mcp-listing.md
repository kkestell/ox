# A cancellable MCP listing refresh

## Goal

Keep a bounded-age tool listing from making one MCP call wait on another's
network round trip, so cancellation reaches every call promptly.

## Desired outcome

A tool call whose context is cancelled returns at once, whatever another call
against the same server is doing. The listing window still saves a relisting per
call in a burst.

## Summary of approach

Hold the listing mutex only to read and to publish, never across
`discoverTools`. A caller that finds the listing expired performs its own
refresh under its own context, which is the behavior that shipped before the
window existed. Two calls arriving together after an expiry may both list; that
costs one extra listing and removes the only uninterruptible wait.

Publishing through one small method also gives activation and refresh the same
path, so no field is written outside the mutex.

## Related code

- `internal/mcp/mcp.go` - `currentTools`, `connectServer`, and `Bundle.Call`.
- `eng/architecture.md` - States that cancellation reaches child loops and tool
  work.

## Current state

- `currentTools` locks, checks freshness, and calls `discoverTools` with the
  mutex held. A second caller blocks on `sync.Mutex.Lock`, which no context can
  interrupt, until the holder's 120-second call deadline expires.
- The primary agent and a child may call the same server concurrently, and a
  child's tool call is documented never to wait on the primary.
- `connectServer` assigns `tools` and `listedAt` directly. That is safe only
  because the server is unpublished at the time.

## Structural considerations

- **Abstraction:** A narrower critical section, not a new coordination
  mechanism. Single-flighting the refresh would add machinery for a duplicate
  listing that is both rare and harmless.
- **Encapsulation:** One publish method means the listing fields have no writer
  outside the mutex.

## Test plan

- **Key behaviors to verify:** A call cancelled while another call is listing
  against the same server returns promptly; a burst still costs one listing; a
  redefinition is still rejected once the listing ages out.
- **Test levels:** A focused `internal/mcp` test with a fixture whose
  `tools/list` blocks after activation.
- **Edge cases and failure modes:** The blocked call must not be the one
  cancelled, or the test would pass against the current code.
- **What not to test:** Result rendering, bounds, and redaction, which this does
  not touch.

## Implementation plan

- Narrow the critical section and publish the listing through one method used by
  both activation and refresh.
- Add the cancellation test.

## Documentation updates

- None. This restores behavior `eng/architecture.md` already requires.

## Impact assessment

- Code paths affected: MCP dispatch validation.
- Data, protocol, or schema impact: None.
- Dependency or API impact: None.

## Validation

- Tests to write and run: The test above, then `make check`.
