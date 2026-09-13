# Synchronized live-turn session state

## Goal

A running turn reads `session.state` fields without holding `stateMu`, while
`commitLocked` replaces the whole `durableState` under that lock. A
configuration change that lands mid-turn is a data race. The session's locks
also do not say what they guard, so the boundary is easy to cross again.

## Desired outcome

Every read of `session.state` happens under `stateMu`, the session's
synchronization invariants are stated where the locks are declared, and an
integration test overlapping configuration changes with live turns passes under
`-race`.

## Summary of approach

Add small locked accessors on `*session` for the values turns read
(`workspaceRoot`, `openTurn`, `turnConfiguration`, `usage`, `cost`), convert
both the unlocked reads and the existing hand-rolled lock/unlock pairs to them,
and document each session mutex at its field. Add an integration test that runs
prompts while a background goroutine changes the session mode, with no
synchronization between the two.

## Related code

- `internal/agent/agent.go` - `session` struct, `commitLocked` copy-on-write
  commit, prompt and recovery entry points.
- `internal/agent/loop.go` - turn loop, tool dispatch, and usage publication
  reads.
- `internal/agent/compact.go`, `internal/agent/config_options.go` - existing
  correct lock/unlock pairs the accessors replace.
- `integration/agent_loop_test.go` - `newHarness`, `routedModel`, `setConfig`.
- `cmd/ox/main.go` - `handlerConcurrency`.

## Current state

- Relevant existing behavior: `commitLocked` clones state, applies the record,
  and assigns `value.state = next` under `stateMu`, so state is copy-on-write
  and any unlocked field read races the assignment.
- Existing patterns to follow: `reserveProviderRequest` and
  `providerRequestAvailable` already take `stateMu` around their reads.
- Constraints from the current implementation: `durableState.turnConfiguration`
  already clones, so accessors can return its result directly.

## Structural considerations

- **Encapsulation:** accessors give `session` one place that owns the `stateMu`
  boundary instead of spreading lock/unlock pairs through the turn loop.
- **Abstraction:** each accessor names a value a turn needs, not a lock
  operation.
- **Testability:** the race is observable through the existing in-process
  integration harness, which runs under `-race`.

## Test plan

- **Key behaviors to verify:** concurrent `session/set_config_option` calls and
  `session/prompt` turns complete without a race report, and the last applied
  mode is the one the session reports.
- **Test levels:** integration, in-process, under `-race`.
- **Edge cases and failure modes:** the configuration goroutine must not
  synchronize with the prompt goroutine, so the test drives prompts on the test
  goroutine and mode changes on a background goroutine that stops only after the
  prompts finish.
- **What not to test:** lock acquisition itself, and accessor return values that
  existing tests already cover.

## Implementation plan

- Add locked `*session` accessors for workspace root, open turn ID, turn
  configuration, usage, and cost.
- Replace unlocked `value.state` reads in `loop.go` and `agent.go` with the
  accessors.
- Replace equivalent hand-rolled `stateMu` pairs in `loop.go`, `agent.go`,
  `compact.go`, and `config_options.go` with the accessors.
- Document what each session mutex guards, the relationship between `configMu`
  and the prompt commit, and the `handlerConcurrency` value.
- Add the concurrent configuration-change integration test.

## Documentation updates

- Todo list item "Synchronize live-turn session-state access and cover
  concurrent configuration changes (F01, F36)".

## Impact assessment

- Code paths affected: session turn loop, tool dispatch, compaction, and
  configuration options.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the new integration test, then `make check`.
- Static checks: `make check-go`.
